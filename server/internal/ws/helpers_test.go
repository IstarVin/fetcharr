package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/istarvin/fetcharr/server/internal/api"
	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/events"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

const (
	testToken = "ws-test-token-0123456789"
	// secretHeader is stored in the headers of every seeded job; it must never
	// appear in any WebSocket message.
	secretHeader = "SECRET-HEADER-VALUE-9f8e7d"
	readTimeout  = 5 * time.Second
)

var tsRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

// harness is the real stack: store, service, event bus, hub and API handler
// behind an httptest server.
type harness struct {
	t   *testing.T
	cfg *config.Config
	st  *store.Store
	svc *service.Service
	bus *events.Bus
	hub *Hub
	srv *httptest.Server
}

func newHarness(t *testing.T, mods ...func(*Options)) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.Server.AuthToken = testToken
	for _, c := range []struct {
		id     string
		layout naming.Layout
	}{{"movies", naming.LayoutMovie}, {"tv", naming.LayoutTV}, {"misc", naming.LayoutFlat}} {
		p := filepath.Join(dir, "media", c.id)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg.Categories = append(cfg.Categories, config.Category{ID: c.id, Label: c.id, Path: p, Layout: c.layout})
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	svc := service.New(st, cfg, service.WithPublisher(bus))
	opts := Options{
		Bus:    bus,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Snapshot: func(ctx context.Context) ([]*store.Job, error) {
			js, _, err := svc.List(ctx, store.ListFilter{Statuses: jobs.ActiveStatuses()})
			return js, err
		},
	}
	for _, m := range mods {
		m(&opts)
	}
	hub := New(opts)
	srv := httptest.NewServer(api.New(api.Options{
		Config: cfg, Service: svc, Version: "test", WebSocket: hub,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}))
	h := &harness{t: t, cfg: cfg, st: st, svc: svc, bus: bus, hub: hub, srv: srv}
	t.Cleanup(func() {
		hub.Close()
		srv.Close()
		bus.Close()
		_ = st.Close()
	})
	return h
}

func (h *harness) wsURL() string { return "ws" + strings.TrimPrefix(h.srv.URL, "http") + "/api/ws" }

func authHeader(token string) http.Header {
	return http.Header{"Authorization": {"Bearer " + token}}
}

// wsc is a test client. A background goroutine reads (which also answers the
// server's pings) and queues the messages: reading with a short per-call
// context would close the connection, so tests never do that.
type wsc struct {
	*websocket.Conn
	msgs chan msg
	errc chan error // receives the read error once, when the connection ends
}

// dial connects with the valid token and registers cleanup.
func (h *harness) dial() *wsc {
	h.t.Helper()
	return h.dialURL(h.wsURL())
}

func (h *harness) dialURL(url string) *wsc {
	h.t.Helper()
	conn := h.dialRaw(url, authHeader(testToken))
	c := &wsc{Conn: conn, msgs: make(chan msg, 1024), errc: make(chan error, 1)}
	go func() {
		for {
			typ, raw, err := conn.Read(h.t.Context())
			if err != nil {
				c.errc <- err
				return
			}
			if typ != websocket.MessageText {
				h.t.Errorf("message type = %v, want text", typ)
			}
			if bytes.Contains(raw, []byte(secretHeader)) {
				h.t.Errorf("message leaks a header value: %s", raw)
			}
			m := msg{Raw: raw}
			if err := json.Unmarshal(raw, &m.Obj); err != nil {
				h.t.Errorf("message is not a JSON object: %q", raw)
				continue
			}
			m.Type, _ = m.Obj["type"].(string)
			c.msgs <- m
		}
	}()
	return c
}

// dialSilent connects but never reads, so the library never answers pings.
func (h *harness) dialSilent() *websocket.Conn {
	h.t.Helper()
	return h.dialRaw(h.wsURL(), authHeader(testToken))
}

func (h *harness) dialRaw(url string, hdr http.Header) *websocket.Conn {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(h.t.Context(), readTimeout)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		h.t.Fatalf("dial: %v", err)
	}
	c.SetReadLimit(-1)
	h.t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

