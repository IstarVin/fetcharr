// Package process implements the processing phase of a job (spec §4.3):
// subtitles, the second existing-file check, placing the files in the
// library, cleaning up the work directory and completing the job.
//
// # Cancel and shutdown (decision D7)
//
// Until store.SetPlacing succeeds, the job can be cancelled: ctx is cancelled,
// the Processor returns ctx's error and the runner deletes the work directory.
// Subtitle conversion (ffmpeg, in its own process group) is killed with it.
// SetPlacing runs before the first file moves; if a cancel won the race it
// reports store.ErrStatusMismatch and the Processor returns without touching
// any file.
//
// After SetPlacing, the API refuses cancel, so the only thing that can still
// cancel ctx is a graceful shutdown. Renames inside one filesystem take no
// time and always finish. A cross-filesystem copy, which can take minutes,
// honours ctx: it stops, deletes its .partial file and the files already
// placed for this job, and returns ctx's error, so the runner sets the job
// back to pending (spec §4.4) with the library untouched. Once the last file
// is in place the job is always completed, regardless of ctx.
package process

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/library"
	"github.com/istarvin/fetcharr/server/internal/logsafe"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
	"github.com/istarvin/fetcharr/server/internal/timefmt"
)

// Defaults for Options.
const (
	DefaultSubtitleTimeout  = time.Minute
	DefaultConvertTimeout   = 2 * time.Minute
	DefaultMaxSubtitleBytes = 10 << 20
	DefaultKillGrace        = 2 * time.Second
)

// Options configures New. Config and Store are required.
type Options struct {
	Config *config.Config
	Store  *store.Store
	// Publisher receives every change the Processor commits (default: none).
	Publisher service.Publisher
	Logger    *slog.Logger

	// OnCompleted is called in its own goroutine after a job completed,
	// with the completed row and the folder media servers should rescan (the
	// show folder for tv, the movie folder for movie, the category root for
	// flat; spec §4.5). The row contains request headers: never log it
	// except through its LogValue. Nil disables the hook.
	OnCompleted func(job *store.Job, refreshDir string)

	// FFmpeg is the ffmpeg executable (default "ffmpeg" from PATH).
	FFmpeg string
	// HTTPClient downloads subtitles (default: a client with the default
	// transport; the timeout is per request, see SubtitleTimeout).
	HTTPClient *http.Client
	// SubtitleTimeout bounds one subtitle download (default 1 minute).
	SubtitleTimeout time.Duration
	// ConvertTimeout bounds one ffmpeg conversion (default 2 minutes).
	ConvertTimeout time.Duration
	// MaxSubtitleBytes caps a subtitle download (default 10 MiB).
	MaxSubtitleBytes int64
	// KillGrace is the SIGTERM-to-SIGKILL delay for ffmpeg (default 2 s).
	KillGrace time.Duration
	// Rename moves a file (default os.Rename). Tests inject EXDEV to exercise
	// the cross-filesystem copy.
	Rename func(oldpath, newpath string) error
	// Now is the clock (default timefmt.Now).
	Now func() time.Time
}

// Processor implements runner.Processor.
type Processor struct {
	cfg *config.Config
	st  *store.Store
	pub service.Publisher
	log *slog.Logger

	onCompleted      func(*store.Job, string)
	ffmpeg           string
	client           *http.Client
	subtitleTimeout  time.Duration
	convertTimeout   time.Duration
	maxSubtitleBytes int64
	killGrace        time.Duration
	rename           func(string, string) error
	now              func() time.Time
	targetMu         sync.Mutex
	targets          map[string]*targetLock

	// afterSetPlacing, when set, runs right after SetPlacing succeeded and
	// before the first file moves (tests hold the placement here).
	afterSetPlacing func()
}

var _ runner.Processor = (*Processor)(nil)

type nopPub struct{}

func (nopPub) Publish(service.Change) {}

// New returns a Processor.
func New(o Options) *Processor {
	p := &Processor{
		cfg: o.Config, st: o.Store, pub: o.Publisher, log: o.Logger,
		onCompleted: o.OnCompleted, ffmpeg: o.FFmpeg, client: o.HTTPClient,
		subtitleTimeout: o.SubtitleTimeout, convertTimeout: o.ConvertTimeout,
		maxSubtitleBytes: o.MaxSubtitleBytes, killGrace: o.KillGrace,
		rename: o.Rename, now: o.Now,
	}
	if p.pub == nil {
		p.pub = nopPub{}
	}
	if p.log == nil {
		p.log = slog.Default()
	}
	if p.ffmpeg == "" {
		p.ffmpeg = "ffmpeg"
	}
	if p.client == nil {
		p.client = &http.Client{}
	}
	if p.subtitleTimeout <= 0 {
		p.subtitleTimeout = DefaultSubtitleTimeout
	}
	if p.convertTimeout <= 0 {
		p.convertTimeout = DefaultConvertTimeout
	}
	if p.maxSubtitleBytes <= 0 {
		p.maxSubtitleBytes = DefaultMaxSubtitleBytes
	}
	if p.killGrace <= 0 {
		p.killGrace = DefaultKillGrace
	}
	if p.rename == nil {
		p.rename = os.Rename
	}
	if p.now == nil {
		p.now = timefmt.Now
	}
	return p
}

func processingFailed(format string, args ...any) error {
	return &runner.Failure{Code: runner.CodeProcessingFailed, Message: fmt.Sprintf(format, args...)}
}

