package runner_test

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/runner"
)

func TestAutoRetryRecoversTransientFailure(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{
		cfg: func(c *config.Config) { c.Downloads.AutoRetries = 1 },
		mod: func(o *runner.Options) { o.AutoRetryDelay = time.Millisecond },
	})
	j := g.create("https://example.com/flaky")
	got := g.waitStatus(j.ID, jobs.StatusProcessing)
	if got.ErrorCode != nil {
		t.Fatalf("error_code = %q", *got.ErrorCode)
	}
}

func TestAutoRetryDisabled(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{mod: func(o *runner.Options) { o.AutoRetryDelay = time.Millisecond }})
	j := g.create("https://example.com/flaky")
	g.waitStatus(j.ID, jobs.StatusFailed)
}

func TestAutoRetryGivesUpAfterLimit(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{
		cfg: func(c *config.Config) { c.Downloads.AutoRetries = 2 },
		mod: func(o *runner.Options) { o.AutoRetryDelay = time.Millisecond },
	})
	j := g.create("https://example.com/fail")
	got := g.waitStatus(j.ID, jobs.StatusFailed)
	if deref(got.ErrorCode) != runner.CodeDownloadFailed {
		t.Fatalf("error_code = %q", deref(got.ErrorCode))
	}
}

func TestPauseDuringRetryWait(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{
		cfg: func(c *config.Config) { c.Downloads.AutoRetries = 1 },
		mod: func(o *runner.Options) { o.AutoRetryDelay = time.Hour },
	})
	j := g.create("https://example.com/flaky")
	g.waitStatus(j.ID, jobs.StatusDownloading)
	time.Sleep(200 * time.Millisecond) // first run fails; the worker waits
	if _, err := g.svc.Pause(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	g.waitStatus(j.ID, jobs.StatusPaused)
	if _, err := g.svc.Resume(t.Context(), j.ID); err != nil {
		t.Fatal(err)
	}
	g.waitStatus(j.ID, jobs.StatusProcessing) // the marker persists: the run succeeds
}

func TestDownloadWindowDefersNewJobs(t *testing.T) {
	setDuration(t, "0")
	var now atomic.Int64
	now.Store(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC).Unix())
	g := newRig(t, rigOpts{
		cfg: func(c *config.Config) { c.Downloads.WindowStart, c.Downloads.WindowEnd = "23:00", "06:00" },
		mod: func(o *runner.Options) { o.Now = func() time.Time { return time.Unix(now.Load(), 0).UTC() } },
	})
	j := g.create("https://example.com/later")
	time.Sleep(200 * time.Millisecond)
	if got := g.get(j.ID); got.Status != jobs.StatusPending {
		t.Fatalf("started outside the window: %s", got.Status)
	}
	now.Store(time.Date(2026, 10, 28, 23, 30, 0, 0, time.UTC).Unix())
	g.r.Wake()
	g.waitStatus(j.ID, jobs.StatusProcessing)
}
