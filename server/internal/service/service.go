package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/library"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/store"
	"github.com/istarvin/fetcharr/server/internal/timefmt"
)

// maxCASAttempts bounds the read-transition-write loop of a lifecycle action
// when concurrent writers keep changing the job's status.
const maxCASAttempts = 4

// Service implements the job operations of spec §6.3. It is safe for
// concurrent use; correctness under concurrency comes from the store's
// compare-and-set updates and unique index, not from locks here.
type Service struct {
	store *store.Store
	cfg   *config.Config
	pub   Publisher
	ctl   Controller
	now   func() time.Time
	log   *slog.Logger
}

// Option customises a Service.
type Option func(*Service)

// WithPublisher sets the change subscriber (default: none).
func WithPublisher(p Publisher) Option {
	return func(s *Service) {
		if p != nil {
			s.pub = p
		}
	}
}

// WithController sets the runner hook (default: none; jobs stay pending).
func WithController(c Controller) Option {
	return func(s *Service) {
		if c != nil {
			s.ctl = c
		}
	}
}

// WithLogger sets the logger for the "job created" info line (default:
// slog.Default()). Only the job id, category and status are logged.
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.log = l
		}
	}
}

// WithClock overrides the time source (tests).
func WithClock(now func() time.Time) Option {
	return func(s *Service) { s.now = now }
}

// New returns a Service over st and cfg.
func New(st *store.Store, cfg *config.Config, opts ...Option) *Service {
	s := &Service{store: st, cfg: cfg, pub: nopPublisher{}, ctl: nopController{}, now: timefmt.Now, log: slog.Default()}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SetPublisher replaces the publisher. Call it during wiring, before the
// Service is shared between goroutines.
func (s *Service) SetPublisher(p Publisher) { WithPublisher(p)(s) }

// SetController replaces the runner hook. Call it during wiring, before the
// Service is shared between goroutines.
func (s *Service) SetController(c Controller) { WithController(c)(s) }

func (s *Service) category(id string) (config.Category, error) {
	if id == "" {
		return config.Category{}, invalid("category is required")
	}
	cat, ok := s.cfg.Category(id)
	if !ok {
		return config.Category{}, invalid("category '%s' does not exist", id)
	}
	return cat, nil
}

// wrapValidation turns a naming.ValidationError into an invalid_request.
func wrapValidation(prefix string, err error) error {
	var ve *naming.ValidationError
	if errors.As(err, &ve) {
		return newErr(CodeInvalidRequest, err, "%s%s", prefix, ve.Error())
	}
	return err
}

func duplicateErr(err error, url string) error {
	var d *store.DuplicateError
	if errors.As(err, &d) && d.URL != "" {
		url = d.URL
	}
	return newErr(CodeDuplicate, err, "url is already active: %s", url)
}

// Create validates req and creates one pending job (spec §6.3
// POST /api/jobs). Errors: invalid_request, duplicate.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*store.Job, error) {
	cat, err := s.category(req.Category)
	if err != nil {
		return nil, err
	}
	if err := validateHTTPURL("url", req.URL); err != nil {
		return nil, err
	}
	meta := naming.Meta{Title: req.Title, Year: req.Year}
	if cat.Layout == naming.LayoutTV {
		meta.ShowName, meta.Season, meta.Episode = req.ShowName, req.Season, req.Episode
	}
	if err := naming.Validate(cat.Layout, meta); err != nil {
		return nil, wrapValidation("", err)
	}
	if err := validateHeaders("headers", req.Headers); err != nil {
		return nil, err
	}
	subs, err := normalizeSubtitles("subtitles", req.Subtitles)
	if err != nil {
		return nil, err
	}

	id := store.NewID()
	j := &store.Job{
		ID:        id,
		Category:  cat.ID,
		URL:       req.URL,
		Headers:   mergeHeaders(nil, req.Headers),
		Title:     optString(req.Title),
		Subtitles: subs,
		Status:    jobs.StatusPending,
		WorkDir:   s.cfg.JobWorkDir(id),
	}
	switch cat.Layout {
	case naming.LayoutMovie:
		j.Year = req.Year
	case naming.LayoutTV:
		j.ShowName = optString(req.ShowName)
		j.Season, j.Episode, j.Year = req.Season, req.Episode, req.Year
	case naming.LayoutFlat:
		// Year, show, season and episode do not apply: stored as null.
	}
	if err := s.store.Insert(ctx, j); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return nil, duplicateErr(err, req.URL)
		}
		return nil, err
	}
	s.log.Info("job created", "job", j)
	s.pub.Publish(Change{Kind: ChangeAdded, Job: j})
	s.ctl.Wake()
	return j, nil
}

