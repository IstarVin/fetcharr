package process

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

const waitTimeout = 15 * time.Second

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// recorder is a service.Publisher that keeps every change.
type recorder struct {
	mu      sync.Mutex
	changes []service.Change
}

func (r *recorder) Publish(c service.Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, c)
}

func (r *recorder) statuses(id string) []jobs.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []jobs.Status
	for _, c := range r.changes {
		if c.Job != nil && c.Job.ID == id {
			out = append(out, c.Job.Status)
		}
	}
	return out
}

// fixture is a store, config and Processor over temp directories.
type fixture struct {
	t     *testing.T
	cfg   *config.Config
	st    *store.Store
	pub   *recorder
	logs  *syncBuf
	proc  *Processor
	media string // parent of the category roots
	n     int
}

func (f *fixture) root(cat string) string { return filepath.Join(f.media, cat) }

func newFixture(t *testing.T, mod func(*Options)) *fixture {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.Downloads.MinFreeSpace = 0
	cfg.Downloads.AutoRetries = 0
	media := filepath.Join(dir, "media")
	for _, c := range []struct {
		id     string
		layout naming.Layout
	}{{"movies", naming.LayoutMovie}, {"tv", naming.LayoutTV}, {"misc", naming.LayoutFlat}} {
		p := filepath.Join(media, c.id)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		cfg.Categories = append(cfg.Categories, config.Category{ID: c.id, Label: c.id, Path: p, Layout: c.layout})
	}
	if err := os.MkdirAll(cfg.WorkDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &fixture{t: t, cfg: cfg, st: st, pub: &recorder{}, logs: &syncBuf{}, media: media}
	o := Options{
		Config: cfg, Store: st, Publisher: f.pub,
		Logger: slog.New(slog.NewTextHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if mod != nil {
		mod(&o)
	}
	f.proc = New(o)
	return f
}

// job inserts a movie job in status processing with a downloaded media file
// in its work directory and returns both.
func (f *fixture) job(mods ...func(*store.Job)) (*store.Job, string) {
	f.t.Helper()
	f.n++
	id := store.NewID()
	j := &store.Job{
		ID:       id,
		Category: "movies",
		URL:      fmt.Sprintf("https://example.com/v-%d", f.n),
		Title:    store.Ptr("Dune"),
		Year:     store.Ptr(2024),
		Status:   jobs.StatusProcessing,
		WorkDir:  f.cfg.JobWorkDir(id),
	}
	for _, m := range mods {
		m(j)
	}
	if err := f.st.Insert(context.Background(), j); err != nil {
		f.t.Fatal(err)
	}
	wd := f.cfg.JobWorkDir(j.ID)
	if err := os.MkdirAll(wd, 0o755); err != nil {
		f.t.Fatal(err)
	}
	media := filepath.Join(wd, "media.mp4")
	if err := os.WriteFile(media, []byte("video-bytes"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	got, err := f.st.Get(context.Background(), j.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return got, media
}

func (f *fixture) get(id string) *store.Job {
	f.t.Helper()
	j, err := f.st.Get(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return j
}

func (f *fixture) process(j *store.Job, media string) error {
	f.t.Helper()
	return f.proc.Process(context.Background(), j, media)
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// listFiles returns the relative paths of all files under root.
func listFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func needFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
}

const srtSample = "1\n00:00:01,000 --> 00:00:02,500\nHello\n\n2\n00:00:03,000 --> 00:00:04,000\nWorld\n"

const vttSample = "WEBVTT\n\n00:00:01.000 --> 00:00:02.500\nHello\n\n00:00:03.000 --> 00:00:04.000\nWorld\n"

// route is a canned response of subServer.
type route struct {
	status int
	body   string
}

// subServer serves subtitles and records the request headers.
type subServer struct {
	*httptest.Server
	mu   sync.Mutex
	seen []http.Header
}

func newSubServer(t *testing.T, routes map[string]route) *subServer {
	t.Helper()
	s := &subServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.Header.Clone())
		s.mu.Unlock()
		rt, ok := routes[r.URL.Path]
		if !ok {
			rt = route{status: http.StatusNotFound}
		}
		if rt.status == 0 {
			rt.status = http.StatusOK
		}
		w.WriteHeader(rt.status)
		_, _ = w.Write([]byte(rt.body))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *subServer) headers() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.seen...)
}

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-tool")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// targetFor resolves the movie target of a title and year.
func (p *Processor) targetFor(cat, title string, year int) (naming.Target, error) {
	c, _ := p.cfg.Category(cat)
	return naming.Resolve(c.Path, c.Layout, naming.Meta{Title: title, Year: &year})
}

// atomicCount is a goroutine-safe counter.
type atomicCount struct {
	mu sync.Mutex
	n  int
}

func (a *atomicCount) inc() { a.mu.Lock(); a.n++; a.mu.Unlock() }
func (a *atomicCount) get() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}