// waitClients waits until the hub has n clients.
func (h *harness) waitClients(n int) {
	h.t.Helper()
	deadline := time.Now().Add(readTimeout)
	for h.hub.Clients() != n {
		if time.Now().After(deadline) {
			h.t.Fatalf("hub has %d clients, want %d", h.hub.Clients(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// api sends an authenticated REST request and returns status and body.
func (h *harness) api(method, path string, body any) (int, []byte) {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(h.t.Context(), method, h.srv.URL+path, rd)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// msg is one received WebSocket message.
type msg struct {
	Raw  []byte
	Obj  map[string]any
	Type string
}

func (m msg) job() map[string]any {
	j, _ := m.Obj["job"].(map[string]any)
	return j
}

var errTimeout = errors.New("timeout waiting for a message")

// read waits for the next message.
func read(t *testing.T, c *wsc) msg {
	t.Helper()
	m, err := tryRead(c, readTimeout)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return m
}

// tryRead waits up to d for the next message. It returns errTimeout when none
// arrives, or the connection error when the connection ended.
func tryRead(c *wsc, d time.Duration) (msg, error) {
	select {
	case m := <-c.msgs:
		return m, nil
	default:
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case m := <-c.msgs:
		return m, nil
	case err := <-c.errc:
		c.errc <- err // keep it for later callers
		return msg{}, err
	case <-timer.C:
		return msg{}, errTimeout
	}
}

// expectSilence asserts that nothing arrives within d.
func expectSilence(t *testing.T, c *wsc, d time.Duration) {
	t.Helper()
	if m, err := tryRead(c, d); err == nil {
		t.Fatalf("unexpected message: %s", m.Raw)
	}
}

// waitClosed waits for the connection to end and returns the read error.
func waitClosed(t *testing.T, c *wsc) error {
	t.Helper()
	for {
		_, err := tryRead(c, readTimeout)
		if err == nil {
			continue // drain queued messages
		}
		if errors.Is(err, errTimeout) {
			t.Fatal("connection did not close")
		}
		return err
	}
}

// ---- fixtures ----------------------------------------------------------

func contractFile(t *testing.T, name string) []byte {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "..", "contract", name))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(contractFile(t, name), &v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

// fixtureJobs decodes the job objects of a WebSocket fixture into store jobs
// carrying a secret header.
func fixtureJobs(t *testing.T, name string) []*store.Job {
	t.Helper()
	raw := contractFile(t, name)
	var one struct {
		Job  *api.JobDTO  `json:"job"`
		Jobs []api.JobDTO `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &one); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var out []*store.Job
	if one.Job != nil {
		out = append(out, jobFromDTO(*one.Job))
	}
	for _, d := range one.Jobs {
		out = append(out, jobFromDTO(d))
	}
	return out
}

func jobFromDTO(d api.JobDTO) *store.Job {
	j := &store.Job{
		ID: d.ID, BatchID: d.BatchID, Category: d.Category, URL: d.URL,
		Headers: map[string]string{"X-Secret": secretHeader},
		Title:   d.Title, ShowName: d.ShowName, Season: d.Season, Episode: d.Episode, Year: d.Year,
		Status:   jobs.Status(d.Status),
		FilePath: d.FilePath, ErrorCode: d.ErrorCode, ErrorMessage: d.ErrorMessage,
		Warnings:  d.Warnings,
		CreatedAt: d.CreatedAt.Time, QueuedAt: d.QueuedAt.Time,
	}
	for _, s := range d.Subtitles {
		j.Subtitles = append(j.Subtitles, store.Subtitle(s))
	}
	if d.Progress != nil {
		p := store.Progress(*d.Progress)
		j.Progress = &p
	}
	if d.StartedAt.IsSet() {
		t := d.StartedAt.Time
		j.StartedAt = &t
	}
	if d.FinishedAt.IsSet() {
		t := d.FinishedAt.Time
		j.FinishedAt = &t
	}
	return j
}

func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// assertShape checks that got has the same key sets (recursively) and JSON
// types as want; values are ignored.
func assertShape(t *testing.T, path string, want, got any) {
	t.Helper()
	if wt, gt := jsonType(want), jsonType(got); wt != gt {
		t.Errorf("%s: type %s, want %s", path, gt, wt)
		return
	}
	switch w := want.(type) {
	case map[string]any:
		g := got.(map[string]any)
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				t.Errorf("%s: missing key %q", path, k)
				continue
			}
			assertShape(t, path+"."+k, wv, gv)
		}
		for k := range g {
			if _, ok := w[k]; !ok {
				t.Errorf("%s: unexpected key %q", path, k)
			}
		}
	case []any:
		g := got.([]any)
		for i, gv := range g {
			if len(w) > 0 {
				assertShape(t, path+"[]", w[min(i, len(w)-1)], gv)
			}
		}
	}
}

// assertTimestamps checks every *_at string against the wire format.
func assertTimestamps(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			if s, ok := c.(string); ok && strings.HasSuffix(k, "_at") && !tsRe.MatchString(s) {
				t.Errorf("%s.%s = %q does not match the timestamp format", path, k, s)
			}
			assertTimestamps(t, path+"."+k, c)
		}
	case []any:
		for _, c := range x {
			assertTimestamps(t, path+"[]", c)
		}
	}
}

// ---- synthetic jobs ----------------------------------------------------

var idSeq struct {
	sync.Mutex
	n int
}

// newJob builds a job for publishing straight onto the bus.
func newJob(status jobs.Status, percent float64) *store.Job {
	idSeq.Lock()
	idSeq.n++
	n := idSeq.n
	idSeq.Unlock()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	return &store.Job{
		ID: "job-" + string(rune('a'+n%26)) + strings.Repeat("x", n/26), Category: "misc",
		URL: "https://x.example/" + string(rune('a'+n%26)), Title: store.Ptr("T"),
		Headers: map[string]string{"X-Secret": secretHeader},
		Status:  status, Progress: &store.Progress{Percent: &percent},
		CreatedAt: now, QueuedAt: now,
	}
}

func withProgress(j *store.Job, percent float64) *store.Job {
	c := *j
	c.Progress = &store.Progress{Percent: &percent}
	return &c
}

func withStatus(j *store.Job, s jobs.Status) *store.Job {
	c := *j
	c.Status = s
	return &c
}

func added(j *store.Job) service.Change   { return service.Change{Kind: service.ChangeAdded, Job: j} }
func updated(j *store.Job) service.Change { return service.Change{Kind: service.ChangeUpdated, Job: j} }

func percentOf(m msg) float64 {
	p, _ := m.job()["progress"].(map[string]any)
	v, _ := p["percent"].(float64)
	return v
}

func timeoutCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(t.Context(), readTimeout)
}

func jsonField(t *testing.T, body []byte, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	s, _ := m[key].(string)
	if s == "" {
		t.Fatalf("no %q in %s", key, body)
	}
	return s
}

func emptySnapshot(mods ...func(*Options)) []func(*Options) {
	return append([]func(*Options){func(o *Options) {
		o.Snapshot = func(context.Context) ([]*store.Job, error) { return nil, nil }
	}}, mods...)
}
