package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/istarvin/fetcharr/server/internal/jobs"
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
	w.execute()
}

func (w *run) execute() {
	r, j := w.r, w.job
	cat, ok := r.cfg.Category(j.Category)
	if !ok {
		w.fail(CodeDownloadFailed, fmt.Sprintf("category '%s' no longer exists", j.Category))
		return
	}
	if w.ctx.Err() != nil {
		return // cancelled or shut down before the claim
	}
	if code, msg := r.precheck(j, cat); code != "" {
		w.fail(code, msg)
		return
	}

	// Claim: pending -> downloading, atomically, so no job starts twice.
	now := r.now()
	row, ok := r.move(j.ID, jobs.StatusPending, jobs.EventStart, store.Update{StartedAt: store.Set(&now)})
	if !ok {
		return // paused or cancelled while queued
	}
	w.status, w.job = jobs.StatusDownloading, row
	r.log.Info("job started", "job", row)

	workDir := r.workDir(j.ID)
	if err := os.MkdirAll(workDir, 0o750); err != nil {
		w.fail(CodeDownloadFailed, "cannot create the work directory: "+err.Error())
		return
	}

	pt := &progressTracker{w: w}
	res, err := ytdlp.Run(w.ctx, r.binary, ytdlp.BuildArgs(ytdlp.Spec{
		URL:         j.URL,
		Headers:     j.Headers,
		WorkDir:     workDir,
		MergeFormat: r.cfg.Downloads.MergeFormat,
		ExtraArgs:   r.cfg.Downloads.ExtraArgs,
	}), ytdlp.Options{
		Dir:        workDir,
		Secrets:    ytdlp.HeaderValues(j.Headers),
		KillGrace:  r.killGrace,
		OnProgress: pt.on,
	})
	if err != nil {
		w.downloadError(err)
		return
	}
	if w.ctx.Err() != nil {
		w.stopped() // finished just as a stop arrived
		return
	}
	file := res.FilePath
	if file == "" {
		file = ytdlp.FallbackFile(workDir)
	}
	if file == "" {
		w.fail(CodeDownloadFailed, "yt-dlp finished but produced no media file")
		return
	}

	row, ok = r.move(j.ID, jobs.StatusDownloading, jobs.EventDownloadDone, store.Update{
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
	if errors.As(err, &ye) {
		msg = ye.Message
	}
	w.fail(CodeDownloadFailed, msg)
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
	if _, ok := w.r.move(w.job.ID, w.status, jobs.EventFail, store.Update{
		ErrorCode:    store.Set(&code),
		ErrorMessage: store.Set(&msg),
		FinishedAt:   store.Set(&finished),
	}); ok {
		// Only the code is logged, never the message.
		w.r.log.Info("job failed", "job", w.job, "error_code", code)
	}
}

func (w *run) onPanic(p any) {
	w.r.log.Error("worker panicked", "job", w.job, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
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
	to, err := jobs.Transition(from, ev, false)
	if err != nil {
		r.log.Error("invalid runner transition", "job", id, "err", err)
		return nil, false
	}
	u.Status = store.Set(to)
	row, err := r.st.UpdateIf(context.Background(), id, []jobs.Status{from}, u)
	switch {
	case err == nil:
		r.pub.Publish(service.Change{Kind: service.ChangeUpdated, Job: row})
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
			t.w.r.log.Error("progress handler panicked", "panic", fmt.Sprint(rec))
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
