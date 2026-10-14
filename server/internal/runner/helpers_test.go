package runner_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

const waitTimeout = 15 * time.Second

func fakeYtdlp(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake-ytdlp"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// event is one published job change.
type event struct {
	ID         string
	Status     jobs.Status
	Downloaded *int64
	Percent    *float64
}

// recorder is a service.Publisher that keeps every change.
type recorder struct {
	mu     sync.Mutex
	events []event
	live   map[string]jobs.Status
	maxDl  int
	calls  atomic.Int64
}

func (r *recorder) Publish(c service.Change) {
	r.calls.Add(1)
	if c.Job == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live == nil {
		r.live = map[string]jobs.Status{}
	}
	e := event{ID: c.Job.ID, Status: c.Job.Status}
	if p := c.Job.Progress; p != nil {
		e.Downloaded, e.Percent = p.DownloadedBytes, p.Percent
	}
	r.events = append(r.events, e)
	r.live[e.ID] = e.Status
	n := 0
	for _, s := range r.live {
		if s == jobs.StatusDownloading {
			n++
		}
	}
	r.maxDl = max(r.maxDl, n)
}

func (r *recorder) snapshot() []event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event(nil), r.events...)
}

// startOrder returns the ids in the order they were published as downloading
// for the first time.
func (r *recorder) startOrder() []string {
	var ids []string
	seen := map[string]bool{}
	for _, e := range r.snapshot() {
		if e.Status == jobs.StatusDownloading && !seen[e.ID] {
			seen[e.ID] = true
			ids = append(ids, e.ID)
		}
	}
	return ids
}

func (r *recorder) maxDownloading() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxDl
}

// rig is the store, service and runner under test.
type rig struct {
	t     *testing.T
	cfg   *config.Config
	st    *store.Store
	rec   *recorder
	svc   *service.Service
	r     *runner.Runner
	media string
	n     int
}

type rigOpts struct {
	concurrency int
	mod         func(*runner.Options)
	cfg         func(*config.Config)
}

func newRig(t *testing.T, ro rigOpts) *rig {
	t.Helper()
	bin := fakeYtdlp(t)
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.Ytdlp.Binary = bin
	cfg.Downloads.MinFreeSpace = 0
	cfg.Downloads.Concurrency = max(ro.concurrency, 1)
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
	if ro.cfg != nil {
		ro.cfg(cfg)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	g := &rig{t: t, cfg: cfg, st: st, rec: &recorder{}, media: media}
	g.start(ro.mod)
	return g
}

// start builds and starts a (new) runner and service over the same store.
func (g *rig) start(mod func(*runner.Options)) {
	g.t.Helper()
	o := runner.Options{
		Config:           g.cfg,
		Store:            g.st,
		Publisher:        g.rec,
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		ProgressInterval: 20 * time.Millisecond,
		PollInterval:     time.Hour,
		KillGrace:        2 * time.Second,
	}
	if mod != nil {
		mod(&o)
	}
	g.r = runner.New(o)
	g.svc = service.New(g.st, g.cfg, service.WithPublisher(g.rec), service.WithController(g.r))
	g.r.Start()
	g.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = g.r.Shutdown(ctx)
	})
}

func (g *rig) shutdown() {
	g.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	if err := g.r.Shutdown(ctx); err != nil {
		g.t.Fatalf("Shutdown: %v", err)
	}
}

// insert stores a pending flat job directly (the runner is not woken).
func (g *rig) insert(mods ...func(*store.Job)) *store.Job {
	g.t.Helper()
	g.n++
	j := &store.Job{
		Category: "misc",
		URL:      fmt.Sprintf("https://example.com/video-%d", g.n),
		Title:    store.Ptr("Title " + strconv.Itoa(g.n)),
		Status:   jobs.StatusPending,
	}
	j.WorkDir = g.cfg.JobWorkDir(store.NewID())
	for _, m := range mods {
		m(j)
	}
	if err := g.st.Insert(context.Background(), j); err != nil {
		g.t.Fatal(err)
	}
	return j
}

// create goes through the service, like the API does (publishes and wakes).
func (g *rig) create(url string) *store.Job {
	g.t.Helper()
	g.n++
	j, err := g.svc.Create(context.Background(), service.CreateRequest{
		Category: "misc", URL: url, Title: "Title " + strconv.Itoa(g.n),
	})
	if err != nil {
		g.t.Fatal(err)
	}
	return j
}

func (g *rig) get(id string) *store.Job {
	g.t.Helper()
	j, err := g.st.Get(context.Background(), id)
	if err != nil {
		g.t.Fatal(err)
	}
	return j
}

// waitStatus polls until the job has the status.
func (g *rig) waitStatus(id string, want jobs.Status) *store.Job {
	g.t.Helper()
	return g.waitFor(id, string(want), func(j *store.Job) bool { return j.Status == want })
}

func (g *rig) waitFor(id, what string, ok func(*store.Job) bool) *store.Job {
	g.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		j := g.get(id)
		if ok(j) {
			return j
		}
		if time.Now().After(deadline) {
			g.t.Fatalf("job %s: timeout waiting for %s; status=%s progress=%+v err=%v", id, what, j.Status, j.Progress, deref(j.ErrorMessage))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (g *rig) workDir(id string) string { return g.cfg.JobWorkDir(id) }

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// waitGone waits until the path no longer exists.
func waitGone(t *testing.T, p string) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for exists(p) {
		if time.Now().After(deadline) {
			t.Fatalf("%s still exists", p)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitIdle(t *testing.T, r *runner.Runner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	if err := r.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
}

// hasDownloaded waits until the job's persisted progress shows at least n bytes.
func (g *rig) waitBytes(id string, n int64) *store.Job {
	g.t.Helper()
	return g.waitFor(id, fmt.Sprintf("%d bytes", n), func(j *store.Job) bool {
		return j.Progress != nil && j.Progress.DownloadedBytes != nil && *j.Progress.DownloadedBytes >= n
	})
}

// blockingProcessor blocks in Process until released or ctx is done.
type blockingProcessor struct {
	started chan string
	release chan struct{}
	// ctxErr receives the cause of the context when Process was stopped.
	stopped chan error
}

func newBlockingProcessor() *blockingProcessor {
	return &blockingProcessor{started: make(chan string, 16), release: make(chan struct{}), stopped: make(chan error, 16)}
}

func (p *blockingProcessor) Process(ctx context.Context, j *store.Job, _ string) error {
	p.started <- j.ID
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		p.stopped <- context.Cause(ctx)
		return ctx.Err()
	}
}

type funcProcessor func(ctx context.Context, j *store.Job, file string) error

func (f funcProcessor) Process(ctx context.Context, j *store.Job, file string) error {
	return f(ctx, j, file)
}
