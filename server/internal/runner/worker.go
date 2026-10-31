package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/direct"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/logsafe"
	"github.com/istarvin/fetcharr/server/internal/resolve"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
	"github.com/istarvin/fetcharr/server/internal/ytdlp"
)

// run is the state of one worker.
type run struct {
	r      *Runner
	ctx    context.Context
	e      *entry
	job    *store.Job
	status jobs.Status // last status this worker set; pending until the claim
}

// work is the worker goroutine of one job.
func (r *Runner) work(ctx context.Context, e *entry, j *store.Job) {
	w := &run{r: r, ctx: ctx, e: e, job: j, status: jobs.StatusPending}
	defer r.release(j.ID, e) // runs last: process group dead, panic handled
	defer func() {
		if p := recover(); p != nil {
			w.onPanic(p)
		}
	}()
	current, err := r.st.Get(ctx, j.ID)
	if err != nil || current.Status != jobs.StatusPending {
		return
	}
	w.job = current
	w.execute()
}

func (w *run) execute() {
	r, j := w.r, w.job
	cfg := r.cfg.Current()
	cat, ok := cfg.Category(j.Category)
	if !ok {
		w.fail(CodeDownloadFailed, fmt.Sprintf("category '%s' no longer exists", j.Category))
		return
	}
	if w.ctx.Err() != nil {
		return // cancelled or shut down before the claim
	}
	if code, msg := r.precheckWithConfig(j, cat, cfg); code != "" {
		w.fail(code, msg)
		return
	}

	// Claim: pending -> downloading, atomically, so no job starts twice.
	now := r.now()
	row, ok := w.move(jobs.StatusPending, jobs.EventStart, store.Update{StartedAt: store.Set(&now)})
	if !ok {
		return // paused or cancelled while queued
	}
	w.status, w.job = jobs.StatusDownloading, row
	j = row
	r.log.Info("job started", "job", row)

	workDir := r.workDir(j.ID)
	if j.ResetWorkDir {
		if err := os.RemoveAll(workDir); err != nil {
			w.fail(CodeDownloadFailed, "cannot reset the work directory")
			return
		}
		updated, err := r.st.UpdateIf(w.ctx, j.ID, []jobs.Status{jobs.StatusDownloading}, store.Update{ResetWorkDir: store.Set(false)})
		if err != nil {
			if w.ctx.Err() != nil {
				w.stopped()
			} else if !errors.Is(err, store.ErrStatusMismatch) && !errors.Is(err, store.ErrNotFound) {
				w.fail(CodeDownloadFailed, "cannot record work directory reset")
			}
			return
		}
		w.job, j = updated, updated
	}
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		w.fail(CodeDownloadFailed, "cannot create the work directory: "+err.Error())
		return
	}

	pt := &progressTracker{w: w}
	fetch := w.ytdlpFetch(cfg, workDir, pt)
	if resolve.Matches(j.URL) {
		fetch = w.directFetch(cfg, workDir, pt)
	}
	file, err := fetch()
	// A failed run is retried after a growing pause; partial files are kept,
	// so the next run resumes. Only download failures are retried, not a
	// missing binary or a stop.
	for attempt := 1; attempt <= cfg.Downloads.AutoRetries && retryable(err) && w.ctx.Err() == nil; attempt++ {
		r.log.Info("download failed; retrying automatically", "job", j, "attempt", attempt, "of", cfg.Downloads.AutoRetries)
		select {
		case <-w.ctx.Done():
		case <-time.After(r.retryDelay * time.Duration(attempt)):
			file, err = fetch()
		}
	}
	if w.ctx.Err() != nil && err != nil {
		w.stopped()
		return
	}
	if err != nil {
		w.downloadError(err)
		return
	}
	if w.ctx.Err() != nil {
		w.stopped() // finished just as a stop arrived
		return
	}
	if file == "" {
		w.fail(CodeDownloadFailed, "yt-dlp finished but produced no media file")
		return
	}

	row, ok = w.move(jobs.StatusDownloading, jobs.EventDownloadDone, store.Update{
		Progress: store.Set(pt.final(file)),
	})
	if !ok {
		return // paused or cancelled at the last moment
	}
	w.status, w.job = jobs.StatusProcessing, row
	r.log.Info("job downloaded", "job", row)
	// The "job completed" info log (spec §9) is written by the Processor,
	// which owns the processing -> completed transition.
	r.leaveDownloading(w.e) // processing jobs do not block the hold's idle signal

	if err := r.proc.Process(w.ctx, row, file); err != nil {
		w.processError(err)
	}
}

func (w *run) downloadError(err error) {
	if errors.Is(err, ytdlp.ErrStopped) {
		w.stopped()
		return
	}
	msg := err.Error()
	var ye *ytdlp.Error
	var re *resolve.Error
	var de *direct.Error
	switch {
	case errors.As(err, &ye):
		msg = ye.Message
	case errors.As(err, &re):
		msg = re.Message
	case errors.As(err, &de):
		msg = de.Message
	}
	w.fail(CodeDownloadFailed, msg)
}

