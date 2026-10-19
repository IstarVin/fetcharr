package runner_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func setDuration(t *testing.T, d string) { t.Setenv("FAKE_YTDLP_DURATION", d) }

func TestClaimsInQueuedAtOrder(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{concurrency: 1})
	release := g.r.Hold()
	base := time.Now().Add(-time.Hour)
	// Inserted in the opposite order of their queue position.
	c := g.insert(func(j *store.Job) { j.QueuedAt = base.Add(3 * time.Second) })
	a := g.insert(func(j *store.Job) { j.QueuedAt = base.Add(1 * time.Second) })
	b := g.insert(func(j *store.Job) { j.QueuedAt = base.Add(2 * time.Second) })
	release()
	for _, j := range []*store.Job{a, b, c} {
		g.waitStatus(j.ID, jobs.StatusProcessing)
	}
	got := g.rec.startOrder()
	want := []string{a.ID, b.ID, c.ID}
	if !slices.Equal(got, want) {
		t.Errorf("start order = %v, want %v", got, want)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	setDuration(t, "0.5")
	g := newRig(t, rigOpts{concurrency: 2})
	var all []*store.Job
	for range 5 {
		all = append(all, g.create("https://example.com/c"+store.NewID()))
	}
	for _, j := range all {
		g.waitStatus(j.ID, jobs.StatusProcessing)
	}
	if got := g.rec.maxDownloading(); got != 2 {
		t.Errorf("max simultaneous downloads = %d, want 2", got)
	}
}

func TestSafetyPollPicksUpUnwokenJob(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{mod: func(o *runner.Options) { o.PollInterval = 30 * time.Millisecond }})
	j := g.insert() // no Wake
	g.waitStatus(j.ID, jobs.StatusProcessing)
}

func TestDownloadToProcessing(t *testing.T) {
	setDuration(t, "0.4")
	var gotJob *store.Job
	var gotFile string
	proc := funcProcessor(func(_ context.Context, j *store.Job, file string) error {
		gotJob, gotFile = j, file
		return nil
	})
	g := newRig(t, rigOpts{mod: func(o *runner.Options) { o.Processor = proc }})
	j := g.create("https://example.com/ok")
	done := g.waitStatus(j.ID, jobs.StatusProcessing)

	if done.StartedAt == nil || done.FinishedAt != nil {
		t.Errorf("started_at=%v finished_at=%v", done.StartedAt, done.FinishedAt)
	}
	p := done.Progress
	if p == nil || p.Percent == nil || *p.Percent != 100 || *p.SpeedBps != 0 || *p.ETASeconds != 0 || *p.TotalBytes != 4<<20 {
		t.Errorf("progress at processing = %+v", p)
	}
	wantFile := filepath.Join(g.workDir(j.ID), "media.mp4")
	if gotFile != wantFile || !exists(wantFile) {
		t.Errorf("Process got file %q, want %q (exists=%v)", gotFile, wantFile, exists(wantFile))
	}
	if gotJob == nil || gotJob.ID != j.ID || gotJob.Status != jobs.StatusProcessing {
		t.Errorf("Process got job %+v", gotJob)
	}
	waitIdle(t, g.r)
	if g.r.Downloading() != 0 {
		t.Errorf("Downloading = %d, want 0", g.r.Downloading())
	}
}

func TestDefaultProcessorLeavesJobProcessing(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{})
	j := g.create("https://example.com/nop")
	g.waitStatus(j.ID, jobs.StatusProcessing)
	time.Sleep(100 * time.Millisecond)
	if got := g.get(j.ID); got.Status != jobs.StatusProcessing {
		t.Errorf("status = %s, want processing", got.Status)
	}
}

func TestDownloadFailure(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{})
	j := g.create("https://example.com/fail")
	got := g.waitStatus(j.ID, jobs.StatusFailed)
	if deref(got.ErrorCode) != runner.CodeDownloadFailed || !strings.Contains(deref(got.ErrorMessage), "simulated failure") {
		t.Errorf("error = %q / %q", deref(got.ErrorCode), deref(got.ErrorMessage))
	}
	if got.FinishedAt == nil || got.StartedAt == nil {
		t.Errorf("started_at=%v finished_at=%v", got.StartedAt, got.FinishedAt)
	}
	if !exists(g.workDir(j.ID)) {
		t.Error("work dir of a failed download must be kept")
	}
	// The slot is free again.
	ok := g.create("https://example.com/after-bad")
	g.waitStatus(ok.ID, jobs.StatusProcessing)
}

