package api

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
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

const (
	testToken = "test-token-0123456789"
	// secretHeaderValue is put into the headers of seeded jobs; it must never
	// appear in any response or log line.
	secretHeaderValue = "SECRET-HEADER-VALUE-9f8e7d"
)

var tsRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

func contractDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs(filepath.Join("..", "..", "..", "contract"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func fixture(t *testing.T, name string) any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(contractDir(t), name))
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return v
}

func fixtureRaw(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(contractDir(t), name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fakeYtdlpPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake-ytdlp"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeUpdater struct {
	latest string
	res    UpdateResult
	err    error
	calls  int
}

func (f *fakeUpdater) LatestVersion(context.Context) (string, error) {
	return f.latest, f.err
}

func (f *fakeUpdater) Update(context.Context) (UpdateResult, error) {
	f.calls++
	return f.res, f.err
}

type recPublisher struct {
	mu      sync.Mutex
	changes []service.Change
}

func (r *recPublisher) Publish(c service.Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, c)
}

type env struct {
	t       *testing.T
	cfg     *config.Config
	st      *store.Store
	svc     *service.Service
	h       http.Handler
	updater *fakeUpdater
	pub     *recPublisher
	logs    *bytes.Buffer

	mu     sync.Mutex
	bodies [][]byte
}

// newEnv builds a real handler over a real store. mods may adjust the config
// before the handler is built.
func newEnv(t *testing.T, mods ...func(*config.Config)) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.Server.AuthToken = testToken
	cfg.Ytdlp.Binary = fakeYtdlpPath(t)
	for _, c := range []struct {
		id, label string
		layout    naming.Layout
	}{
		{"movies", "Movies", naming.LayoutMovie},
		{"tv", "TV Shows", naming.LayoutTV},
		{"misc", "Misc", naming.LayoutFlat},
	} {
		p := filepath.Join(dir, "media", c.id)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg.Categories = append(cfg.Categories, config.Category{ID: c.id, Label: c.label, Path: p, Layout: c.layout})
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, m := range mods {
		m(cfg)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	e := &env{t: t, cfg: cfg, st: st, updater: &fakeUpdater{res: UpdateResult{Previous: "2026.09.01", Current: "2026.09.30"}},
		pub: &recPublisher{}, logs: &bytes.Buffer{}}
	e.svc = service.New(st, cfg, service.WithPublisher(e.pub))
	e.h = New(Options{
		Config: cfg, Service: e.svc, Version: "1.0.0", Updater: e.updater,
		Logger:       slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		YtdlpVersion: func(context.Context, string) (string, error) { return "2099.01.01", nil },
	})
	t.Cleanup(func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		for _, b := range e.bodies {
			if bytes.Contains(b, []byte(secretHeaderValue)) {
				t.Errorf("a response leaked a header value: %s", b)
			}
		}
		if strings.Contains(e.logs.String(), secretHeaderValue) {
			t.Errorf("logs leaked a header value")
		}
	})
	return e
}

type resp struct {
	Code   int
	Header http.Header
	Raw    []byte
}

// JSON decodes the body into a generic value.
func (r resp) JSON(t *testing.T) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(r.Raw, &v); err != nil {
		t.Fatalf("response is not JSON (%d): %q", r.Code, r.Raw)
	}
	return v
}

func (r resp) Obj(t *testing.T) map[string]any {
	t.Helper()
	m, ok := r.JSON(t).(map[string]any)
	if !ok {
		t.Fatalf("response is not an object: %s", r.Raw)
	}
	return m
}

// ErrCode returns error.code of an error envelope.
func (r resp) ErrCode(t *testing.T) string {
	t.Helper()
	e, ok := r.Obj(t)["error"].(map[string]any)
	if !ok {
		t.Fatalf("not an error envelope: %s", r.Raw)
	}
	code, _ := e["code"].(string)
	if msg, _ := e["message"].(string); msg == "" {
		t.Fatalf("error without message: %s", r.Raw)
	}
	return code
}

// request sends a request with the valid token. body may be nil, a string
// (sent verbatim) or any value (JSON encoded).
func (e *env) request(method, path string, body any) resp {
	return e.requestWith(method, path, body, "Bearer "+testToken)
}

func (e *env) requestWith(method, path string, body any, authorization string) resp {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequestWithContext(e.t.Context(), method, path, rd)
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	out := resp{Code: rec.Code, Header: rec.Header(), Raw: rec.Body.Bytes()}
	e.mu.Lock()
	e.bodies = append(e.bodies, out.Raw)
	e.mu.Unlock()
	return out
}

func (e *env) want(r resp, code int) resp {
	e.t.Helper()
	if r.Code != code {
		e.t.Fatalf("status = %d, want %d; body %s", r.Code, code, r.Raw)
	}
	return r
}