// retryable reports whether a failed download attempt is retried
// automatically.
// A yt-dlp failure is retried unless its message says the source itself is
// unusable; unknown messages stay retryable.
func retryable(err error) bool {
	var ye *ytdlp.Error
	if errors.As(err, &ye) {
		return !permanentYtdlp(ye.Message)
	}
	return resolve.IsRetryable(err) || direct.IsRetryable(err)
}

// permanentMarkers are lower-case fragments of yt-dlp error messages that a
// later attempt cannot fix: unsupported or removed sources, private or
// geo-blocked videos, HTTP 404/410. 403 stays retryable: CDNs use it for
// rate limits and short-lived tokens.
var permanentMarkers = []string{
	"unsupported url",
	"video unavailable",
	"this video is unavailable",
	"this video is private",
	"private video",
	"this video has been removed",
	"video has been removed",
	"video is no longer available",
	"in your country",
	"geo restricted",
	"geo-restricted",
	"http error 404",
	"http error 410",
}

func permanentYtdlp(msg string) bool {
	msg = strings.ToLower(msg)
	for _, m := range permanentMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// ytdlpFetch returns one yt-dlp download attempt of the job, giving the
// downloaded file ("" when yt-dlp produced none).
func (w *run) ytdlpFetch(cfg *config.Config, workDir string, pt *progressTracker) func() (string, error) {
	j := w.job
	headers := ytdlp.WithPixeldrainAuth(j.URL, j.Headers, cfg.Downloads.PixeldrainAPIKey)
	args := ytdlp.BuildArgs(ytdlp.Spec{
		URL:         j.URL,
		Headers:     headers,
		WorkDir:     workDir,
		MergeFormat: cfg.Downloads.MergeFormat,
		RateLimit:   int64(cfg.Downloads.RateLimit),
		ExtraArgs:   cfg.Downloads.ExtraArgs,
	})
	opts := ytdlp.Options{
		Dir:        workDir,
		Secrets:    append(ytdlp.HeaderValues(headers), cfg.Downloads.PixeldrainAPIKey),
		KillGrace:  w.r.killGrace,
		OnProgress: pt.on,
	}
	return func() (string, error) {
		res, err := ytdlp.Run(w.ctx, w.r.binary, args, opts)
		if err != nil {
			return "", err
		}
		if res.FilePath != "" {
			return res.FilePath, nil
		}
		return ytdlp.FallbackFile(workDir), nil
	}
}

// directFetch returns one attempt for a resolver link: resolve the link
// (its file URLs are signed and expire, so every attempt resolves again),
// then download from the first mirror that works.
func (w *run) directFetch(cfg *config.Config, workDir string, pt *progressTracker) func() (string, error) {
	j := w.job
	return func() (string, error) {
		sources, err := w.r.resolver.Resolve(w.ctx, j.URL)
		if err != nil {
			return "", err
		}
		var last error
		for _, s := range sources {
			file, err := direct.Download(w.ctx, w.r.http, direct.Spec{
				URL:        s.URL,
				Headers:    ytdlp.WithPixeldrainAuth(s.URL, nil, cfg.Downloads.PixeldrainAPIKey),
				Dir:        workDir,
				Resumable:  s.Resumable,
				RateLimit:  int64(cfg.Downloads.RateLimit),
				OnProgress: pt.on,
			})
			if err == nil {
				return file, nil
			}
			if w.ctx.Err() != nil || !direct.IsRetryable(err) {
				return "", err
			}
			w.r.log.Info("mirror failed; trying the next one", "job", j, "server", s.Server, "err", err)
			last = err
		}
		return "", last
	}
}

func (w *run) processError(err error) {
	if w.ctx.Err() != nil {
		w.stopped()
		return
	}
	code, msg := CodeProcessingFailed, err.Error()
	var f *Failure
	if errors.As(err, &f) {
		if f.Code != "" {
			code = f.Code
		}
		msg = f.Message
	}
	w.fail(code, msg)
}

// stopped handles the end of a run caused by Stop or Shutdown. A pause or
// cancel was already committed by the service: nothing to write (the work
// directory is handled by release). A shutdown puts the job back to pending.
func (w *run) stopped() {
	if !errors.Is(context.Cause(w.ctx), errShutdown) {
		return
	}
	if w.status != jobs.StatusDownloading && w.status != jobs.StatusProcessing {
		return // still pending: nothing was claimed
	}
	w.r.move(w.job.ID, w.status, jobs.EventShutdown, store.Update{Placing: store.Set(false)})
}

// fail moves the job from the status the worker last set to failed. A lost
// race (the user paused or cancelled) is expected and silent.
func (w *run) fail(code, msg string) {
	finished := w.r.now()
	if _, ok := w.move(w.status, jobs.EventFail, store.Update{
		ErrorCode:    store.Set(&code),
		ErrorMessage: store.Set(&msg),
		FinishedAt:   store.Set(&finished),
	}); ok {
		// Only the code is logged, never the message.
		w.r.log.Info("job failed", "job", w.job, "error_code", code)
	}
}

// move excludes Stop while checking retirement and committing a worker
// transition. In particular, a worker delayed in precheck must not claim or
// fail the pending status belonging to a later resume/retry.
func (w *run) move(from jobs.Status, ev jobs.Event, u store.Update) (*store.Job, bool) {
	row, ok := func() (*store.Job, bool) {
		w.r.mu.Lock()
		defer w.r.mu.Unlock()
		if w.e.stopped {
			return nil, false
		}
		return w.r.moveStored(w.job.ID, from, ev, u)
	}()
	if ok {
		w.r.pub.Publish(service.Change{Kind: service.ChangeUpdated, Job: row})
	}
	return row, ok
}

func (w *run) onPanic(p any) {
	w.r.log.Error("worker panicked", "job", w.job, "panic", logsafe.Text(fmt.Sprint(p)), "stack", string(debug.Stack()))
	w.e.cancel(errPanic)
	// Spec §9: a panicked worker's job fails with processing_failed, in
	// whatever phase the panic happened.
	func() {
		defer func() { _ = recover() }()
		w.fail(CodeProcessingFailed, "internal error")
	}()
}

// move applies ev to the job with a compare-and-set on status from, using
// the target status of the state machine, and publishes the new row. ok is
// false when the job was not in status from any more (a concurrent pause,
// cancel or delete won), which is expected and not logged as an error.
func (r *Runner) move(id string, from jobs.Status, ev jobs.Event, u store.Update) (*store.Job, bool) {
	row, ok := r.moveStored(id, from, ev, u)
	if ok {
		r.pub.Publish(service.Change{Kind: service.ChangeUpdated, Job: row})
	}
	return row, ok
}

func (r *Runner) moveStored(id string, from jobs.Status, ev jobs.Event, u store.Update) (*store.Job, bool) {
	to, err := jobs.Transition(from, ev, false)
	if err != nil {
		r.log.Error("invalid runner transition", "job", id, "err", err)
		return nil, false
	}
	u.Status = store.Set(to)
	row, err := r.st.UpdateIf(context.Background(), id, []jobs.Status{from}, u)
	switch {
	case err == nil:
		return row, true
	case errors.Is(err, store.ErrStatusMismatch), errors.Is(err, store.ErrNotFound):
		r.log.Debug("job changed concurrently; runner update skipped", "job", id, "event", string(ev))
	default:
		r.log.Error("update job", "job", id, "event", string(ev), "err", err)
	}
	return nil, false
}

// progressTracker persists yt-dlp progress, at most once per
// Runner.progressEvery, and remembers the last value.
type progressTracker struct {
	w     *run
	last  store.Progress
	have  bool
	wrote time.Time
}

// on is the ytdlp.Options.OnProgress callback. It runs in a goroutine of the
// exec package, so a panic would crash the process: it is contained here.
func (t *progressTracker) on(p ytdlp.Progress) {
	defer func() {
		if rec := recover(); rec != nil {
			t.w.r.log.Error("progress handler panicked", "panic", logsafe.Text(fmt.Sprint(rec)))
		}
	}()
	sp := store.Progress(p)
	t.last, t.have = sp, true
	now := time.Now()
	if !t.wrote.IsZero() && now.Sub(t.wrote) < t.w.r.progressEvery {
		return
	}
	t.wrote = now
	r := t.w.r
	row, err := r.st.UpdateIf(context.Background(), t.w.job.ID, []jobs.Status{jobs.StatusDownloading},
		store.Update{Progress: store.Set(&sp)})
	switch {
	case err == nil:
		r.pub.Publish(service.Change{Kind: service.ChangeUpdated, Job: row})
	case errors.Is(err, store.ErrStatusMismatch), errors.Is(err, store.ErrNotFound):
		// Paused or cancelled meanwhile: the late tick is dropped.
	default:
		r.log.Warn("persist progress", "job", t.w.job, "err", err)
	}
}

// final is the progress stored with the download -> processing transition:
// percent 100, speed 0, eta 0 (contract README), with the last known byte
// counts, or the file size when yt-dlp reported none.
func (t *progressTracker) final(file string) *store.Progress {
	p := store.Progress{Percent: store.Ptr(100.0), SpeedBps: store.Ptr[int64](0), ETASeconds: store.Ptr[int64](0)}
	if t.have {
		p.DownloadedBytes, p.TotalBytes = t.last.DownloadedBytes, t.last.TotalBytes
	}
	if st, err := os.Stat(file); err == nil {
		if p.DownloadedBytes == nil {
			p.DownloadedBytes = store.Ptr(st.Size())
		}
		if p.TotalBytes == nil {
			p.TotalBytes = store.Ptr(st.Size())
		}
	}
	return &p
}