func TestMissingBinaryFailsJob(t *testing.T) {
	g := newRig(t, rigOpts{mod: func(o *runner.Options) { o.Binary = "/nonexistent/yt-dlp" }})
	j := g.create("https://example.com/x")
	got := g.waitStatus(j.ID, jobs.StatusFailed)
	if deref(got.ErrorCode) != runner.CodeDownloadFailed || deref(got.ErrorMessage) == "" {
		t.Errorf("error = %q / %q", deref(got.ErrorCode), deref(got.ErrorMessage))
	}
}

func TestPrecheckLowDiskSpace(t *testing.T) {
	setDuration(t, "0")
	var free uint64 = 1000
	var ferr error
	g := newRig(t, rigOpts{
		cfg: func(c *config.Config) { c.Downloads.MinFreeSpace = 5000 },
		mod: func(o *runner.Options) {
			o.FreeBytes = func(string) (uint64, error) { return free, ferr }
		},
	})
	j := g.create("https://example.com/low")
	got := g.waitStatus(j.ID, jobs.StatusFailed)
	if deref(got.ErrorCode) != runner.CodeLowDiskSpace || got.FinishedAt == nil {
		t.Errorf("error = %q finished=%v", deref(got.ErrorCode), got.FinishedAt)
	}
	if exists(g.workDir(j.ID)) {
		t.Error("a pre-check failure must not create the work dir")
	}

	// Enough space, or an unknown amount, lets the job run.
	free = 5000
	g.waitStatus(g.create("https://example.com/enough").ID, jobs.StatusProcessing)
	free, ferr = 0, errors.New("statfs failed")
	g.waitStatus(g.create("https://example.com/unknown").ID, jobs.StatusProcessing)
}

func TestPrecheckAlreadyExists(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{})
	dir := filepath.Join(g.media, "misc")
	write := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Taken.MKV")     // extension case does not matter
	write("Subs only.srt") // not a video
	write("Partial.mp4.partial")

	taken := g.insert(func(j *store.Job) { j.Title = store.Ptr("Taken") })
	g.r.Wake()
	g.waitFor(taken.ID, "failed", func(j *store.Job) bool { return j.Status == jobs.StatusFailed })
	if got := g.get(taken.ID); deref(got.ErrorCode) != runner.CodeAlreadyExists {
		t.Errorf("error code = %q", deref(got.ErrorCode))
	}

	for _, title := range []string{"Subs only", "Partial", "Free"} {
		j := g.insert(func(j *store.Job) { j.Title = store.Ptr(title) })
		g.r.Wake()
		g.waitStatus(j.ID, jobs.StatusProcessing)
	}

	// overwrite skips the check
	ow := g.insert(func(j *store.Job) { j.Title = store.Ptr("Taken"); j.Overwrite = true })
	g.r.Wake()
	g.waitStatus(ow.ID, jobs.StatusProcessing)
}

func TestPrecheckAlreadyExistsMovieLayout(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{})
	dir := filepath.Join(g.media, "movies", "Dune (2021)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dune (2021).mp4"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := g.insert(func(j *store.Job) {
		j.Category, j.Title, j.Year = "movies", store.Ptr("Dune"), store.Ptr(2021)
	})
	g.r.Wake()
	got := g.waitStatus(j.ID, jobs.StatusFailed)
	if deref(got.ErrorCode) != runner.CodeAlreadyExists {
		t.Errorf("error code = %q", deref(got.ErrorCode))
	}
}