// Process implements runner.Processor (see its documentation for the
// contract and the package documentation for cancel and shutdown).
func (p *Processor) Process(ctx context.Context, job *store.Job, mediaFile string) error {
	cat, ok := p.cfg.Category(job.Category)
	if !ok {
		return processingFailed("category '%s' no longer exists", job.Category)
	}
	if filepath.Base(job.ID) != job.ID || job.ID == "." || job.ID == ".." {
		return processingFailed("invalid job id")
	}
	workDir := p.cfg.JobWorkDir(job.ID)
	if abs, err := filepath.Abs(workDir); err == nil {
		workDir = abs
	}

	// 1. Subtitles.
	subs, warnings, err := p.subtitles(ctx, job, workDir)
	if err != nil {
		return err
	}

	// 2. Existing file, again.
	target, err := library.TargetOf(cat, job)
	if err != nil {
		return processingFailed("cannot resolve the library path: %v", err)
	}
	// Containment first: a folder that escapes the root must not be listed.
	_, resolvedDir, err := library.ResolveDir(target.Root, target.Dir)
	if err != nil {
		return processingFailed("%v", err)
	}
	// Different URLs (or containers) can name the same library item. Keep the
	// existence check and placement together so only an explicit overwrite
	// can replace a file placed by another worker.
	unlock, err := p.lockTarget(ctx, filepath.Join(resolvedDir, target.Base))
	if err != nil {
		return err
	}
	defer unlock()
	if !job.Overwrite {
		existing, err := library.ExistingMedia(target)
		if err != nil {
			return processingFailed("cannot check for an existing file: %v", err)
		}
		if existing != "" {
			return &runner.Failure{Code: runner.CodeAlreadyExists, Message: "a file already exists: " + existing}
		}
	}
	var mediaSize int64
	if st, err := os.Stat(mediaFile); err == nil {
		mediaSize = st.Size()
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}

	// 3. Place the files. The lock comes first (D7).
	if _, err := p.st.SetPlacing(context.WithoutCancel(ctx), job.ID); err != nil {
		if errors.Is(err, store.ErrStatusMismatch) || errors.Is(err, store.ErrNotFound) {
			return nil // a cancel won: the service and the runner clean up
		}
		return processingFailed("cannot start placing: %v", err)
	}
	if p.afterSetPlacing != nil {
		p.afterSetPlacing()
	}
	finalMedia := target.MediaPath(filepath.Ext(mediaFile))
	if err := p.place(ctx, target, mediaFile, subs, job.Overwrite); err != nil {
		p.clearPlacing(job.ID)
		if ctx.Err() != nil {
			return err // shutdown: the runner puts the job back to pending
		}
		return processingFailed("%v", err)
	}

	// 4. Clean up. The files are in the library: a failure here only leaves
	// disk space behind.
	if err := os.RemoveAll(workDir); err != nil {
		p.log.Warn("delete work directory", "job", job, "err", err)
	}

	// 5. Complete.
	return p.complete(job, finalMedia, mediaSize, warnings, target.RefreshDir)
}

// clearPlacing resets the placing flag of a job that failed or was
// interrupted during placement.
func (p *Processor) clearPlacing(id string) {
	if _, err := p.st.UpdateIf(context.Background(), id, []jobs.Status{jobs.StatusProcessing},
		store.Update{Placing: store.Set(false)}); err != nil &&
		!errors.Is(err, store.ErrStatusMismatch) && !errors.Is(err, store.ErrNotFound) {
		p.log.Warn("clear placing flag", "job", id, "err", err)
	}
}

// complete commits processing -> completed and notifies the media servers.
func (p *Processor) complete(job *store.Job, filePath string, mediaSize int64, warnings []string, refreshDir string) error {
	to, err := jobs.Transition(jobs.StatusProcessing, jobs.EventProcessingDone, true)
	if err != nil {
		return processingFailed("%v", err)
	}
	finished := p.now()
	row, err := p.st.UpdateIf(context.Background(), job.ID, []jobs.Status{jobs.StatusProcessing}, store.Update{
		Status:     store.Set(to),
		FilePath:   store.Set(&filePath),
		Progress:   store.Set(completedProgress(job.Progress, mediaSize)),
		Warnings:   store.Set(warnings),
		Placing:    store.Set(false),
		FinishedAt: store.Set(&finished),
	})
	switch {
	case err == nil:
	case errors.Is(err, store.ErrStatusMismatch), errors.Is(err, store.ErrNotFound):
		p.log.Warn("job changed while completing; completion skipped", "job", job)
		return nil
	default:
		return processingFailed("cannot complete the job: %v", err)
	}
	p.pub.Publish(service.Change{Kind: service.ChangeUpdated, Job: row})
	p.log.Info("job completed", "job", row)
	if p.onCompleted != nil {
		go p.notify(row, refreshDir)
	}
	return nil
}

func (p *Processor) notify(row *store.Job, refreshDir string) {
	defer func() {
		if r := recover(); r != nil {
			p.log.Error("completion hook panicked", "job", row, "panic", logsafe.Text(fmt.Sprint(r)))
		}
	}()
	p.onCompleted(row, refreshDir)
}

// completedProgress is the progress of a completed job: percent 100, speed 0,
// eta 0 (contract README), keeping the byte counts.
func completedProgress(old *store.Progress, size int64) *store.Progress {
	p := store.Progress{}
	if old != nil {
		p = *old
	}
	p.Percent, p.SpeedBps, p.ETASeconds = store.Ptr(100.0), store.Ptr[int64](0), store.Ptr[int64](0)
	if size > 0 {
		if p.DownloadedBytes == nil {
			p.DownloadedBytes = store.Ptr(size)
		}
		if p.TotalBytes == nil {
			p.TotalBytes = store.Ptr(size)
		}
	}
	return &p
}