// CreateBatch validates req and creates the episodes of one season (spec §6.3
// POST /api/jobs/batch). Any invalid episode rejects the whole request;
// episodes whose URL is already active are skipped and reported. Errors:
// invalid_request, duplicate (every episode skipped).
func (s *Service) CreateBatch(ctx context.Context, req BatchRequest) (*BatchResult, error) {
	cat, err := s.category(req.Category)
	if err != nil {
		return nil, err
	}
	if cat.Layout != naming.LayoutTV {
		return nil, invalid("category '%s' does not have the tv layout", cat.ID)
	}
	if len(req.Episodes) == 0 {
		return nil, invalid("episodes must contain at least one episode")
	}
	if err := validateHeaders("headers", req.Headers); err != nil {
		return nil, err
	}

	batchID := store.NewID()
	type item struct {
		job *store.Job
		ep  int
	}
	items := make([]item, 0, len(req.Episodes))
	for i, e := range req.Episodes {
		where := fmt.Sprintf("episodes[%d]: ", i)
		meta := naming.Meta{ShowName: req.ShowName, Season: req.Season, Episode: e.Episode, Year: req.Year}
		if err := naming.Validate(cat.Layout, meta); err != nil {
			return nil, wrapValidation(where, err)
		}
		if err := validateHTTPURL(where+"url", e.URL); err != nil {
			return nil, err
		}
		if err := validateHeaders(where+"headers", e.Headers); err != nil {
			return nil, err
		}
		subs, err := normalizeSubtitles(where+"subtitles", e.Subtitles)
		if err != nil {
			return nil, err
		}
		id := store.NewID()
		items = append(items, item{ep: *e.Episode, job: &store.Job{
			ID:        id,
			BatchID:   store.Ptr(batchID),
			Category:  cat.ID,
			URL:       e.URL,
			Headers:   mergeHeaders(req.Headers, e.Headers),
			Title:     optString(e.Title),
			ShowName:  optString(req.ShowName),
			Season:    req.Season,
			Episode:   store.Ptr(*e.Episode),
			Year:      req.Year,
			Subtitles: subs,
			Status:    jobs.StatusPending,
			WorkDir:   s.cfg.JobWorkDir(id),
		}})
	}
	// Queue order follows ascending episode number (spec §2.5).
	sort.SliceStable(items, func(a, b int) bool { return items[a].ep < items[b].ep })
	batch := make([]*store.Job, len(items))
	for i, it := range items {
		batch[i] = it.job
	}

	results, err := s.store.InsertBatch(ctx, batch)
	if err != nil {
		return nil, err
	}
	res := &BatchResult{BatchID: batchID, Jobs: []*store.Job{}, Skipped: []SkippedEpisode{}}
	for i, it := range items {
		if results[i] != nil {
			res.Skipped = append(res.Skipped, SkippedEpisode{Episode: it.ep, Reason: SkipReasonDuplicate})
			continue
		}
		res.Jobs = append(res.Jobs, it.job)
	}
	if len(res.Jobs) == 0 {
		return nil, newErr(CodeDuplicate, nil, "every episode url is already active")
	}
	for _, j := range res.Jobs {
		s.log.Info("job created", "job", j, "batch", batchID)
		s.pub.Publish(Change{Kind: ChangeAdded, Job: j})
	}
	s.ctl.Wake()
	return res, nil
}

// Get returns one job. Error: not_found.
func (s *Service) Get(ctx context.Context, id string) (*store.Job, error) {
	j, err := s.store.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("job", id)
	}
	return j, err
}

// List returns the jobs matching f (newest created_at first) and the total
// number of matches ignoring Limit and Offset. Defaults and maximum of the
// page size are the caller's business (the API applies 50 / 500).
func (s *Service) List(ctx context.Context, f store.ListFilter) ([]*store.Job, int, error) {
	return s.store.List(ctx, f)
}

// Pause moves a pending or downloading job to paused. Errors: not_found,
// invalid_state.
func (s *Service) Pause(ctx context.Context, id string) (*store.Job, error) {
	return s.apply(ctx, id, jobs.EventPause, RetryOptions{})
}

