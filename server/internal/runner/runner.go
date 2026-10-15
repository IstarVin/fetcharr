package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// Defaults for Options.
const (
	DefaultPollInterval     = 5 * time.Second
	DefaultProgressInterval = 500 * time.Millisecond
)

// Stop causes, carried by the worker contexts (context.Cause).
var (
	errPause    = errors.New("runner: job paused")
	errCancel   = errors.New("runner: job cancelled")
	errShutdown = errors.New("runner: shutting down")
	errPanic    = errors.New("runner: worker panicked")
)

// Options configures New. Config and Store are required.
type Options struct {
	Config *config.Config
	Store  *store.Store
	// Publisher receives every change the runner commits (default: none).
	Publisher service.Publisher
	// Processor runs the processing phase (default NopProcessor).
	Processor Processor
	Logger    *slog.Logger

	// Binary is the yt-dlp executable (default: ytdlp.binary, else the
	// managed binary in data_dir).
	Binary string
	// Concurrency overrides downloads.concurrency (tests).
	Concurrency int
	// PollInterval is the period of the safety poll that picks up pending
	// jobs even if a Wake was missed (default 5 s).
	PollInterval time.Duration
	// ProgressInterval is the minimum time between two persisted progress
	// updates of one job (default 500 ms). The WebSocket hub throttles
	// further.
	ProgressInterval time.Duration
	// KillGrace is the SIGTERM-to-SIGKILL delay when stopping yt-dlp
	// (default ytdlp.DefaultKillGrace).
	KillGrace time.Duration
	// FreeBytes returns the bytes available on the filesystem holding path
	// (default syscall.Statfs). Tests inject it.
	FreeBytes func(path string) (uint64, error)
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// entry is one busy job id: a worker, or the placeholder of a work directory
// deletion that has no worker.
type entry struct {
	worker  bool
	cancel  context.CancelCauseFunc
	wipe    bool // delete the work directory when the worker is done
	wiped   bool // the deletion happened
	counted bool // counted in Runner.downloading
}

// Runner is the dispatcher and worker pool. Create it with New, then Start it.
// It implements service.Controller.
type Runner struct {
	cfg  *config.Config
	st   *store.Store
	pub  service.Publisher
	proc Processor
	log  *slog.Logger

	binary        string
	concurrency   int
	poll          time.Duration
	progressEvery time.Duration
	killGrace     time.Duration
	freeBytes     func(string) (uint64, error)
	now           func() time.Time

	wake      chan struct{}
	quit      chan struct{} // closed by Shutdown
	loopDone  chan struct{}
	startOnce sync.Once
	downOnce  sync.Once

	mu          sync.Mutex
	busy        map[string]*entry
	workers     int
	downloading int
	idle        chan struct{} // closed while downloading == 0
	holds       int
	closing     bool
	wg          sync.WaitGroup // workers and work directory deletions
}

var _ service.Controller = (*Runner)(nil)

type nopPub struct{}

func (nopPub) Publish(service.Change) {}

// New returns a Runner. It starts nothing: call Start.
func New(o Options) *Runner {
	r := &Runner{
		cfg:           o.Config,
		st:            o.Store,
		pub:           o.Publisher,
		proc:          o.Processor,
		log:           o.Logger,
		binary:        o.Binary,
		concurrency:   o.Concurrency,
		poll:          o.PollInterval,
		progressEvery: o.ProgressInterval,
		killGrace:     o.KillGrace,
		freeBytes:     o.FreeBytes,
		now:           o.Now,
		wake:          make(chan struct{}, 1),
		quit:          make(chan struct{}),
		loopDone:      make(chan struct{}),
		busy:          make(map[string]*entry),
		idle:          make(chan struct{}),
	}
	close(r.idle)
	if r.pub == nil {
		r.pub = nopPub{}
	}
	if r.proc == nil {
		r.proc = NopProcessor{}
	}
	if r.log == nil {
		r.log = slog.Default()
	}
	if r.binary == "" {
		r.binary = r.cfg.Ytdlp.Binary
		if r.binary == "" {
			r.binary = r.cfg.ManagedYtdlpPath()
		}
	}
	if strings.ContainsRune(r.binary, filepath.Separator) && !filepath.IsAbs(r.binary) {
		// yt-dlp runs in the work dir, so a relative path would not resolve.
		if a, err := filepath.Abs(r.binary); err == nil {
			r.binary = a
		}
	}
	if r.concurrency <= 0 {
		r.concurrency = max(r.cfg.Downloads.Concurrency, 1)
	}
	if r.poll <= 0 {
		r.poll = DefaultPollInterval
	}
	if r.progressEvery <= 0 {
		r.progressEvery = DefaultProgressInterval
	}
	if r.freeBytes == nil {
		r.freeBytes = statfsFree
	}
	if r.now == nil {
		r.now = time.Now
	}
	return r
}

// Start launches the dispatcher goroutine. It is idempotent and has no
// effect after Shutdown.
func (r *Runner) Start() {
	r.startOnce.Do(func() {
		select {
		case <-r.quit:
			close(r.loopDone)
			return
		default:
		}
		go r.loop()
	})
}

// Wake asks the dispatcher to look for pending jobs. It never blocks and
// coalesces repeated calls.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Hold stops the dispatcher from starting new jobs until the returned
// release function is called (it is idempotent). Holds nest: jobs start only
// when every hold is released. Jobs that already started are not affected;
// use WaitIdle to wait for the downloading ones.
func (r *Runner) Hold() (release func()) {
	r.mu.Lock()
	r.holds++
	r.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			r.holds--
			r.mu.Unlock()
			r.Wake()
		})
	}
}