func TestHoldAndIdle(t *testing.T) {
	setDuration(t, "1")
	proc := newBlockingProcessor()
	g := newRig(t, rigOpts{concurrency: 1, mod: func(o *runner.Options) { o.Processor = proc }})

	release := g.r.Hold()
	if !g.r.Held() {
		t.Fatal("Held = false after Hold")
	}
	first := g.create("https://example.com/h1")
	time.Sleep(300 * time.Millisecond)
	if got := g.get(first.ID); got.Status != jobs.StatusPending {
		t.Fatalf("job started while held: %s", got.Status)
	}
	select {
	case <-g.r.Idle():
	default:
		t.Fatal("not idle with nothing downloading")
	}
	release()
	release() // idempotent
	if g.r.Held() {
		t.Fatal("still held after release")
	}
	g.waitStatus(first.ID, jobs.StatusDownloading)

	// A running download blocks the idle signal; a second hold queues the
	// next job meanwhile.
	h := g.r.Hold()
	second := g.create("https://example.com/h2")
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := g.r.WaitIdle(ctx); err == nil {
		t.Fatal("WaitIdle returned while a job was downloading")
	}
	idle := g.r.Idle()
	waitIdle(t, g.r)
	select {
	case <-idle:
	default:
		t.Fatal("Idle channel not closed after WaitIdle returned")
	}
	// The first job is now processing (blocked in the Processor): it does not
	// count as downloading, and the held dispatcher starts nothing else.
	if got := g.get(first.ID); got.Status != jobs.StatusProcessing {
		t.Fatalf("first status = %s, want processing", got.Status)
	}
	time.Sleep(100 * time.Millisecond)
	if got := g.get(second.ID); got.Status != jobs.StatusPending {
		t.Errorf("second job status = %s while held", got.Status)
	}
	h()
	close(proc.release) // lets both processors finish
	g.waitStatus(second.ID, jobs.StatusProcessing)
	if g.r.Downloading() > 1 {
		t.Errorf("Downloading = %d", g.r.Downloading())
	}
}

func TestHoldsNest(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{})
	r1, r2 := g.r.Hold(), g.r.Hold()
	j := g.create("https://example.com/nest")
	r1()
	time.Sleep(150 * time.Millisecond)
	if got := g.get(j.ID); got.Status != jobs.StatusPending {
		t.Fatalf("started with one hold left: %s", got.Status)
	}
	r2()
	g.waitStatus(j.ID, jobs.StatusProcessing)
}

