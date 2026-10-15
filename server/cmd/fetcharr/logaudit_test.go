package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/istarvin/fetcharr/server/internal/config"
)

// Secrets that must never reach a log (spec §9). headerSecrets and
// credentialSecrets must also never reach an HTTP response or WebSocket
// frame; the URL query secrets legitimately come back in the "url" field of
// the job the client created, but never in warnings or error messages.
const (
	secCookie    = "SECRET-COOKIE-123"
	secAuth      = "SECRET-AUTH-456"
	secQuery     = "SECRET-QUERY-789"
	secSub404    = "SECRET-SUBQ-404"
	secSubVTT    = "SECRET-SUBQ-VTT"
	secFailQuery = "SECRET-FAIL-QUERY-999"
	secToken     = "SECRET-SERVER-TOKEN-000"
	secWrongTok  = "SECRET-WRONGTOKEN-555"
	secJFKey     = "SECRET-JF-KEY-111"
	secJFKeyBad  = "SECRET-JF-KEY-BAD-222"
	secUserinfo  = "SECRET-USERINFO-333"
)

var (
	headerSecrets     = []string{secCookie, secAuth, secToken, secWrongTok, secJFKey, secJFKeyBad, secUserinfo}
	querySecrets      = []string{secQuery, secSub404, secSubVTT, secFailQuery}
	allSecrets        = append(append([]string{}, headerSecrets...), querySecrets...)
	logAuditStatusEnd = map[string]bool{"completed": true, "failed": true, "cancelled": true}
)

// syncBuffer is a concurrency-safe log sink.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// auditClient talks to the server and remembers every response body.
type auditClient struct {
	t     *testing.T
	base  string
	token string

	mu   sync.Mutex
	seen []string
}

func (c *auditClient) record(s string) {
	c.mu.Lock()
	c.seen = append(c.seen, s)
	c.mu.Unlock()
}

func (c *auditClient) do(method, path string, body any, token string) (int, []byte) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(c.t.Context(), method, c.base+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	c.record(string(b))
	return resp.StatusCode, b
}

func (c *auditClient) call(method, path string, body any) (int, []byte) {
	c.t.Helper()
	return c.do(method, path, body, c.token)
}

type auditJob struct {
	ID           string   `json:"id"`
	Status       string   `json:"status"`
	ErrorMessage *string  `json:"error_message"`
	Warnings     []string `json:"warnings"`
}

func (c *auditClient) create(body map[string]any) auditJob {
	c.t.Helper()
	code, b := c.call(http.MethodPost, "/api/jobs", body)
	var j auditJob
	if code != http.StatusCreated {
		c.t.Fatalf("create job: %d %s", code, b)
	}
	if err := json.Unmarshal(b, &j); err != nil {
		c.t.Fatal(err)
	}
	return j
}

func (c *auditClient) get(id string) auditJob {
	c.t.Helper()
	code, b := c.call(http.MethodGet, "/api/jobs/"+id, nil)
	var j auditJob
	if code != http.StatusOK {
		c.t.Fatalf("get job: %d %s", code, b)
	}
	if err := json.Unmarshal(b, &j); err != nil {
		c.t.Fatal(err)
	}
	return j
}