// Held reports whether at least one hold is active.
func (r *Runner) Held() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.holds > 0
}

// Downloading returns the number of jobs in the pre-check or download phase,
// that is, the jobs that block WaitIdle. Processing jobs are not counted.
func (r *Runner) Downloading() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.downloading
}

// Idle returns a channel that is closed when no job is in the pre-check or
// download phase. The channel belongs to the current idle period: if a job
// starts later, Idle returns a new open channel. Call Hold first, otherwise
// the dispatcher may start a job right after the channel closed. A job counts
// until its yt-dlp process group is dead, so a paused or cancelled job blocks
// Idle until the process is gone.
func (r *Runner) Idle() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.idle
}

// WaitIdle blocks until Idle is closed or ctx is done.
func (r *Runner) WaitIdle(ctx context.Context) error {
	select {
	case <-r.Idle():
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Stop implements service.Controller: it ends the job's run, if any. Pause
// keeps the work directory; cancel deletes it once the process group is dead,
// also when the job has no running worker (pending, paused, processing
// without a worker). It does not wait.
func (r *Runner) Stop(jobID string, reason service.StopReason) {
	if !validID(jobID) || r.superseded(jobID) {
		return
	}
	r.mu.Lock()
	if e := r.busy[jobID]; e != nil {
		if e.worker {
			if reason == service.StopCancel {
				e.wipe = true
				e.cancel(errCancel)
			} else {
				e.cancel(errPause)
			}
		}
		r.mu.Unlock()
		return
	}
	if reason != service.StopCancel {
		r.mu.Unlock()
		return
	}
	if r.closing {
		r.mu.Unlock()
		r.wipeWorkDir(jobID) // shutting down: nobody to wait for, do it here
		return
	}
	// No worker: delete the work directory in the background. The placeholder
	// keeps the id busy so a retry cannot start downloading into it meanwhile.
	r.busy[jobID] = &entry{}
	r.wg.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.wg.Done()
		r.wipeWorkDir(jobID)
		r.mu.Lock()
		delete(r.busy, jobID)
		r.mu.Unlock()
		r.Wake()
	}()
}

// superseded reports whether a Stop is stale: the service commits pause and
// cancel before calling Stop, so a job whose stored status is pending,
// downloading or processing has been resumed, retried or restarted since, and
// the Stop must not touch its (new) worker or work directory. Unknown jobs
// (already deleted) and lookup errors are not superseded.
func (r *Runner) superseded(id string) bool {
	j, err := r.st.Get(context.Background(), id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			r.log.Warn("stop: cannot read job; stopping anyway", "job", id, "err", err)
		}
		return false
	}
	switch j.Status {
	case jobs.StatusPending, jobs.StatusDownloading, jobs.StatusProcessing:
		r.log.Debug("ignoring stale stop", "job", j)
		return true
	}
	return false
}

