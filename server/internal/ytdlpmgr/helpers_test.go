package ytdlpmgr_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func fakeYtdlpPath(t *testing.T) string {
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

// wrapper is a yt-dlp stand-in around the fake: `-U` appends "start" and
// "done" to a log (and rewrites the version file), `--version` prints the
// version file, everything else is the fake downloader.
type wrapper struct {
	path, log, version, fail, slow string
}

func newWrapper(t *testing.T) *wrapper {
	t.Helper()
	dir := t.TempDir()
	w := &wrapper{
		path:    filepath.Join(dir, "yt-dlp"),
		log:     filepath.Join(dir, "update.log"),
		version: filepath.Join(dir, "version"),
		fail:    filepath.Join(dir, "fail"),
		slow:    filepath.Join(dir, "slow"),
	}
	if err := os.WriteFile(w.version, []byte("2026.09.01\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
--version) cat %[1]q; exit 0;;
-U)
  echo start >> %[2]q
  [ -f %[4]q ] && sleep 1
  if [ -f %[3]q ]; then echo "ERROR: simulated update failure" >&2; exit 1; fi
  echo 2026.09.30 > %[1]q
  echo done >> %[2]q
  exit 0;;
esac
exec %[5]q "$@"
`, w.version, w.log, w.fail, w.slow, fakeYtdlpPath(t))
	if err := os.WriteFile(w.path, []byte(script), 0o755); err != nil { //nolint:gosec // executable test script
		t.Fatal(err)
	}
	return w
}

func (w *wrapper) lines(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(w.log)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func (w *wrapper) touch(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// blockProc is a runner.Processor that keeps jobs in processing until
// release is closed.
type blockProc struct {
	entered chan string
	release chan struct{}
}

func newBlockProc() *blockProc {
	return &blockProc{entered: make(chan string, 16), release: make(chan struct{})}
}

func (p *blockProc) Process(ctx context.Context, j *store.Job, _ string) error {
	p.entered <- j.ID
	select {
	case <-p.release:
	case <-ctx.Done():
	}
	return nil
}

type rig struct {
	t   *testing.T
	cfg *config.Config
	st  *store.Store
	svc *service.Service
	rn  *runner.Runner
	n   int
}

func newRig(t *testing.T, binary string, proc runner.Processor) *rig {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = filepath.Join(dir, "data")
	cfg.Downloads.MinFreeSpace = 0
	cfg.Downloads.Concurrency = 2
	media := filepath.Join(dir, "media")
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.WorkDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Categories = []config.Category{{ID: "misc", Label: "misc", Path: media, Layout: naming.LayoutFlat}}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	rn := runner.New(runner.Options{
		Config: cfg, Store: st, Binary: binary, Processor: proc,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		PollInterval: time.Hour, ProgressInterval: 20 * time.Millisecond, KillGrace: 2 * time.Second,
	})
	svc := service.New(st, cfg, service.WithController(rn))
	rn.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = rn.Shutdown(ctx)
	})
	return &rig{t: t, cfg: cfg, st: st, svc: svc, rn: rn}
}

func (g *rig) create() *store.Job {
	g.t.Helper()
	g.n++
	j, err := g.svc.Create(context.Background(), service.CreateRequest{
		Category: "misc", URL: fmt.Sprintf("https://example.com/v%d", g.n), Title: fmt.Sprintf("T%d", g.n),
	})
	if err != nil {
		g.t.Fatal(err)
	}
	return j
}

func (g *rig) status(id string) jobs.Status {
	g.t.Helper()
	j, err := g.st.Get(context.Background(), id)
	if err != nil {
		g.t.Fatal(err)
	}
	return j.Status
}

func (g *rig) waitStatus(id string, want jobs.Status) {
	g.t.Helper()
	eventually(g.t, string(want), func() bool { return g.status(id) == want })
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
