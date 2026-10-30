package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
// concurrent use. Store guards protect state transitions; lifecycle keeps
// each transition and its runner hooks ordered against subsequent actions.
type Service struct {
	lifecycle chan struct{}
	// submit serializes the active-target check with the insert or retry
	// that makes a job active, so two submissions for one library file
	// cannot both pass it.
	submit sync.Mutex

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
	s := &Service{store: st, cfg: cfg, pub: nopPublisher{}, ctl: nopController{}, now: timefmt.Now, log: slog.Default(), lifecycle: make(chan struct{}, 1)}
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

// existingMedia returns the library file that j would replace; "" if there is
// none. A failed lookup is logged and treated as none, because the worker
// pre-check (spec §4.1) looks again before the download starts.
func (s *Service) existingMedia(cat config.Category, j *store.Job) string {
	if j.Archive != nil {
		return "" // episodes already in the library are skipped when placing
	}
	t, err := library.TargetOf(cat, j)
	if err == nil {
		var path string
		if path, err = library.ExistingMedia(t); err == nil {
			return path
		}
	}
	s.log.Warn("cannot check for an existing file", "job", j, "err", err)
	return ""
}

// targetKey identifies the library item a job writes: its folder and base
// name, compared without case, since every video extension counts (§4.1).
func targetKey(t naming.Target) string {
	return strings.ToLower(filepath.Join(t.Dir, t.Base))
}

// seasonKey identifies the season folder a tv job writes into. An archive
// job claims the whole season.
func seasonKey(t naming.Target) string {
	return "season:" + strings.ToLower(t.Dir)
}

// activeTargets maps the library items of every active job to the job, and
// each season folder to an archive job filling it (else to any job writing
// into it). Jobs whose category or name cannot be resolved are left out.
func (s *Service) activeTargets(ctx context.Context) (map[string]*store.Job, error) {
	active, _, err := s.store.List(ctx, store.ListFilter{Statuses: jobs.ActiveStatuses()})
	if err != nil {
		return nil, err
	}
	out := make(map[string]*store.Job, len(active))
	for _, j := range active {
		cat, ok := s.cfg.Category(j.Category)
		if !ok {
			continue
		}
		if j.Archive != nil {
			if t, err := library.SeasonTarget(cat, j); err == nil {
				out[seasonKey(t)] = j
			}
			continue
		}
		if t, err := library.TargetOf(cat, j); err == nil {
			out[targetKey(t)] = j
			if cat.Layout == naming.LayoutTV {
				if _, taken := out[seasonKey(t)]; !taken {
					out[seasonKey(t)] = j
				}
			}
		}
	}
	return out, nil
}

// queuedFor returns the active job that already writes j's library item, if
// any. Different source URLs for one episode (common with expiring stream
// links) would otherwise download it twice and fail the second job.
func (s *Service) queuedFor(cat config.Category, j *store.Job, active map[string]*store.Job) *store.Job {
	if j.Archive != nil {
		t, err := library.SeasonTarget(cat, j)
		if err != nil {
			return nil
		}
		if other := active[seasonKey(t)]; other != nil && other.ID != j.ID {
			return other
		}
		return nil
	}
	t, err := library.TargetOf(cat, j)
	if err != nil {
		return nil
	}
	if other := active[targetKey(t)]; other != nil && other.ID != j.ID {
		return other
	}
	if cat.Layout == naming.LayoutTV {
		if other := active[seasonKey(t)]; other != nil && other.Archive != nil && other.ID != j.ID {
			return other
		}
	}
	return nil
}

func queuedErr(j *store.Job) error {
	return newErr(CodeDuplicate, nil, "already queued for the same library file (job %s)", j.ID)
}

// Create validates req and creates one pending job (spec §6.3
// POST /api/jobs). Errors: invalid_request, already_exists, duplicate.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*store.Job, error) {
	unlock := s.cfg.ReadSettings()
	defer unlock()
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
	if req.Archive != nil {
		if cat.Layout != naming.LayoutTV {
			return nil, invalid("archive needs a category with the tv layout")
		}
		if req.Episode != nil {
			return nil, invalid("episode must be omitted for an archive job")
		}
		if err := req.Archive.validate(); err != nil {
			return nil, err
		}
		meta.Episode = new(int) // validated per file when placing
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
		ID:              id,
		Category:        cat.ID,
		DestinationRoot: cat.Path,
		URL:             req.URL,
		Headers:         mergeHeaders(nil, req.Headers),
		Title:           optString(req.Title),
		Subtitles:       subs,
		Status:          jobs.StatusPending,
		Overwrite:       req.Overwrite,
		WorkDir:         s.cfg.JobWorkDir(id),
	}
	switch cat.Layout {
	case naming.LayoutMovie:
		j.Year = req.Year
	case naming.LayoutTV:
		j.ShowName = optString(req.ShowName)
		j.Season, j.Episode, j.Year = req.Season, req.Episode, req.Year
		if req.Archive != nil {
			j.Archive = &store.Archive{EpisodeOffset: req.Archive.EpisodeOffset, EpisodeTitles: req.Archive.EpisodeTitles}
		}
	case naming.LayoutFlat:
		// Year, show, season and episode do not apply: stored as null.
	}
	s.submit.Lock()
	defer s.submit.Unlock()
	active, err := s.activeTargets(ctx)
	if err != nil {
		return nil, err
	}
	if other := s.queuedFor(cat, j, active); other != nil {
		return nil, queuedErr(other)
	}
	if !j.Overwrite {
		if path := s.existingMedia(cat, j); path != "" {
			return nil, newErr(CodeAlreadyExists, nil, "a file already exists: %s", path)
		}
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
// episodes already in the library (unless req.Overwrite) or whose URL is
// already active are skipped and reported. Errors: invalid_request,
// already_exists (every episode skipped, at least one of them because it is
// in the library), duplicate (every episode skipped as duplicate).
func (s *Service) CreateBatch(ctx context.Context, req BatchRequest) (*BatchResult, error) {
	unlock := s.cfg.ReadSettings()
	defer unlock()
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
			ID:              id,
			BatchID:         store.Ptr(batchID),
			Category:        cat.ID,
			DestinationRoot: cat.Path,
			URL:             e.URL,
			Headers:         mergeHeaders(req.Headers, e.Headers),
			Title:           optString(e.Title),
			ShowName:        optString(req.ShowName),
			Season:          req.Season,
			Episode:         store.Ptr(*e.Episode),
			Year:            req.Year,
			Subtitles:       subs,
			Status:          jobs.StatusPending,
			Overwrite:       req.Overwrite,
			WorkDir:         s.cfg.JobWorkDir(id),
		}})
	}
	// Queue order follows ascending episode number (spec §2.5).
	sort.SliceStable(items, func(a, b int) bool { return items[a].ep < items[b].ep })
	s.submit.Lock()
	defer s.submit.Unlock()
	active, err := s.activeTargets(ctx)
	if err != nil {
		return nil, err
	}
	skipReason := make([]string, len(items))
	var batch []*store.Job
	for i, it := range items {
		if s.queuedFor(cat, it.job, active) != nil {
			skipReason[i] = SkipReasonDuplicate
			continue
		}
		// A repeated episode number in one request writes the same file.
		if t, err := library.TargetOf(cat, it.job); err == nil {
			active[targetKey(t)] = it.job
		}
		if !req.Overwrite && s.existingMedia(cat, it.job) != "" {
			skipReason[i] = SkipReasonAlreadyExists
			continue
		}
		batch = append(batch, it.job)
	}

	var results []error
	if len(batch) > 0 {
		if results, err = s.store.InsertBatch(ctx, batch); err != nil {
			return nil, err
		}
	}
	// results is aligned with batch, which holds the items not yet skipped.
	n := 0
	for i := range items {
		if skipReason[i] != "" {
			continue
		}
		if results[n] != nil {
			skipReason[i] = SkipReasonDuplicate
		}
		n++
	}
	res := &BatchResult{BatchID: batchID, Jobs: []*store.Job{}, Skipped: []SkippedEpisode{}}
	inLibrary := false
	for i, it := range items {
		if skipReason[i] != "" {
			res.Skipped = append(res.Skipped, SkippedEpisode{Episode: it.ep, Reason: skipReason[i]})
			inLibrary = inLibrary || skipReason[i] == SkipReasonAlreadyExists
			continue
		}
		res.Jobs = append(res.Jobs, it.job)
	}
	if len(res.Jobs) == 0 && inLibrary {
		return nil, newErr(CodeAlreadyExists, nil, "every episode is already in the library or active")
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
	if opts.Source != nil {
		if err := opts.Source.validate(); err != nil {
			return nil, err
		}
	}
	return s.apply(ctx, id, jobs.EventRetry, opts)
}

