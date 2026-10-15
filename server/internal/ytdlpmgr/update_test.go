package ytdlpmgr_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/api"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/ytdlpmgr"
)

func TestAutoUpdateCanBeEnabledAndDisabledWhileRunning(t *testing.T) {
	w := newWrapper(t)
	g := newRig(t, w.path, nil)
	var enabled atomic.Bool
	started := make(chan struct{})
	var once sync.Once
	m := newUpdater(t, g, w, func(o *ytdlpmgr.Options) {
		o.AutoUpdate = false
		o.CurrentAutoUpdate = func() bool {
			value := enabled.Load()
			once.Do(func() { close(started) })
			return value
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	<-started
	enabled.Store(true)
	m.Wake()
	eventually(t, "live update", func() bool { return len(w.lines(t)) >= 2 })
	enabled.Store(false)
	m.Wake()
	cancel()
	<-done
	if got := w.lines(t); len(got) != 2 {
		t.Fatalf("disabled manager ran another update: %v", got)
	}
}

func newUpdater(t *testing.T, g *rig, w *wrapper, mod func(*ytdlpmgr.Options)) *ytdlpmgr.Manager {
	t.Helper()
	o := ytdlpmgr.Options{
		Binary: w.path, Runner: g.rn, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		AutoUpdate: true,
	}
	if mod != nil {
		mod(&o)
	}
	return ytdlpmgr.New(o)
}

func runAutoUpdate(ctx context.Context, m *ytdlpmgr.Manager) <-chan error {
	done := make(chan error, 1)
	go func() { done <- m.AutoUpdate(ctx) }()
	return done
}

func TestAutoUpdateDrainsDownloadsBeforeUpdating(t *testing.T) {
	t.Setenv("FAKE_YTDLP_DURATION", "2")
	w := newWrapper(t)
	g := newRig(t, w.path, nil)
	m := newUpdater(t, g, w, nil)

	first := g.create()
	g.waitStatus(first.ID, jobs.StatusDownloading)

	done := runAutoUpdate(t.Context(), m)
	eventually(t, "dispatcher hold", g.rn.Held)
	second := g.create() // would start at once if the dispatcher were not held

	time.Sleep(500 * time.Millisecond)
	if got := w.lines(t); len(got) != 0 {
		t.Fatalf("update ran while a job was downloading: %v", got)
	}
	if s := g.status(second.ID); s != jobs.StatusPending {
		t.Fatalf("second job is %s while the dispatcher is held, want pending", s)
	}
	if g.rn.Downloading() != 1 {
		t.Fatalf("Downloading = %d, want 1", g.rn.Downloading())
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AutoUpdate: %v", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("AutoUpdate did not finish")
	}
	// The update ran after the first download was over (it left downloading).
	if got := w.lines(t); !reflect.DeepEqual(got, []string{"start", "done"}) {
		t.Fatalf("update log = %v", got)
	}
	if s := g.status(first.ID); s != jobs.StatusProcessing {
		t.Errorf("first job is %s after the update, want processing (download finished)", s)
	}
	if g.rn.Held() {
		t.Error("dispatcher still held after the update")
	}
	// Dispatching resumed.
	g.waitStatus(second.ID, jobs.StatusDownloading)
}

func TestAutoUpdateDoesNotWaitForProcessingJobs(t *testing.T) {
	t.Setenv("FAKE_YTDLP_DURATION", "0")
	w := newWrapper(t)
	proc := newBlockProc()
	g := newRig(t, w.path, proc)
	m := newUpdater(t, g, w, nil)
	defer close(proc.release)

	j := g.create()
	select {
	case <-proc.entered:
	case <-time.After(waitTimeout):
		t.Fatal("job never reached processing")
	}
	g.waitStatus(j.ID, jobs.StatusProcessing)

	select {
	case err := <-runAutoUpdate(t.Context(), m):
		if err != nil {
			t.Fatalf("AutoUpdate: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AutoUpdate blocked on a processing job")
	}
	if got := w.lines(t); !reflect.DeepEqual(got, []string{"start", "done"}) {
		t.Fatalf("update log = %v", got)
	}
}

func TestAutoUpdateFailureReleasesHold(t *testing.T) {
	t.Setenv("FAKE_YTDLP_DURATION", "0")
	w := newWrapper(t)
	w.touch(t, w.fail)
	g := newRig(t, w.path, nil)
	m := newUpdater(t, g, w, nil)

	err := m.AutoUpdate(t.Context())
	if err == nil || !strings.Contains(err.Error(), "simulated update failure") {
		t.Fatalf("AutoUpdate error = %v, want the yt-dlp failure", err)
	}
	if g.rn.Held() {
		t.Fatal("dispatcher still held after a failed update")
	}
	// Dispatching resumed: a job runs to completion of its download.
	j := g.create()
	g.waitStatus(j.ID, jobs.StatusProcessing)
}

func TestAutoUpdateCancelledWhileWaitingReleasesHold(t *testing.T) {
	t.Setenv("FAKE_YTDLP_DURATION", "3")
	w := newWrapper(t)
	g := newRig(t, w.path, nil)
	m := newUpdater(t, g, w, nil)

	j := g.create()
	g.waitStatus(j.ID, jobs.StatusDownloading)

	ctx, cancel := context.WithCancel(t.Context())
	done := runAutoUpdate(ctx, m)
	eventually(t, "dispatcher hold", g.rn.Held)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("AutoUpdate = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AutoUpdate ignored the cancellation")
	}
	if g.rn.Held() {
		t.Fatal("dispatcher still held after cancellation")
	}
	if got := w.lines(t); len(got) != 0 {
		t.Errorf("update ran: %v", got)
	}
}

func TestManualUpdateIsBusyWhileDownloading(t *testing.T) {
	t.Setenv("FAKE_YTDLP_DURATION", "3")
	w := newWrapper(t)
	g := newRig(t, w.path, nil)
	m := newUpdater(t, g, w, nil)

	j := g.create()
	g.waitStatus(j.ID, jobs.StatusDownloading)

	start := time.Now()
	_, err := m.Update(t.Context())
	if !errors.Is(err, api.ErrYtdlpBusy) {
		t.Fatalf("Update = %v, want ErrYtdlpBusy", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("busy answer took %v; a manual update never waits", d)
	}
	if g.rn.Held() {
		t.Error("dispatcher held after a busy answer")
	}
	if got := w.lines(t); len(got) != 0 {
		t.Errorf("update ran: %v", got)
	}
}

func TestManualUpdateWhenIdle(t *testing.T) {
	w := newWrapper(t)
	g := newRig(t, w.path, nil)
	m := newUpdater(t, g, w, nil)

	res, err := m.Update(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if res != (api.UpdateResult{Previous: "2026.09.01", Current: "2026.09.30"}) {
		t.Errorf("result = %+v", res)
	}
	if g.rn.Held() {
		t.Error("dispatcher still held")
	}

	// A failing update reports an error and releases the hold too.
	w.touch(t, w.fail)
	if _, err := m.Update(t.Context()); err == nil || errors.Is(err, api.ErrYtdlpBusy) {
		t.Errorf("failing Update = %v", err)
	}
	if g.rn.Held() {
		t.Error("dispatcher still held after a failed update")
	}
}

func TestManualUpdateIsBusyDuringAnotherUpdate(t *testing.T) {
	t.Setenv("FAKE_YTDLP_DURATION", "0")
	w := newWrapper(t)
	w.touch(t, w.slow)
	g := newRig(t, w.path, nil)
	m := newUpdater(t, g, w, nil)

	done := runAutoUpdate(t.Context(), m)
	eventually(t, "update started", func() bool { return len(w.lines(t)) > 0 })
	if _, err := m.Update(t.Context()); !errors.Is(err, api.ErrYtdlpBusy) {
		t.Errorf("Update during an update = %v, want ErrYtdlpBusy", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := w.lines(t); !reflect.DeepEqual(got, []string{"start", "done"}) {
		t.Errorf("update log = %v, want exactly one update", got)
	}
}

func TestRunUpdatesAtStartupAndEveryInterval(t *testing.T) {
	t.Setenv("FAKE_YTDLP_DURATION", "0")
	w := newWrapper(t)
	g := newRig(t, w.path, nil)
	m := newUpdater(t, g, w, func(o *ytdlpmgr.Options) { o.Interval = 150 * time.Millisecond })

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	eventually(t, "three updates", func() bool { return len(w.lines(t)) >= 6 })
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
	if g.rn.Held() {
		t.Error("dispatcher held after Run stopped")
	}
}

func TestRunDoesNothingWithoutAutoUpdate(t *testing.T) {
	w := newWrapper(t)
	g := newRig(t, w.path, nil)
	m := newUpdater(t, g, w, func(o *ytdlpmgr.Options) { o.AutoUpdate = false })
	done := make(chan struct{})
	go func() { m.Run(t.Context()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run blocked although auto_update is off")
	}
	if got := w.lines(t); len(got) != 0 {
		t.Errorf("update ran: %v", got)
	}
}

func TestRunSkipsStartupUpdateRightAfterDownload(t *testing.T) {
	t.Setenv("FAKE_YTDLP_DURATION", "0")
	w := newWrapper(t)
	body, err := os.ReadFile(w.path)
	if err != nil {
		t.Fatal(err)
	}
	rel := newReleases(t, body)
	g := newRig(t, w.path, nil)
	bin := filepath.Join(t.TempDir(), "bin", "yt-dlp")
	m := ytdlpmgr.New(ytdlpmgr.Options{
		Binary: bin, Runner: g.rn, BaseURL: rel.srv.URL, AutoUpdate: true, Interval: time.Hour,
		Logger: slog.New(slog.DiscardHandler), GOOS: "linux", GOARCH: "amd64",
	})
	if err := m.Ensure(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	m.Run(ctx) // returns when the context expires
	if got := w.lines(t); len(got) != 0 {
		t.Errorf("a freshly downloaded binary was updated again: %v", got)
	}
}

// TestAPIContract serves the manager through the real API handler and
// compares the answers with the contract fixtures.
func TestAPIContract(t *testing.T) {
	t.Setenv("FAKE_YTDLP_DURATION", "3")
	w := newWrapper(t)
	g := newRig(t, w.path, nil)
	m := newUpdater(t, g, w, nil)
	g.cfg.Ytdlp.Binary = "" // managed
	h := api.New(api.Options{
		Config: g.cfg, Service: g.svc, Version: "1.0.0", Updater: m, Logger: slog.New(slog.DiscardHandler),
		YtdlpVersion: func(context.Context, string) (string, error) { return "2026.09.01", nil },
	})
	post := func() (int, any) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/system/ytdlp/update", nil))
		var v any
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}
		return rec.Code, v
	}
	fixture := func(name string) any {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "contract", name))
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	j := g.create()
	g.waitStatus(j.ID, jobs.StatusDownloading)
	start := time.Now()
	code, body := post()
	if code != http.StatusConflict || !reflect.DeepEqual(body, fixture("error-ytdlp_busy.json")) {
		t.Errorf("while downloading: %d %v", code, body)
	}
	if time.Since(start) > time.Second {
		t.Error("409 ytdlp_busy was not immediate")
	}

	if _, err := g.svc.Cancel(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "idle", func() bool { return g.rn.Downloading() == 0 })
	code, body = post()
	if code != http.StatusOK || !reflect.DeepEqual(body, fixture("ytdlp-update.json")) {
		t.Errorf("when idle: %d %v", code, body)
	}
}