func (c *auditClient) waitTerminal(id string) auditJob {
	c.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		j := c.get(id)
		if logAuditStatusEnd[j.Status] {
			return j
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("job %s stuck in %s", id, j.Status)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestLogsNeverContainSecrets runs the whole server (fake yt-dlp, real ffmpeg
// for subtitle conversion, a fake Jellyfin) through created, completed,
// failed, paused, resumed, cancelled, retried and deleted jobs with a DEBUG
// logger, then checks that no secret reached the log (spec §9) or, for the
// header values and credentials, any HTTP response or WebSocket frame.
func TestLogsNeverContainSecrets(t *testing.T) {
	for _, bin := range []string{"python3", "ffmpeg"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not available", bin)
		}
	}
	fake, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake-ytdlp"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_YTDLP_DURATION", "0.6")

	// Subtitle hosts: one that 404s, one serving WebVTT (converted by ffmpeg).
	var subMu sync.Mutex
	var subCookies []string
	subs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subMu.Lock()
		subCookies = append(subCookies, r.Header.Get("Cookie"))
		subMu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/missing") {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nhello\n")
	}))
	defer subs.Close()

	// Jellyfin: records the token it was sent.
	var jfMu sync.Mutex
	var jfHits []string
	jelly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jfMu.Lock()
		jfHits = append(jfHits, r.URL.Path+" token="+r.Header.Get("X-Emby-Token"))
		jfMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer jelly.Close()

	// A second Jellyfin that refuses connections: the transport error carries
	// the request URL, including the userinfo.
	dead, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := dead.Addr().String()
	_ = dead.Close()

	cfg := testConfig(t)
	cfg.Ytdlp.Binary = fake
	cfg.Server.AuthToken = secToken
	cfg.Server.LogLevel = "debug"
	cfg.Downloads.Concurrency = 2
	cfg.MediaServers = []config.MediaServer{
		{Type: config.MediaServerJellyfin, URL: jelly.URL, APIKey: secJFKey},
		{Type: config.MediaServerJellyfin, URL: "http://user:" + secUserinfo + "@" + deadAddr, APIKey: secJFKeyBad},
	}

	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg, logger, ln) }()

	c := &auditClient{t: t, base: "http://" + ln.Addr().String(), token: secToken}

	// An unauthenticated and a wrongly authenticated request.
	if code, _ := c.do(http.MethodGet, "/api/jobs?token="+secWrongTok, nil, ""); code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := c.do(http.MethodGet, "/api/jobs", nil, secWrongTok); code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", code)
	}

	// A WebSocket client collects every frame.
	wctx, wcancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer wcancel()
	hdr := http.Header{"Authorization": {"Bearer " + secToken}}
	wc, resp, err := websocket.Dial(wctx, "ws://"+ln.Addr().String()+"/api/ws", &websocket.DialOptions{HTTPHeader: hdr})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wc.CloseNow() }()
	wsDone := make(chan struct{})
	go func() {
		defer close(wsDone)
		for {
			_, raw, err := wc.Read(wctx)
			if err != nil {
				return
			}
			c.record(string(raw))
		}
	}()

	headers := map[string]string{"Cookie": "sess=" + secCookie, "Authorization": "Bearer " + secAuth}
	okURL := "https://media.example/video/a?token=" + secQuery + "#frag"
	okJob := c.create(map[string]any{
		"url": okURL, "category": "misc", "title": "Good", "headers": headers,
		"subtitles": []map[string]string{
			{"url": subs.URL + "/missing.vtt?token=" + secSub404, "lang": "en"},
			{"url": subs.URL + "/present.vtt?token=" + secSubVTT, "lang": "fr"},
		},
	})
	failJob := c.create(map[string]any{
		"url": "https://fail.example/x?token=" + secFailQuery, "category": "misc", "title": "Bad", "headers": headers,
	})
	slowJob := c.create(map[string]any{
		"url": "https://media.example/video/slow?token=" + secQuery, "category": "misc", "title": "Slow", "headers": headers,
	})

	// Duplicate and invalid requests carry the same values.
	c.call(http.MethodPost, "/api/jobs", map[string]any{
		"url": okURL, "category": "misc", "title": "Good", "headers": headers,
	})
	c.call(http.MethodPost, "/api/jobs", map[string]any{
		"url": "ftp://x.example/a?token=" + secQuery, "category": "misc", "title": "Nope", "headers": headers,
	})
	c.call(http.MethodGet, "/api/jobs?status=downloading&token="+secQuery, nil)

	// Pause / resume / cancel, whatever state the slow job is in.
	for _, action := range []string{"pause", "resume", "pause", "resume"} {
		c.call(http.MethodPost, "/api/jobs/"+slowJob.ID+"/"+action, nil)
	}
	time.Sleep(150 * time.Millisecond)
	c.call(http.MethodPost, "/api/jobs/"+slowJob.ID+"/cancel", nil)

	gotOK := c.waitTerminal(okJob.ID)
	gotFail := c.waitTerminal(failJob.ID)
	c.waitTerminal(slowJob.ID)
	if gotOK.Status != "completed" {
		t.Errorf("good job = %+v, want completed", gotOK)
	}
	if gotFail.Status != "failed" {
		t.Errorf("fail job = %+v, want failed", gotFail)
	}
	if len(gotOK.Warnings) == 0 {
		t.Errorf("the 404 subtitle produced no warning: %+v", gotOK)
	}

	// Retry the failed job, then clean up.
	c.call(http.MethodPost, "/api/jobs/"+failJob.ID+"/retry", nil)
	c.waitTerminal(failJob.ID)
	for _, id := range []string{failJob.ID, slowJob.ID} {
		c.call(http.MethodDelete, "/api/jobs/"+id, nil)
	}
	c.call(http.MethodGet, "/api/info", nil)
	c.call(http.MethodGet, "/api/jobs", nil)

	// Responses and warnings/error messages must not carry query secrets.
	for _, id := range []string{okJob.ID, failJob.ID} {
		if code, b := c.call(http.MethodGet, "/api/jobs/"+id, nil); code == http.StatusOK {
			var j auditJob
			if err := json.Unmarshal(b, &j); err != nil {
				t.Fatal(err)
			}
			text := strings.Join(j.Warnings, "\n")
			if j.ErrorMessage != nil {
				text += "\n" + *j.ErrorMessage
			}
			for _, s := range allSecrets {
				if strings.Contains(text, s) {
					t.Errorf("job %s warnings/error message contain %s: %q", id, s, text)
				}
			}
		}
	}

	// Graceful shutdown flushes the debounced media server refresh.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not return after cancel")
	}
	wcancel()
	<-wsDone

	// The scenario really exercised what it claims to.
	out := logs.String()
	for _, want := range []string{
		"job created", "job started", "job downloaded", "job completed", "job failed",
		"websocket connected", "media server refresh failed", "media server refresh requested",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q; the scenario did not reach that code path", want)
		}
	}
	jfMu.Lock()
	hits := strings.Join(jfHits, "\n")
	jfMu.Unlock()
	if !strings.Contains(hits, "token="+secJFKey) {
		t.Errorf("Jellyfin was not refreshed with its api key: %q", hits)
	}
	subMu.Lock()
	cookies := strings.Join(subCookies, "\n")
	subMu.Unlock()
	if !strings.Contains(cookies, secCookie) {
		t.Errorf("subtitle requests did not carry the job headers: %q", cookies)
	}

	for _, s := range allSecrets {
		if strings.Contains(out, s) {
			t.Errorf("secret %s found in the log:\n%s", s, grepLines(out, s))
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range headerSecrets {
		for _, body := range c.seen {
			if strings.Contains(body, s) {
				t.Errorf("secret %s found in an HTTP response or WebSocket frame: %.300s", s, body)
				break
			}
		}
	}
	if len(c.seen) < 20 {
		t.Errorf("only %d responses/frames recorded", len(c.seen))
	}
}

func grepLines(text, sub string) string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// server.log_level decides what the logger emits.
func TestNewLoggerHonoursLevel(t *testing.T) {
	cases := []struct {
		level string
		want  []string // emitted messages, in order
	}{
		{"debug", []string{"d", "i", "w", "e"}},
		{"info", []string{"i", "w", "e"}},
		{"warn", []string{"w", "e"}},
		{"error", []string{"e"}},
		{"bogus", []string{"i", "w", "e"}},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		l := newLoggerTo(&buf, tc.level)
		l.Debug("d")
		l.Info("i")
		l.Warn("w")
		l.Error("e")
		var got []string
		for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
			if _, msg, ok := strings.Cut(line, "msg="); ok {
				got = append(got, msg)
			}
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("level %q emitted %v, want %v", tc.level, got, tc.want)
		}
	}
}