// Resume moves a paused job back to pending at the end of the queue. Errors:
// not_found, invalid_state.
func (s *Service) Resume(ctx context.Context, id string) (*store.Job, error) {
	return s.apply(ctx, id, jobs.EventResume, RetryOptions{})
}

// Cancel moves a pending, downloading, processing (before placement) or
// paused job to cancelled. Errors: not_found, invalid_state.
func (s *Service) Cancel(ctx context.Context, id string) (*store.Job, error) {
	return s.apply(ctx, id, jobs.EventCancel, RetryOptions{})
}

// Retry requeues a failed or cancelled job. Errors: not_found, invalid_state,
// duplicate (another active job has the same URL).
func (s *Service) Retry(ctx context.Context, id string, opts RetryOptions) (*store.Job, error) {
	return s.apply(ctx, id, jobs.EventRetry, opts)
}

// BatchAction applies ev (pause, resume, cancel or retry) to every job of the
// batch where the transition is allowed and skips the rest (spec §6.3). It
// returns the updated jobs in queue order. Error: not_found for an unknown
// batch.
func (s *Service) BatchAction(ctx context.Context, batchID string, ev jobs.Event, opts RetryOptions) ([]*store.Job, error) {
	switch ev {
	case jobs.EventPause, jobs.EventResume, jobs.EventCancel, jobs.EventRetry:
	default:
		return nil, invalid("unsupported batch action %q", ev)
	}
	members, err := s.store.ListByBatch(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, notFound("batch", batchID)
	}
	updated := []*store.Job{}
	for _, m := range members {
		j, err := s.apply(ctx, m.ID, ev, opts)
		if code, ok := CodeOf(err); ok && (code == CodeInvalidState || code == CodeDuplicate || code == CodeNotFound) {
			continue // not allowed for this member: skip it
		}
		if err != nil {
			return nil, err
		}
		updated = append(updated, j)
	}
	return updated, nil
}

// apply runs one lifecycle action: read, consult the state machine, then a
// compare-and-set write guarded by the status that was read. A lost race
// re-reads and tries again.
func (s *Service) apply(ctx context.Context, id string, ev jobs.Event, opts RetryOptions) (*store.Job, error) {
	for range maxCASAttempts {
		cur, err := s.store.Get(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return nil, notFound("job", id)
		}
		if err != nil {
			return nil, err
		}
		to, err := jobs.Transition(cur.Status, ev, cur.Placing)
		if err != nil {
			return nil, invalidState(cur, ev, err)
		}
		// The guard is evaluated atomically with the write: the status must be
		// the one we read and, for cancelling a processing job, placing must
		// still be false (a runner may have started placing since the read).
		guard := store.UpdateGuard{Statuses: []jobs.Status{cur.Status}}
		if ev == jobs.EventCancel && cur.Status == jobs.StatusProcessing {
			guard.Placing = store.Ptr(false)
		}
		j, err := s.store.UpdateGuarded(ctx, id, guard, s.updateFor(ev, to, opts))
		switch {
		case err == nil:
			s.afterAction(ev, cur.Status, j)
			return j, nil
		case errors.Is(err, store.ErrStatusMismatch):
			continue
		case errors.Is(err, store.ErrNotFound):
			return nil, notFound("job", id)
		case errors.Is(err, store.ErrDuplicate):
			return nil, duplicateErr(err, cur.URL)
		default:
			return nil, err
		}
	}
	return nil, newErr(CodeInvalidState, nil, "job %s is changing concurrently, try again", id)
}

func invalidState(cur *store.Job, ev jobs.Event, cause error) error {
	if cur.Status == jobs.StatusProcessing && ev == jobs.EventCancel && cur.Placing {
		return newErr(CodeInvalidState, cause, "cannot cancel job %s: its files are being placed", cur.ID)
	}
	return newErr(CodeInvalidState, cause, "cannot %s job in status '%s'", ev, cur.Status)
}