func TestPauseKeepsWorkDirAndResumeContinues(t *testing.T) {
	setDuration(t, "3")
	g := newRig(t, rigOpts{})
	j := g.create("https://example.com/pause")
	before := g.waitBytes(j.ID, 1<<20)

	if _, err := g.svc.Pause(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, g.r) // process group is dead
	part := filepath.Join(g.workDir(j.ID), "media.mp4.part")
	st, err := os.Stat(part)
	if err != nil {
		t.Fatalf("partial file lost on pause: %v", err)
	}
	if st.Size() < *before.Progress.DownloadedBytes {
		t.Errorf(".part size %d < persisted progress %d", st.Size(), *before.Progress.DownloadedBytes)
	}
	time.Sleep(200 * time.Millisecond)
	paused := g.get(j.ID)
	if paused.Status != jobs.StatusPaused || paused.FinishedAt != nil || paused.ErrorCode != nil {
		t.Fatalf("after pause: %+v", paused)
	}

	if _, err := g.svc.Resume(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	g.waitStatus(j.ID, jobs.StatusProcessing)
	for _, e := range g.rec.snapshot() {
		if e.Status == jobs.StatusFailed {
			t.Fatal("job failed")
		}
	}
}

func TestPauseThenImmediateResume(t *testing.T) {
	setDuration(t, "1")
	g := newRig(t, rigOpts{concurrency: 2})
	j := g.create("https://example.com/flap")
	g.waitBytes(j.ID, 1)
	for range 3 {
		if _, err := g.svc.Pause(context.Background(), j.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := g.svc.Resume(context.Background(), j.ID); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	g.waitStatus(j.ID, jobs.StatusProcessing)
	if g.rec.maxDownloading() > 1 {
		t.Errorf("job downloaded twice at once: max=%d", g.rec.maxDownloading())
	}
}

func TestCancelRetryWhileOldWorkerIsInPrecheck(t *testing.T) {
	for _, scenario := range []string{"claim", "precheck-failure", "replace-claim", "replace-precheck-failure"} {
		lowDisk := strings.Contains(scenario, "failure")
		name := scenario
		t.Run(name, func(t *testing.T) {
			setDuration(t, "0")
			entered, unblock := make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(unblock) })
			defer release()
			var once sync.Once
			g := newRig(t, rigOpts{
				cfg: func(c *config.Config) { c.Downloads.MinFreeSpace = 1 },
				mod: func(o *runner.Options) {
					o.FreeBytes = func(string) (uint64, error) {
						first := false
						once.Do(func() {
							first = true
							close(entered)
							<-unblock
						})
						if first && lowDisk {
							return 0, nil
						}
						return 1 << 40, nil
					}
				},
			})
			j := g.create("https://example.com/precheck-retry")
			select {
			case <-entered:
			case <-time.After(waitTimeout):
				t.Fatal("worker did not enter precheck")
			}
			if _, err := g.svc.Cancel(t.Context(), j.ID); err != nil {
				t.Fatal(err)
			}
			opts := service.RetryOptions{}
			if strings.HasPrefix(scenario, "replace") {
				opts.Source = &service.ReplacementSource{URL: "https://fresh.example/video", HeadersMode: "clear", Subtitles: []service.SubtitleInput{}}
			}
			if _, err := g.svc.Retry(t.Context(), j.ID, opts); err != nil {
				t.Fatal(err)
			}
			release()
			done := g.waitStatus(j.ID, jobs.StatusProcessing)
			if opts.Source != nil && done.URL != opts.Source.URL {
				t.Fatal("stale worker used old URL")
			}
			if !exists(filepath.Join(g.workDir(j.ID), "media.mp4")) {
				t.Error("the retried execution lost its downloaded file")
			}
		})
	}
}

func TestCancelDownloadingDeletesWorkDir(t *testing.T) {
	setDuration(t, "3")
	g := newRig(t, rigOpts{})
	j := g.create("https://example.com/cancel")
	g.waitBytes(j.ID, 1)
	if _, err := g.svc.Cancel(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, g.r)
	waitGone(t, g.workDir(j.ID))
	time.Sleep(150 * time.Millisecond)
	got := g.get(j.ID)
	if got.Status != jobs.StatusCancelled || got.ErrorCode != nil {
		t.Errorf("after cancel: status=%s code=%v", got.Status, got.ErrorCode)
	}
	// Retry starts from scratch.
	if _, err := g.svc.Retry(context.Background(), j.ID, service.RetryOptions{}); err != nil {
		t.Fatal(err)
	}
	g.waitBytes(j.ID, 1)
}

func TestCancelPendingAndPausedDeletesWorkDir(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{})
	release := g.r.Hold()
	defer release()

	seed := func(id string) string {
		dir := g.workDir(id)
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "media.mp4.part"), []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	pending := g.create("https://example.com/cp")
	pdir := seed(pending.ID)
	if _, err := g.svc.Cancel(context.Background(), pending.ID); err != nil {
		t.Fatal(err)
	}
	waitGone(t, pdir)

	paused := g.create("https://example.com/cpaused")
	if _, err := g.svc.Pause(context.Background(), paused.ID); err != nil {
		t.Fatal(err)
	}
	ddir := seed(paused.ID)
	if _, err := g.svc.Cancel(context.Background(), paused.ID); err != nil {
		t.Fatal(err)
	}
	waitGone(t, ddir)
}

func TestCancelProcessingStopsProcessorAndDeletesWorkDir(t *testing.T) {
	setDuration(t, "0")
	proc := newBlockingProcessor()
	g := newRig(t, rigOpts{mod: func(o *runner.Options) { o.Processor = proc }})
	j := g.create("https://example.com/cproc")
	<-proc.started
	if _, err := g.svc.Cancel(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-proc.stopped:
	case <-time.After(waitTimeout):
		t.Fatal("Processor context was not cancelled")
	}
	waitGone(t, g.workDir(j.ID))
	if got := g.get(j.ID); got.Status != jobs.StatusCancelled {
		t.Errorf("status = %s, want cancelled", got.Status)
	}
}

func TestCancelProcessingWithoutWorkerDeletesWorkDir(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{}) // NopProcessor: job stays processing, no worker
	j := g.create("https://example.com/cnop")
	g.waitStatus(j.ID, jobs.StatusProcessing)
	waitIdle(t, g.r)
	if !exists(g.workDir(j.ID)) {
		t.Fatal("work dir missing")
	}
	if _, err := g.svc.Cancel(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	waitGone(t, g.workDir(j.ID))
}

