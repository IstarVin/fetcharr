package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/istarvin/fetcharr/server/internal/api"
	"github.com/istarvin/fetcharr/server/internal/events"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
	"github.com/istarvin/fetcharr/server/internal/ws"
)

// stack is the real server: API handler, WebSocket hub, event bus, service
// and runner over the fake yt-dlp.
type stack struct {
	t   *testing.T
	g   *rig
	srv *httptest.Server
}

func newStack(t *testing.T) *stack {
	t.Helper()
	bus := events.NewBus()
	g := newRig(t, rigOpts{concurrency: 2, mod: func(o *runner.Options) { o.Publisher = bus }})
	// Rebuild the service on the bus (newRig wired the recorder).
	g.svc = service.New(g.st, g.cfg, service.WithPublisher(bus), service.WithController(g.r))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := ws.New(ws.Options{
		Bus: bus, Logger: log, ProgressInterval: 50 * time.Millisecond,
		Snapshot: func(ctx context.Context) ([]*store.Job, error) {
			js, _, err := g.svc.List(ctx, store.ListFilter{Statuses: jobs.ActiveStatuses()})
			return js, err
		},
	})
	srv := httptest.NewServer(api.New(api.Options{
		Config: g.cfg, Service: g.svc, Version: "test", WebSocket: hub, Logger: log,
	}))
	t.Cleanup(func() {
		srv.Close()
		hub.Close()
		bus.Close()
	})
	return &stack{t: t, g: g, srv: srv}
}