// Shutdown stops the dispatcher, kills every running process group, sets
// downloading and processing jobs back to pending (work directories are
// kept) and waits for the workers, until ctx is done. It is idempotent.
func (r *Runner) Shutdown(ctx context.Context) error {
	r.downOnce.Do(func() {
		r.mu.Lock()
		r.closing = true
		for _, e := range r.busy {
			if e.worker {
				e.cancel(errShutdown)
			}
		}
		r.mu.Unlock()
		close(r.quit)
	})
	// Never started: nothing runs the loop, so mark it finished.
	r.startOnce.Do(func() { close(r.loopDone) })

	done := make(chan struct{})
	go func() {
		<-r.loopDone
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	// Workers are gone. A job still downloading or processing has no worker
	// (e.g. the S3 NopProcessor leaves jobs in processing): requeue it too.
	requeued, err := r.st.RequeueInterrupted(context.Background())
	if err != nil {
		return fmt.Errorf("requeue interrupted jobs: %w", err)
	}
	for _, j := range requeued {
		r.pub.Publish(service.Change{Kind: service.ChangeUpdated, Job: j})
	}
	return nil
}

func (r *Runner) loop() {
	defer close(r.loopDone)
	t := time.NewTicker(r.poll)
	defer t.Stop()
	for {
		r.fill()
		select {
		case <-r.quit:
			return
		case <-r.wake:
		case <-t.C:
		}
	}
}

type reservation int

const (
	reserved reservation = iota
	skipJob              // the job is busy; try the next one
	noSlot               // held, closing or pool full; stop
)

// fill starts workers for pending jobs, oldest queued_at first, while slots
// are free.
func (r *Runner) fill() {
	r.mu.Lock()
	free := r.concurrency - r.workers
	limit := free + len(r.busy)
	can := r.holds == 0 && !r.closing && free > 0
	r.mu.Unlock()
	if !can {
		return
	}
	pending, err := r.st.PendingJobs(context.Background(), limit)
	if err != nil {
		r.log.Error("list pending jobs", "err", err)
		return
	}
	for _, j := range pending {
		ctx, e, res := r.reserve(j.ID)
		if res == noSlot {
			return
		}
		if res == skipJob {
			continue
		}
		go r.work(ctx, e, j)
	}
}

// reserve takes a worker slot for id and registers its entry, atomically with
// the hold, closing and busy checks.
func (r *Runner) reserve(id string) (context.Context, *entry, reservation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.holds > 0 || r.closing || r.workers >= r.concurrency {
		return nil, nil, noSlot
	}
	if _, busy := r.busy[id]; busy {
		return nil, nil, skipJob
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	e := &entry{worker: true, cancel: cancel, counted: true}
	r.busy[id] = e
	r.workers++
	if r.downloading == 0 {
		r.idle = make(chan struct{})
	}
	r.downloading++
	r.wg.Add(1)
	return ctx, e, reserved
}

// leaveDownloading ends the entry's contribution to Downloading (idempotent).
func (r *Runner) leaveDownloading(e *entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leaveDownloadingLocked(e)
}

func (r *Runner) leaveDownloadingLocked(e *entry) {
	if !e.counted {
		return
	}
	e.counted = false
	r.downloading--
	if r.downloading == 0 {
		close(r.idle)
	}
}

// release frees the slot and the id of a finished worker. The work directory
// of a cancelled job is deleted first: the process group is dead by now.
func (r *Runner) release(id string, e *entry) {
	r.mu.Lock()
	// Checked under the lock up to the delete below, so a Stop that finds the
	// entry sets wipe in time and one that does not starts its own deletion.
	for e.wipe && !e.wiped {
		e.wiped = true
		r.mu.Unlock()
		r.wipeWorkDir(id)
		r.mu.Lock()
	}
	r.leaveDownloadingLocked(e)
	delete(r.busy, id)
	r.workers--
	r.mu.Unlock()
	r.wg.Done()
	r.Wake()
}

// workDir is the absolute work directory of a job (yt-dlp runs inside it).
func (r *Runner) workDir(id string) string {
	d := r.cfg.JobWorkDir(id)
	if a, err := filepath.Abs(d); err == nil {
		return a
	}
	return d
}

func (r *Runner) wipeWorkDir(id string) {
	if err := os.RemoveAll(r.workDir(id)); err != nil {
		r.log.Error("delete work directory", "job", id, "err", err)
	}
}

// validID guards the work directory path derived from a job id.
func validID(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id
}