func TestStopIgnoresUnsafeIDs(t *testing.T) {
	g := newRig(t, rigOpts{})
	marker := filepath.Join(g.cfg.DataDir, "marker")
	if err := os.WriteFile(marker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "../x", "a/b"} {
		g.r.Stop(id, service.StopCancel)
	}
	g.r.Stop("unknown-but-valid", service.StopCancel)
	time.Sleep(100 * time.Millisecond)
	if !exists(marker) || !exists(g.cfg.WorkDir()) {
		t.Error("Stop deleted something outside a job work dir")
	}
}

func TestShutdownRequeuesAndKeepsWorkDir(t *testing.T) {
	setDuration(t, "3")
	g := newRig(t, rigOpts{concurrency: 2})
	a := g.create("https://example.com/s1")
	b := g.create("https://example.com/s2")
	waitBytesBoth := func(id string) { g.waitBytes(id, 1<<20) }
	waitBytesBoth(a.ID)
	waitBytesBoth(b.ID)

	start := time.Now()
	g.shutdown()
	if time.Since(start) > 5*time.Second {
		t.Errorf("Shutdown took %v", time.Since(start))
	}
	for _, j := range []*store.Job{a, b} {
		got := g.get(j.ID)
		if got.Status != jobs.StatusPending || got.FinishedAt != nil || got.Placing {
			t.Errorf("job %s after shutdown: %+v", j.ID, got)
		}
		if !exists(filepath.Join(g.workDir(j.ID), "media.mp4.part")) {
			t.Errorf("job %s lost its partial file", j.ID)
		}
	}
	// No new work after Shutdown, even on Wake.
	g.r.Wake()
	time.Sleep(100 * time.Millisecond)
	if got := g.get(a.ID); got.Status != jobs.StatusPending {
		t.Errorf("job started after Shutdown: %s", got.Status)
	}
	// Idempotent.
	g.shutdown()

	// A new runner resumes from the partial files.
	g.rec = &recorder{}
	g.start(nil)
	g.waitStatus(a.ID, jobs.StatusProcessing)
	first := true
	for _, e := range g.rec.snapshot() {
		if e.ID == a.ID && e.Status == jobs.StatusDownloading && e.Downloaded != nil && first {
			first = false
			if *e.Downloaded < 1<<20 {
				t.Errorf("first progress after restart = %d, want a continuation", *e.Downloaded)
			}
		}
	}
}

func TestShutdownRequeuesProcessingJob(t *testing.T) {
	setDuration(t, "0")
	proc := funcProcessor(func(ctx context.Context, _ *store.Job, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	})
	g := newRig(t, rigOpts{mod: func(o *runner.Options) { o.Processor = proc }})
	j := g.create("https://example.com/sp")
	g.waitStatus(j.ID, jobs.StatusProcessing)
	time.Sleep(50 * time.Millisecond)
	g.shutdown()
	got := g.get(j.ID)
	if got.Status != jobs.StatusPending {
		t.Errorf("status = %s, want pending", got.Status)
	}
	if !exists(filepath.Join(g.workDir(j.ID), "media.mp4")) {
		t.Error("downloaded file lost")
	}
}

func TestShutdownNeverStarted(t *testing.T) {
	g := newRig(t, rigOpts{})
	other := runnerNeverStarted(g)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := other.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	other.Start() // no effect after Shutdown
}

func runnerNeverStarted(g *rig) *runner.Runner {
	return runner.New(runner.Options{Config: g.cfg, Store: g.st})
}

func TestStartupRecoveryResumesFromPartialFile(t *testing.T) {
	setDuration(t, "0.4")
	g := newRig(t, rigOpts{mod: func(o *runner.Options) { o.PollInterval = time.Hour }})
	// A crashed server left a downloading job with a partial file.
	id := store.NewID()
	j := g.insert(func(j *store.Job) { j.ID, j.Status = id, jobs.StatusDownloading })
	if err := os.MkdirAll(g.workDir(id), 0o750); err != nil {
		t.Fatal(err)
	}
	const have = 2 << 20
	if err := os.WriteFile(filepath.Join(g.workDir(id), "media.mp4.part"), make([]byte, have), 0o600); err != nil {
		t.Fatal(err)
	}
	re, err := g.st.RequeueInterrupted(context.Background())
	if err != nil || len(re) != 1 {
		t.Fatalf("RequeueInterrupted: %v %v", re, err)
	}
	g.r.Wake()
	g.waitStatus(j.ID, jobs.StatusProcessing)
	for _, e := range g.rec.snapshot() {
		if e.Status == jobs.StatusDownloading && e.Downloaded != nil && *e.Downloaded <= have {
			t.Errorf("progress %d restarted below the partial file size", *e.Downloaded)
		}
	}
}