// updateFor builds the store update for a transition: the side effects the
// pure state machine leaves to the service (queued_at, clearing on retry,
// finished_at).
func (s *Service) updateFor(ev jobs.Event, to jobs.Status, opts RetryOptions) store.Update {
	now := s.now()
	u := store.Update{Status: store.Set(to)}
	switch ev {
	case jobs.EventResume:
		u.QueuedAt = store.Set(now)
	case jobs.EventRetry:
		u.QueuedAt = store.Set(now)
		u.ErrorCode = store.Set[*string](nil)
		u.ErrorMessage = store.Set[*string](nil)
		u.Warnings = store.Set([]string{})
		u.Progress = store.Set[*store.Progress](nil)
		u.StartedAt = store.Set[*time.Time](nil)
		u.FinishedAt = store.Set[*time.Time](nil)
		u.Overwrite = store.Set(opts.Overwrite)
		u.Placing = store.Set(false)
	case jobs.EventCancel:
		u.FinishedAt = store.Set(&now)
	}
	return u
}

func (s *Service) afterAction(ev jobs.Event, from jobs.Status, j *store.Job) {
	s.pub.Publish(Change{Kind: ChangeUpdated, Job: j})
	switch ev {
	case jobs.EventResume, jobs.EventRetry:
		s.ctl.Wake()
	case jobs.EventPause:
		if from == jobs.StatusDownloading {
			s.ctl.Stop(j.ID, StopPause)
		}
	case jobs.EventCancel:
		s.ctl.Stop(j.ID, StopCancel)
	}
}

// Delete removes the record of a finished job. The status check and the
// delete are one atomic statement. With opts.DeleteFile, the media file and
// subtitle sidecars of a completed job are deleted first (and the folders
// that leaves empty, up to the category root); a failure keeps the record so
// the request can be repeated. Errors: not_found, invalid_state (job still
// active), invalid_request (delete_file for a file outside its category).
func (s *Service) Delete(ctx context.Context, id string, opts DeleteOptions) error {
	for range maxCASAttempts {
		cur, err := s.Get(ctx, id)
		if err != nil {
			return err
		}
		if _, err := jobs.Transition(cur.Status, jobs.EventDelete, cur.Placing); err != nil {
			return invalidState(cur, jobs.EventDelete, err)
		}
		if opts.DeleteFile && cur.Status == jobs.StatusCompleted && cur.FilePath != nil {
			if err := s.deleteFiles(cur); err != nil {
				return err
			}
		}
		err = s.store.DeleteIf(ctx, id, jobs.FinishedStatuses())
		switch {
		case err == nil:
			s.pub.Publish(Change{Kind: ChangeRemoved, JobIDs: []string{id}})
			return nil
		case errors.Is(err, store.ErrStatusMismatch):
			continue // status changed since the read: re-read and decide again
		case errors.Is(err, store.ErrNotFound):
			return notFound("job", id)
		default:
			return err
		}
	}
	return newErr(CodeInvalidState, nil, "job %s is changing concurrently, try again", id)
}

// deleteFiles removes the library files of a completed job (spec §6.3). A file
// that is already gone is not an error.
func (s *Service) deleteFiles(j *store.Job) error {
	cat, ok := s.cfg.Category(j.Category)
	if !ok {
		return invalid("category '%s' no longer exists; cannot delete the file", j.Category)
	}
	if _, err := library.RemoveFiles(cat.Path, *j.FilePath); err != nil {
		if errors.Is(err, library.ErrOutsideRoot) {
			return newErr(CodeInvalidRequest, err, "the file is outside category '%s'; not deleted", cat.ID)
		}
		return fmt.Errorf("delete files of job %s: %w", j.ID, err)
	}
	return nil
}

// DeleteMany clears history: it deletes every job whose status is in statuses
// (empty = completed, failed and cancelled) and returns how many. Active
// statuses are invalid_request. Files are never touched.
func (s *Service) DeleteMany(ctx context.Context, statuses []jobs.Status) (int, error) {
	for _, st := range statuses {
		if !jobs.IsFinished(st) {
			return 0, invalid("cannot delete jobs in status '%s'", st)
		}
	}
	ids, err := s.store.DeleteMany(ctx, statuses, nil)
	if err != nil {
		if errors.Is(err, store.ErrInvalidArgument) {
			return 0, newErr(CodeInvalidRequest, err, "%s", strings.TrimPrefix(err.Error(), store.ErrInvalidArgument.Error()+": "))
		}
		return 0, err
	}
	if len(ids) > 0 {
		s.pub.Publish(Change{Kind: ChangeRemoved, JobIDs: ids})
	}
	return len(ids), nil
}