func (s *stack) call(method, path string, body any) (int, map[string]any) {
	s.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(s.t.Context(), method, s.srv.URL+path, rd)
	if err != nil {
		s.t.Fatal(err)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

// wsJob is the part of a WebSocket job message the tests look at.
type wsMsg struct {
	Type string `json:"type"`
	Job  struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		ErrorCode    string `json:"error_code"`
		ErrorMessage string `json:"error_message"`
		Progress     *struct {
			Percent         *float64 `json:"percent"`
			DownloadedBytes *int64   `json:"downloaded_bytes"`
		} `json:"progress"`
	} `json:"job"`
}

type wsClient struct {
	t    *testing.T
	msgs chan wsMsg
}

func (s *stack) dialWS() *wsClient {
	s.t.Helper()
	url := "ws" + strings.TrimPrefix(s.srv.URL, "http") + "/api/ws"
	ctx, cancel := context.WithTimeout(s.t.Context(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, url, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		s.t.Fatalf("dial: %v", err)
	}
	s.t.Cleanup(func() { _ = conn.CloseNow() })
	c := &wsClient{t: s.t, msgs: make(chan wsMsg, 4096)}
	go func() {
		for {
			_, raw, err := conn.Read(s.t.Context())
			if err != nil {
				close(c.msgs)
				return
			}
			var m wsMsg
			if json.Unmarshal(raw, &m) == nil {
				c.msgs <- m
			}
		}
	}()
	if m := c.next(func(m wsMsg) bool { return m.Type == "snapshot" }); m.Type != "snapshot" {
		s.t.Fatal("no snapshot")
	}
	return c
}

// next returns the first message that satisfies ok, skipping the others.
func (c *wsClient) next(ok func(wsMsg) bool) wsMsg {
	c.t.Helper()
	timeout := time.After(waitTimeout)
	for {
		select {
		case m, open := <-c.msgs:
			if !open {
				c.t.Fatal("WebSocket closed")
			}
			if ok(m) {
				return m
			}
		case <-timeout:
			c.t.Fatal("timeout waiting for a WebSocket message")
		}
	}
}

func status(id, st string) func(wsMsg) bool {
	return func(m wsMsg) bool { return m.Job.ID == id && m.Job.Status == st }
}

// TestPipelineOverHTTPAndWebSocket is the S3 done-when: a job moves
// pending -> downloading -> processing with live progress over WebSocket, and
// pause/resume continues where the download stopped.
func TestPipelineOverHTTPAndWebSocket(t *testing.T) {
	setDuration(t, "2.5")
	s := newStack(t)
	c := s.dialWS()

	code, job := s.call(http.MethodPost, "/api/jobs", map[string]any{
		"category": "misc", "url": "https://example.com/video.m3u8", "title": "Integration Video",
		"headers": map[string]string{"Referer": "https://example.com/"},
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, job)
	}
	id, _ := job["id"].(string)

	c.next(func(m wsMsg) bool { return m.Type == "job_added" && m.Job.ID == id && m.Job.Status == "pending" })
	c.next(status(id, "downloading"))

	// Live progress: at least two updates with increasing downloaded_bytes.
	var seen []int64
	for len(seen) < 3 {
		m := c.next(func(m wsMsg) bool {
			return m.Job.ID == id && m.Job.Status == "downloading" && m.Job.Progress != nil && m.Job.Progress.DownloadedBytes != nil
		})
		n := *m.Job.Progress.DownloadedBytes
		if len(seen) > 0 && n <= seen[len(seen)-1] {
			t.Fatalf("downloaded_bytes did not increase: %v then %d", seen, n)
		}
		seen = append(seen, n)
	}
	lastBeforePause := seen[len(seen)-1]

	// Pause: status paused, the partial file stays in the work dir.
	if code, got := s.call(http.MethodPost, "/api/jobs/"+id+"/pause", nil); code != http.StatusOK || got["status"] != "paused" {
		t.Fatalf("pause: %d %v", code, got)
	}
	paused := c.next(status(id, "paused"))
	if paused.Job.Progress == nil {
		t.Error("paused job lost its progress")
	}
	waitIdle(t, s.g.r)
	part := filepath.Join(s.g.workDir(id), "media.mp4.part")
	fi, err := os.Stat(part)
	if err != nil {
		t.Fatalf("partial file after pause: %v", err)
	}
	if fi.Size() < lastBeforePause {
		t.Errorf(".part is %d bytes, less than the %d already reported", fi.Size(), lastBeforePause)
	}
	// Nothing more is published for the paused job.
	time.Sleep(300 * time.Millisecond)
drain:
	for {
		select {
		case m := <-c.msgs:
			if m.Job.ID == id && m.Job.Status == "downloading" {
				t.Errorf("progress for a paused job: %+v", m.Job)
			}
		default:
			break drain
		}
	}

	// Resume: pending, downloading again, and progress continues from there.
	if code, got := s.call(http.MethodPost, "/api/jobs/"+id+"/resume", nil); code != http.StatusOK || got["status"] != "pending" {
		t.Fatalf("resume: %d %v", code, got)
	}
	c.next(status(id, "pending"))
	c.next(status(id, "downloading"))
	first := c.next(func(m wsMsg) bool {
		return m.Job.ID == id && m.Job.Status == "downloading" && m.Job.Progress != nil && m.Job.Progress.DownloadedBytes != nil &&
			*m.Job.Progress.DownloadedBytes > 0
	})
	if got := *first.Job.Progress.DownloadedBytes; got < lastBeforePause {
		t.Errorf("first progress after resume = %d, below %d before the pause: the download restarted", got, lastBeforePause)
	}

	done := c.next(status(id, "processing"))
	if p := done.Job.Progress; p == nil || p.Percent == nil || *p.Percent != 100 {
		t.Errorf("processing progress = %+v", p)
	}
	if code, got := s.call(http.MethodGet, "/api/jobs/"+id, nil); code != http.StatusOK || got["status"] != "processing" {
		t.Errorf("GET job: %d %v", code, got)
	}
}

func TestFailedDownloadOverHTTPAndWebSocket(t *testing.T) {
	setDuration(t, "0")
	s := newStack(t)
	c := s.dialWS()
	code, job := s.call(http.MethodPost, "/api/jobs", map[string]any{
		"category": "misc", "url": "https://example.com/fail.m3u8", "title": "Broken",
	})
	if code != http.StatusCreated {
		t.Fatalf("create: %d %v", code, job)
	}
	id, _ := job["id"].(string)
	failed := c.next(status(id, "failed"))
	if failed.Job.ErrorCode != "download_failed" || !strings.Contains(failed.Job.ErrorMessage, "simulated failure") {
		t.Errorf("failed job: %+v", failed.Job)
	}
	if code, got := s.call(http.MethodGet, "/api/jobs/"+id, nil); code != http.StatusOK ||
		got["status"] != "failed" || got["error_code"] != "download_failed" || got["finished_at"] == nil {
		t.Errorf("GET job: %d %v", code, got)
	}
}

func TestCancelOverHTTPDeletesWorkDir(t *testing.T) {
	setDuration(t, "3")
	s := newStack(t)
	c := s.dialWS()
	_, job := s.call(http.MethodPost, "/api/jobs", map[string]any{
		"category": "misc", "url": "https://example.com/c.m3u8", "title": "Cancelled",
	})
	id, _ := job["id"].(string)
	c.next(func(m wsMsg) bool {
		return m.Job.ID == id && m.Job.Status == "downloading" && m.Job.Progress != nil
	})
	if code, got := s.call(http.MethodPost, "/api/jobs/"+id+"/cancel", nil); code != http.StatusOK || got["status"] != "cancelled" {
		t.Fatalf("cancel: %d %v", code, got)
	}
	waitGone(t, s.g.workDir(id))
}