func TestPanicInProcessorFailsJobAndFreesSlot(t *testing.T) {
	setDuration(t, "0")
	calls := 0
	proc := funcProcessor(func(context.Context, *store.Job, string) error {
		calls++
		if calls == 1 {
			panic("boom")
		}
		return nil
	})
	g := newRig(t, rigOpts{concurrency: 1, mod: func(o *runner.Options) { o.Processor = proc }})
	a := g.create("https://example.com/p1")
	got := g.waitStatus(a.ID, jobs.StatusFailed)
	if deref(got.ErrorCode) != runner.CodeProcessingFailed || deref(got.ErrorMessage) != "internal error" || got.FinishedAt == nil {
		t.Errorf("after panic: %q %q", deref(got.ErrorCode), deref(got.ErrorMessage))
	}
	b := g.create("https://example.com/p2")
	g.waitStatus(b.ID, jobs.StatusProcessing)
	waitIdle(t, g.r)
}

func TestPanicInPrecheckFailsJobAndFreesSlot(t *testing.T) {
	setDuration(t, "0")
	calls := 0
	g := newRig(t, rigOpts{
		concurrency: 1,
		cfg:         func(c *config.Config) { c.Downloads.MinFreeSpace = 1 },
		mod: func(o *runner.Options) {
			o.FreeBytes = func(string) (uint64, error) {
				calls++
				if calls == 1 {
					panic("boom")
				}
				return 1 << 40, nil
			}
		},
	})
	a := g.create("https://example.com/pp1")
	got := g.waitStatus(a.ID, jobs.StatusFailed)
	if deref(got.ErrorCode) != runner.CodeProcessingFailed { // spec §9: panics are processing_failed
		t.Errorf("error code = %q", deref(got.ErrorCode))
	}
	b := g.create("https://example.com/pp2")
	g.waitStatus(b.ID, jobs.StatusProcessing)
}

func TestProcessorErrors(t *testing.T) {
	setDuration(t, "0")
	proc := funcProcessor(func(_ context.Context, j *store.Job, _ string) error {
		switch *j.Title {
		case "plain":
			return errors.New("ffmpeg exploded")
		default:
			return &runner.Failure{Code: runner.CodeAlreadyExists, Message: "it is there"}
		}
	})
	g := newRig(t, rigOpts{concurrency: 2, mod: func(o *runner.Options) { o.Processor = proc }})
	plain := g.insert(func(j *store.Job) { j.Title = store.Ptr("plain") })
	coded := g.insert(func(j *store.Job) { j.Title = store.Ptr("coded") })
	g.r.Wake()
	p := g.waitStatus(plain.ID, jobs.StatusFailed)
	if deref(p.ErrorCode) != runner.CodeProcessingFailed || deref(p.ErrorMessage) != "ffmpeg exploded" {
		t.Errorf("plain: %q %q", deref(p.ErrorCode), deref(p.ErrorMessage))
	}
	c := g.waitStatus(coded.ID, jobs.StatusFailed)
	if deref(c.ErrorCode) != runner.CodeAlreadyExists || deref(c.ErrorMessage) != "it is there" {
		t.Errorf("coded: %q %q", deref(c.ErrorCode), deref(c.ErrorMessage))
	}
}