// seed inserts a job directly into the store (with a secret header).
func (e *env) seed(j *store.Job) *store.Job {
	e.t.Helper()
	if j.Headers == nil {
		j.Headers = map[string]string{"X-Secret": secretHeaderValue}
	}
	if err := e.st.Insert(context.Background(), j); err != nil {
		e.t.Fatal(err)
	}
	return j
}

// seedFixture inserts the job described by contract/<name> with the exact
// values (id, status, timestamps, progress) of the fixture.
func (e *env) seedFixture(name string) *store.Job {
	e.t.Helper()
	var d JobDTO
	if err := json.Unmarshal(fixtureRaw(e.t, name), &d); err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	return e.seed(jobFromDTO(d))
}

func jobFromDTO(d JobDTO) *store.Job {
	j := &store.Job{
		ID: d.ID, BatchID: d.BatchID, Category: d.Category, URL: d.URL,
		Title: d.Title, ShowName: d.ShowName, Season: d.Season, Episode: d.Episode, Year: d.Year,
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

// newJob returns a minimal pending job for seeding.
func newJob(url string, st jobs.Status) *store.Job {
	now := time.Now().UTC()
	j := &store.Job{Category: "misc", URL: url, Title: store.Ptr("T " + url), Status: st}
	if st != jobs.StatusPending {
		j.StartedAt = &now
	}
	if jobs.IsFinished(st) {
		j.FinishedAt = &now
	}
	return j
}

// jsonType names the JSON type of a decoded value.
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
// types as want. Values are ignored. Keys in relaxed may differ in type (and
// are not descended into): they depend on the job state the fixture shows.
func assertShape(t *testing.T, path string, want, got any, relaxed map[string]bool) {
	t.Helper()
	wt, gt := jsonType(want), jsonType(got)
	if wt != gt {
		t.Errorf("%s: type %s, want %s", path, gt, wt)
		return
	}
	switch w := want.(type) {
	case map[string]any:
		g := got.(map[string]any)
		for k := range w {
			if _, ok := g[k]; !ok {
				t.Errorf("%s: missing key %q", path, k)
			}
		}
		for k := range g {
			if _, ok := w[k]; !ok {
				t.Errorf("%s: unexpected key %q", path, k)
			}
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok || relaxed[k] {
				continue
			}
			assertShape(t, path+"."+k, wv, gv, relaxed)
		}
	case []any:
		g := got.([]any)
		if len(w) == 0 {
			return
		}
		for i, gv := range g {
			assertShape(t, path+"["+itoa(i)+"]", w[min(i, len(w)-1)], gv, relaxed)
		}
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// assertTimestamps checks every *_at string in v against the wire format.
func assertTimestamps(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			if strings.HasSuffix(k, "_at") {
				if s, ok := c.(string); ok && !tsRe.MatchString(s) {
					t.Errorf("%s.%s = %q does not match the timestamp format", path, k, s)
				}
				if c != nil && jsonType(c) != "string" {
					t.Errorf("%s.%s is %s, want string or null", path, k, jsonType(c))
				}
			}
			assertTimestamps(t, path+"."+k, c)
		}
	case []any:
		for i, c := range x {
			assertTimestamps(t, path+"["+itoa(i)+"]", c)
		}
	}
}

func assertEqualJSON(t *testing.T, what string, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		w, _ := json.MarshalIndent(want, "", " ")
		g, _ := json.MarshalIndent(got, "", " ")
		t.Errorf("%s differs\nwant: %s\n got: %s", what, w, g)
	}
}

// jobStateKeys are the job fields whose type depends on the lifecycle state.
var jobStateKeys = map[string]bool{
	"progress": true, "started_at": true, "finished_at": true,
	"error_code": true, "error_message": true, "file_path": true,
}

func (e *env) logsLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func newJobWith(url, category string) *store.Job {
	j := newJob(url, jobs.StatusPending)
	j.Category = category
	return j
}

// writeLibraryFile creates an empty file at rel inside the root of category cat.
func writeLibraryFile(t *testing.T, e *env, cat, rel string) string {
	t.Helper()
	c, ok := e.cfg.Category(cat)
	if !ok {
		t.Fatalf("no category %s", cat)
	}
	p := filepath.Join(c.Path, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func storeFilterBatch(id string) store.ListFilter { return store.ListFilter{BatchID: id} }

// id extracts the "id" of a job response.
func (r resp) ID(t *testing.T) string {
	t.Helper()
	id, _ := r.Obj(t)["id"].(string)
	if id == "" {
		t.Fatalf("no id in %s", r.Raw)
	}
	return id
}