// BatchAction applies ev (pause, resume, cancel or retry) to every job of the
// batch where the transition is allowed and skips the rest (spec §6.3). It
// returns the updated jobs in queue order. Error: not_found for an unknown
// batch.
func (s *Service) BatchAction(ctx context.Context, batchID string, ev jobs.Event, opts RetryOptions) ([]*store.Job, error) {
	if opts.Source != nil {
		return nil, invalid("source replacement is only supported for individual jobs")
	}
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
	unlock := s.cfg.ReadSettings()
	defer unlock()
	// A resumed/retried job must not become pending before the previous stop
	// has cancelled its worker or reserved its work directory for deletion.
	select {
	case s.lifecycle <- struct{}{}:
		defer func() { <-s.lifecycle }()
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	if ev == jobs.EventRetry {
		s.submit.Lock()
		defer s.submit.Unlock()
	}
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
		update := s.updateFor(ev, to, opts)
		if ev == jobs.EventRetry {
			cat, err := s.category(cur.Category)
			if err != nil {
				return nil, err
			}
			meta := naming.Meta{Year: cur.Year, Season: cur.Season, Episode: cur.Episode}
			if cur.Title != nil {
				meta.Title = *cur.Title
			}
			if cur.ShowName != nil {
				meta.ShowName = *cur.ShowName
			}
			if err := naming.Validate(cat.Layout, meta); err != nil {
				return nil, wrapValidation("", err)
			}
			update.DestinationRoot = store.Set(cat.Path)
			active, err := s.activeTargets(ctx)
			if err != nil {
				return nil, err
			}
			if other := s.queuedFor(cat, cur, active); other != nil {
				return nil, queuedErr(other)
			}
			if source := opts.Source; source != nil {
				update.URL = store.Set(source.URL)
				headers := map[string]string{}
				switch source.HeadersMode {
				case "keep":
					headers = cur.Headers
				case "replace":
					headers = source.Headers
				}
				update.Headers = store.Set(headers)
				subs, err := normalizeSubtitles("source.subtitles", source.Subtitles)
				if err != nil {
					return nil, err
				}
				update.Subtitles = store.Set(subs)
				update.ResetWorkDir = store.Set(true)
				update.WorkDir = store.Set("")
				update.FilePath = store.Set[*string](nil)
			}
		}
		j, err := s.store.UpdateGuarded(ctx, id, guard, update)
		switch {
		case err == nil:
			s.afterAction(ev, cur.Status, j)
			return j, nil
		case errors.Is(err, store.ErrStatusMismatch):
			continue
		case errors.Is(err, store.ErrNotFound):
			return nil, notFound("job", id)
		case errors.Is(err, store.ErrDuplicate):
			url := cur.URL
			if opts.Source != nil {
				url = opts.Source.URL
			}
			return nil, duplicateErr(err, url)
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

// Prioritize makes a pending job the next one to start (spec §2.5). Errors:
// not_found, invalid_state (not pending).
func (s *Service) Prioritize(ctx context.Context, id string) (*store.Job, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur.Status != jobs.StatusPending {
		return nil, newErr(CodeInvalidState, nil, "cannot prioritize job in status '%s'", cur.Status)
	}
	moved, err := s.store.MoveToFront(ctx, []string{id})
	if err != nil {
		return nil, err
	}
	if len(moved) == 0 {
		return nil, newErr(CodeInvalidState, nil, "job %s is no longer pending", id)
	}
	s.pub.Publish(Change{Kind: ChangeUpdated, Job: moved[0]})
	return moved[0], nil
}

// PrioritizeBatch moves the pending episodes of a batch ahead of every other
// pending job, keeping their order, and returns them. Error: not_found.
func (s *Service) PrioritizeBatch(ctx context.Context, batchID string) ([]*store.Job, error) {
	members, err := s.store.ListByBatch(ctx, batchID)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, notFound("batch", batchID)
	}
	var ids []string
	for _, m := range members {
		if m.Status == jobs.StatusPending {
			ids = append(ids, m.ID)
		}
	}
	moved, err := s.store.MoveToFront(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, j := range moved {
		s.pub.Publish(Change{Kind: ChangeUpdated, Job: j})
	}
	return moved, nil
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
	if j.DestinationRoot == "" {
		return invalid("original destination of job '%s' is unknown; cannot delete the file", j.ID)
	}
	if _, err := library.RemoveFiles(j.DestinationRoot, *j.FilePath); err != nil {
		if errors.Is(err, library.ErrOutsideRoot) {
			return newErr(CodeInvalidRequest, err, "the file is outside its original category '%s'; not deleted", j.Category)
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