func TestShutdownRequeuesProcessingJobWithoutWorker(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{}) // NopProcessor: the job stays processing, no worker
	j := g.create("https://example.com/nw")
	g.waitStatus(j.ID, jobs.StatusProcessing)
	waitIdle(t, g.r)
	time.Sleep(50 * time.Millisecond)
	g.shutdown()
	got := g.get(j.ID)
	if got.Status != jobs.StatusPending || got.Placing {
		t.Errorf("status = %s placing=%v, want pending", got.Status, got.Placing)
	}
	if !exists(filepath.Join(g.workDir(j.ID), "media.mp4")) {
		t.Error("work dir content lost")
	}
	var last event
	for _, e := range g.rec.snapshot() {
		if e.ID == j.ID {
			last = e
		}
	}
	if last.Status != jobs.StatusPending {
		t.Errorf("last published status = %s, want pending", last.Status)
	}
}

// The service commits pause/cancel before calling Stop, so a Stop that
// arrives after the job was resumed or restarted must not touch the new run.
func TestStaleStopIsIgnored(t *testing.T) {
	setDuration(t, "1.5")
	g := newRig(t, rigOpts{})
	j := g.create("https://example.com/stale")
	g.waitBytes(j.ID, 1)
	g.r.Stop(j.ID, service.StopPause)
	g.r.Stop(j.ID, service.StopCancel)
	g.waitStatus(j.ID, jobs.StatusProcessing)
	if !exists(filepath.Join(g.workDir(j.ID), "media.mp4")) {
		t.Error("a stale cancel deleted the work dir of a running job")
	}
	for _, e := range g.rec.snapshot() {
		if e.ID == j.ID && (e.Status == jobs.StatusFailed || e.Status == jobs.StatusPaused) {
			t.Errorf("unexpected status %s", e.Status)
		}
	}

	// No worker: a stale cancel of a pending job keeps its work dir.
	release := g.r.Hold()
	defer release()
	p := g.create("https://example.com/stale2")
	if err := os.MkdirAll(g.workDir(p.ID), 0o750); err != nil {
		t.Fatal(err)
	}
	g.r.Stop(p.ID, service.StopCancel)
	time.Sleep(150 * time.Millisecond)
	if !exists(g.workDir(p.ID)) {
		t.Error("a stale cancel deleted the work dir of a pending job")
	}
}

func TestRelativeConfigPathsWork(t *testing.T) {
	setDuration(t, "0.2")
	fake := fakeYtdlp(t)
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Symlink(fake, "yt-dlp-fake"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("media/misc", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("data", 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "data_dir: data\nytdlp:\n  binary: ./yt-dlp-fake\ncategories:\n  - {id: misc, path: media/misc, layout: flat}\n"
	if err := os.WriteFile("config.yaml", []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadWithOptions("config.yaml", config.Options{
		LookPath: func(s string) (string, error) { return s, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	r := runner.New(runner.Options{Config: cfg, Store: st, PollInterval: 20 * time.Millisecond})
	r.Start()
	defer func() { _ = r.Shutdown(context.Background()) }()
	j := &store.Job{Category: "misc", URL: "https://example.com/rel", Title: store.Ptr("Rel"), WorkDir: cfg.JobWorkDir("x")}
	if err := st.Insert(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	r.Wake()
	deadline := time.Now().Add(waitTimeout)
	for {
		got, err := st.Get(context.Background(), j.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == jobs.StatusProcessing {
			break
		}
		if got.Status == jobs.StatusFailed || time.Now().After(deadline) {
			t.Fatalf("status = %s, error = %q", got.Status, deref(got.ErrorMessage))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !exists(filepath.Join(cfg.JobWorkDir(j.ID), "media.mp4")) {
		t.Errorf("output is not in %s", cfg.JobWorkDir(j.ID))
	}
}

func TestLifecycleLogsAreInfoAndSecretFree(t *testing.T) {
	setDuration(t, "0")
	var buf bytes.Buffer
	g := newRig(t, rigOpts{mod: func(o *runner.Options) {
		o.Logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}})
	ok := g.create("https://example.com/logok")
	g.waitStatus(ok.ID, jobs.StatusProcessing)
	bad := g.create("https://example.com/fail?token=SECRET-QUERY")
	g.waitStatus(bad.ID, jobs.StatusFailed)
	g.shutdown()
	out := buf.String()
	for _, want := range []string{"job started", "job failed", "error_code=download_failed", "id=" + ok.ID, "id=" + bad.ID} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "SECRET-QUERY") || strings.Contains(out, "simulated failure") {
		t.Errorf("log leaks URL or message:\n%s", out)
	}
}
