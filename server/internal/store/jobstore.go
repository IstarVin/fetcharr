package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"modernc.org/sqlite"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/timefmt"
)

const jobColumns = `id, batch_id, category, url, headers, title, show_name, season, episode, year,
 subtitles, status, progress, file_path, error_code, error_message, warnings,
 overwrite, placing, work_dir, created_at, queued_at, started_at, finished_at, destination_root, reset_work_dir`

type rowScanner interface{ Scan(dest ...any) error }

func scanJob(r rowScanner) (*Job, error) {
	var (
		j                                   Job
		batchID, title, showName            sql.NullString
		season, episode, year               sql.NullInt64
		headers, subtitles, warnings        string
		progress, filePath, errCode, errMsg sql.NullString
		status, created, queued             string
		started, finished                   sql.NullString
		overwrite, placing, resetWorkDir    int
	)
	err := r.Scan(&j.ID, &batchID, &j.Category, &j.URL, &headers, &title, &showName, &season, &episode, &year,
		&subtitles, &status, &progress, &filePath, &errCode, &errMsg, &warnings,
		&overwrite, &placing, &j.WorkDir, &created, &queued, &started, &finished, &j.DestinationRoot, &resetWorkDir)
	if err != nil {
		return nil, err
	}
	j.BatchID = nullStr(batchID)
	j.Title = nullStr(title)
	j.ShowName = nullStr(showName)
	j.Season = nullInt(season)
	j.Episode = nullInt(episode)
	j.Year = nullInt(year)
	j.FilePath = nullStr(filePath)
	j.ErrorCode = nullStr(errCode)
	j.ErrorMessage = nullStr(errMsg)
	j.Status = jobs.Status(status)
	j.ResetWorkDir = resetWorkDir != 0
	j.Overwrite = overwrite != 0
	j.Placing = placing != 0

	j.Headers = map[string]string{}
	if err := json.Unmarshal([]byte(headers), &j.Headers); err != nil {
		return nil, fmt.Errorf("job %s: bad headers json: %w", j.ID, err)
	}
	j.Subtitles = []Subtitle{}
	if err := json.Unmarshal([]byte(subtitles), &j.Subtitles); err != nil {
		return nil, fmt.Errorf("job %s: bad subtitles json: %w", j.ID, err)
	}
	j.Warnings = []string{}
	if err := json.Unmarshal([]byte(warnings), &j.Warnings); err != nil {
		return nil, fmt.Errorf("job %s: bad warnings json: %w", j.ID, err)
	}
	if progress.Valid {
		j.Progress = &Progress{}
		if err := json.Unmarshal([]byte(progress.String), j.Progress); err != nil {
			return nil, fmt.Errorf("job %s: bad progress json: %w", j.ID, err)
		}
	}
	if j.CreatedAt, err = timefmt.Parse(created); err != nil {
		return nil, err
	}
	if j.QueuedAt, err = timefmt.Parse(queued); err != nil {
		return nil, err
	}
	if j.StartedAt, err = nullTime(started); err != nil {
		return nil, err
	}
	if j.FinishedAt, err = nullTime(finished); err != nil {
		return nil, err
	}
	return &j, nil
}

func nullStr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}

func nullInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func nullTime(n sql.NullString) (*time.Time, error) {
	if !n.Valid {
		return nil, nil
	}
	t, err := timefmt.Parse(n.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// optTime/optStr/optInt convert pointers to driver arguments (nil -> NULL).
func optTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return timefmt.Format(*t)
}

func optStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func optInt(i *int) any {
	if i == nil {
		return nil
	}
	return *i
}

func encodeProgress(p *Progress) (any, error) {
	if p == nil {
		return nil, nil
	}
	b, err := json.Marshal(p)
	return string(b), err
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// prepare fills defaults and validates j before insertion. index offsets the
// default timestamps so batch members are 1 ms apart.
func prepare(j *Job, base time.Time) error {
	if j == nil {
		return fmt.Errorf("%w: nil job", ErrInvalidArgument)
	}
	if j.URL == "" || j.Category == "" {
		return fmt.Errorf("%w: job needs url and category", ErrInvalidArgument)
	}
	if j.ID == "" {
		j.ID = NewID()
	}
	if j.Status == "" {
		j.Status = jobs.StatusPending
	}
	if !jobs.IsValid(j.Status) {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidArgument, j.Status)
	}
	if j.CreatedAt.IsZero() {
		j.CreatedAt = base
	}
	j.CreatedAt = timefmt.Truncate(j.CreatedAt)
	if j.QueuedAt.IsZero() {
		j.QueuedAt = j.CreatedAt
	}
	j.QueuedAt = timefmt.Truncate(j.QueuedAt)
	if j.Headers == nil {
		j.Headers = map[string]string{}
	}
	if j.Subtitles == nil {
		j.Subtitles = []Subtitle{}
	}
	if j.Warnings == nil {
		j.Warnings = []string{}
	}
	return nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func insertJob(ctx context.Context, x execer, j *Job) error {
	headers, err := json.Marshal(j.Headers)
	if err != nil {
		return err
	}
	subs, err := json.Marshal(j.Subtitles)
	if err != nil {
		return err
	}
	warns, err := json.Marshal(j.Warnings)
	if err != nil {
		return err
	}
	prog, err := encodeProgress(j.Progress)
	if err != nil {
		return err
	}
	_, err = x.ExecContext(ctx, `INSERT INTO jobs (`+jobColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		j.ID, optStr(j.BatchID), j.Category, j.URL, string(headers), optStr(j.Title), optStr(j.ShowName),
		optInt(j.Season), optInt(j.Episode), optInt(j.Year), string(subs), string(j.Status), prog,
		optStr(j.FilePath), optStr(j.ErrorCode), optStr(j.ErrorMessage), string(warns),
		b2i(j.Overwrite), b2i(j.Placing), j.WorkDir,
		timefmt.Format(j.CreatedAt), timefmt.Format(j.QueuedAt), optTime(j.StartedAt), optTime(j.FinishedAt), j.DestinationRoot, b2i(j.ResetWorkDir))
	if err != nil {
		return wrapConstraint(ctx, x, err, j.URL)
	}
	return nil
}

// wrapConstraint turns a unique-index violation on jobs.url into a
// *DuplicateError (looking up the blocking job's id on a best-effort basis).
func wrapConstraint(ctx context.Context, x execer, err error, url string) error {
	var se *sqlite.Error
	if errors.As(err, &se) && se.Code()&0xff == 19 /* SQLITE_CONSTRAINT */ && strings.Contains(se.Error(), "jobs.url") {
		d := &DuplicateError{URL: url}
		var id string
		q := `SELECT id FROM jobs WHERE url = ? AND status IN (` + activeList() + `) LIMIT 1`
		if x.QueryRowContext(ctx, q, url).Scan(&id) == nil {
			d.ExistingID = id
		}
		return d
	}
	return err
}

// activeList renders the active statuses as a SQL literal list. The values
// are compile-time constants, never user input.
func activeList() string {
	parts := make([]string, 0, 4)
	for _, s := range jobs.ActiveStatuses() {
		parts = append(parts, "'"+string(s)+"'")
	}
	return strings.Join(parts, ",")
}

// Insert stores a new job. Zero fields are defaulted: ID (new UUID), Status
// (pending), CreatedAt (now, ms), QueuedAt (= CreatedAt). It returns a
// *DuplicateError (errors.Is(err, ErrDuplicate)) when another job with the
// same URL is active; this is race-free because it is enforced by a partial
// unique index.
func (s *Store) Insert(ctx context.Context, j *Job) error {
	if err := prepare(j, timefmt.Now()); err != nil {
		return err
	}
	return insertJob(ctx, s.db, j)
}

// InsertBatch inserts jobs in one transaction. A job whose URL is already
// active (including one earlier in the same slice) is skipped, not fatal: the
// returned slice is aligned with the input, with nil for inserted jobs and a
// *DuplicateError for skipped ones. Any other error rolls everything back and
// is returned as the second value.
//
// Jobs with a zero CreatedAt get the same base time plus i milliseconds (i =
// position in jobs), so queue order follows slice order and is observable.
func (s *Store) InsertBatch(ctx context.Context, batch []*Job) ([]error, error) {
	base := timefmt.Now()
	for i, j := range batch {
		if err := prepare(j, base.Add(time.Duration(i)*time.Millisecond)); err != nil {
			return nil, err
		}
	}
	results := make([]error, len(batch))
	err := s.withWriteTx(ctx, func(c *sql.Conn) error {
		for i, j := range batch {
			// A failed INSERT only aborts that statement, not the transaction.
			err := insertJob(ctx, c, j)
			var d *DuplicateError
			switch {
			case err == nil:
			case errors.As(err, &d):
				results[i] = d
			default:
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// Get returns the job with the given id, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (*Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return j, err
}

// ListFilter selects jobs for List. Zero values mean "no constraint".
type ListFilter struct {
	Statuses      []jobs.Status // any of
	Category      string
	BatchID       string
	FinishedAfter *time.Time // finished_at strictly later than this
	Limit         int        // <= 0: no limit (the API applies its own default 50 / max 500)
	Offset        int
}

func (f ListFilter) where() (string, []any, error) {
	var conds []string
	var args []any
	if len(f.Statuses) > 0 {
		ph := make([]string, len(f.Statuses))
		for i, st := range f.Statuses {
			if !jobs.IsValid(st) {
				return "", nil, fmt.Errorf("%w: unknown status %q", ErrInvalidArgument, st)
			}
			ph[i] = "?"
			args = append(args, string(st))
		}
		conds = append(conds, "status IN ("+strings.Join(ph, ",")+")")
	}
	if f.Category != "" {
		conds = append(conds, "category = ?")
		args = append(args, f.Category)
	}
	if f.BatchID != "" {
		conds = append(conds, "batch_id = ?")
		args = append(args, f.BatchID)
	}
	if f.FinishedAfter != nil {
		conds = append(conds, "finished_at > ?")
		args = append(args, timefmt.Format(*f.FinishedAfter))
	}
	if len(conds) == 0 {
		return "", nil, nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args, nil
}

// List returns the jobs matching f, newest created_at first (ties: most
// recently inserted first), plus the total number of matches ignoring
// Limit/Offset.
func (s *Store) List(ctx context.Context, f ListFilter) ([]*Job, int, error) {
	where, args, err := f.where()
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT ` + jobColumns + ` FROM jobs` + where + ` ORDER BY created_at DESC, rowid DESC`
	qargs := append([]any{}, args...)
	if f.Limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		qargs = append(qargs, f.Limit, max(f.Offset, 0))
	} else if f.Offset > 0 {
		q += ` LIMIT -1 OFFSET ?`
		qargs = append(qargs, f.Offset)
	}
	out, err := s.queryJobs(ctx, q, qargs...)
	return out, total, err
}

func (s *Store) queryJobs(ctx context.Context, q string, args ...any) ([]*Job, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []*Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ListByBatch returns the jobs of a batch in queue order (queued_at, then
// insertion order), which is ascending episode order for batches created with
// InsertBatch. An unknown batch yields an empty slice.
func (s *Store) ListByBatch(ctx context.Context, batchID string) ([]*Job, error) {
	return s.queryJobs(ctx, `SELECT `+jobColumns+` FROM jobs WHERE batch_id = ? ORDER BY queued_at ASC, rowid ASC`, batchID)
}

// PendingJobs returns up to limit pending jobs in dispatch order: ascending
// queued_at (spec §2.5), ties by insertion order. limit <= 0 means no limit.
func (s *Store) PendingJobs(ctx context.Context, limit int) ([]*Job, error) {
	q := `SELECT ` + jobColumns + ` FROM jobs WHERE status = 'pending' ORDER BY queued_at ASC, rowid ASC`
	if limit > 0 {
		return s.queryJobs(ctx, q+` LIMIT ?`, limit)
	}
	return s.queryJobs(ctx, q)
}

// Opt marks an optional field of Update as set (even to a nil/zero value).
type Opt[T any] struct {
	Set   bool
	Value T
}

// Set builds an Opt that is set to v.
func Set[T any](v T) Opt[T] { return Opt[T]{Set: true, Value: v} }

// Update is a partial update of a job: only fields with Set == true are
// written. Nullable columns use pointer values: Set[*string](nil) writes NULL.
type Update struct {
	URL             Opt[string]
	Headers         Opt[map[string]string]
	Subtitles       Opt[[]Subtitle]
	ResetWorkDir    Opt[bool]
	Status          Opt[jobs.Status]
	Progress        Opt[*Progress]
	FilePath        Opt[*string]
	ErrorCode       Opt[*string]
	ErrorMessage    Opt[*string]
	Warnings        Opt[[]string]
	Overwrite       Opt[bool]
	Placing         Opt[bool]
	WorkDir         Opt[string]
	DestinationRoot Opt[string]
	QueuedAt        Opt[time.Time]
	StartedAt       Opt[*time.Time]
	FinishedAt      Opt[*time.Time]
}

// Update applies u to the job and returns the updated row. It returns
// ErrNotFound for an unknown id and a *DuplicateError when the new status
// would make the job active while another active job has the same URL
// (e.g. retrying a failed job whose URL was queued again).
func (s *Store) Update(ctx context.Context, id string, u Update) (*Job, error) {
	return s.update(ctx, id, UpdateGuard{}, u)
}

// UpdateIf is Update guarded by the current status: the write happens only if
// the job's status is one of ifStatus, otherwise ErrStatusMismatch is
// returned. This is a compare-and-set that keeps a late progress update from
// overwriting a pause or cancel.
func (s *Store) UpdateIf(ctx context.Context, id string, ifStatus []jobs.Status, u Update) (*Job, error) {
	if len(ifStatus) == 0 {
		return nil, fmt.Errorf("%w: UpdateIf needs at least one status", ErrInvalidArgument)
	}
	return s.update(ctx, id, UpdateGuard{Statuses: ifStatus}, u)
}

// UpdateGuard is the precondition of a compare-and-set update. The zero value
// guards nothing.
type UpdateGuard struct {
	// Statuses: the job's status must be one of them (empty = any).
	Statuses []jobs.Status
	// Placing: when non-nil, the job's placing flag must equal it. This makes
	// "cancel only while not placing" atomic with the write.
	Placing *bool
}

// UpdateGuarded is Update guarded by g, evaluated atomically with the write
// (one SQL statement). It returns ErrNotFound for an unknown id and
// ErrStatusMismatch when the row exists but does not satisfy g.
func (s *Store) UpdateGuarded(ctx context.Context, id string, g UpdateGuard, u Update) (*Job, error) {
	return s.update(ctx, id, g, u)
}

// SetPlacing marks a job as placing (spec §4.3 step 3, decision D7): it sets
// placing=true only if the job is still processing. If a cancel won the race
// (or the job is in any other status) it returns ErrStatusMismatch and the
// runner must not move any file.
func (s *Store) SetPlacing(ctx context.Context, id string) (*Job, error) {
	return s.update(ctx, id, UpdateGuard{Statuses: []jobs.Status{jobs.StatusProcessing}},
		Update{Placing: Set(true)})
}

func (s *Store) update(ctx context.Context, id string, g UpdateGuard, u Update) (*Job, error) {
	ifStatus := g.Statuses
	var sets []string
	var args []any
	add := func(col string, v any) {
		sets = append(sets, col+" = ?")
		args = append(args, v)
	}
	if u.URL.Set {
		add("url", u.URL.Value)
	}
	if u.Headers.Set {
		b, err := json.Marshal(u.Headers.Value)
		if err != nil {
			return nil, err
		}
		add("headers", string(b))
	}
	if u.Subtitles.Set {
		b, err := json.Marshal(u.Subtitles.Value)
		if err != nil {
			return nil, err
		}
		add("subtitles", string(b))
	}
	if u.ResetWorkDir.Set {
		add("reset_work_dir", b2i(u.ResetWorkDir.Value))
	}
	if u.Status.Set {
		if !jobs.IsValid(u.Status.Value) {
			return nil, fmt.Errorf("%w: unknown status %q", ErrInvalidArgument, u.Status.Value)
		}
		add("status", string(u.Status.Value))
	}
	if u.Progress.Set {
		p, err := encodeProgress(u.Progress.Value)
		if err != nil {
			return nil, err
		}
		add("progress", p)
	}
	if u.FilePath.Set {
		add("file_path", optStr(u.FilePath.Value))
	}
	if u.ErrorCode.Set {
		add("error_code", optStr(u.ErrorCode.Value))
	}
	if u.ErrorMessage.Set {
		add("error_message", optStr(u.ErrorMessage.Value))
	}
	if u.Warnings.Set {
		w := u.Warnings.Value
		if w == nil {
			w = []string{}
		}
		b, err := json.Marshal(w)
		if err != nil {
			return nil, err
		}
		add("warnings", string(b))
	}
	if u.Overwrite.Set {
		add("overwrite", b2i(u.Overwrite.Value))
	}
	if u.Placing.Set {
		add("placing", b2i(u.Placing.Value))
	}
	if u.WorkDir.Set {
		add("work_dir", u.WorkDir.Value)
	}
	if u.DestinationRoot.Set {
		add("destination_root", u.DestinationRoot.Value)
	}
	if u.QueuedAt.Set {
		add("queued_at", timefmt.Format(u.QueuedAt.Value))
	}
	if u.StartedAt.Set {
		add("started_at", optTime(u.StartedAt.Value))
	}
	if u.FinishedAt.Set {
		add("finished_at", optTime(u.FinishedAt.Value))
	}
	if len(sets) == 0 {
		j, err := s.Get(ctx, id)
		if err == nil && !g.matches(j) {
			return nil, ErrStatusMismatch
		}
		return j, err
	}

	q := `UPDATE jobs SET ` + strings.Join(sets, ", ") + ` WHERE id = ?`
	args = append(args, id)
	if len(ifStatus) > 0 {
		ph := make([]string, len(ifStatus))
		for i, st := range ifStatus {
			ph[i] = "?"
			args = append(args, string(st))
		}
		q += ` AND status IN (` + strings.Join(ph, ",") + `)`
	}
	if g.Placing != nil {
		q += ` AND placing = ?`
		args = append(args, b2i(*g.Placing))
	}
	q += ` RETURNING ` + jobColumns

	j, err := scanJob(s.db.QueryRowContext(ctx, q, args...))
	switch {
	case err == nil:
		return j, nil
	case errors.Is(err, sql.ErrNoRows):
		if _, gerr := s.Get(ctx, id); gerr != nil {
			return nil, gerr // ErrNotFound
		}
		return nil, ErrStatusMismatch
	default:
		cur, gerr := s.Get(ctx, id)
		url := ""
		if gerr == nil {
			url = cur.URL
			if u.URL.Set {
				url = u.URL.Value
			}
		}
		return nil, wrapConstraint(ctx, s.db, err, url)
	}
}

func (g UpdateGuard) matches(j *Job) bool {
	if len(g.Statuses) > 0 && !slices.Contains(g.Statuses, j.Status) {
		return false
	}
	return g.Placing == nil || *g.Placing == j.Placing
}

// DeleteIf removes one job record only if its status is one of statuses, in
// one atomic statement. ErrNotFound if the job does not exist,
// ErrStatusMismatch if it exists in another status.
func (s *Store) DeleteIf(ctx context.Context, id string, statuses []jobs.Status) error {
	if len(statuses) == 0 {
		return fmt.Errorf("%w: DeleteIf needs at least one status", ErrInvalidArgument)
	}
	ph := make([]string, len(statuses))
	args := []any{id}
	for i, st := range statuses {
		ph[i] = "?"
		args = append(args, string(st))
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ? AND status IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}
	if _, err := s.Get(ctx, id); err != nil {
		return err // ErrNotFound
	}
	return ErrStatusMismatch
}

// Delete removes one job record. It does not check the status (the caller
// applies jobs.EventDelete first). ErrNotFound if it does not exist.
func (s *Store) Delete(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteMany clears history (spec §6.3 DELETE /api/jobs): it deletes finished
// jobs whose status is in statuses (empty = completed, failed and cancelled)
// and, when finishedBefore is non-nil, whose finished_at is older. It returns
// the deleted ids (for job_removed events). Active statuses are rejected
// with ErrInvalidArgument. Files are never touched.
func (s *Store) DeleteMany(ctx context.Context, statuses []jobs.Status, finishedBefore *time.Time) ([]string, error) {
	if len(statuses) == 0 {
		statuses = jobs.FinishedStatuses()
	}
	ph := make([]string, len(statuses))
	args := make([]any, 0, len(statuses)+1)
	for i, st := range statuses {
		if !jobs.IsFinished(st) {
			return nil, fmt.Errorf("%w: cannot bulk-delete status %q", ErrInvalidArgument, st)
		}
		ph[i] = "?"
		args = append(args, string(st))
	}
	q := `DELETE FROM jobs WHERE status IN (` + strings.Join(ph, ",") + `)`
	if finishedBefore != nil {
		q += ` AND finished_at < ?`
		args = append(args, timefmt.Format(*finishedBefore))
	}
	q += ` RETURNING id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RequeueInterrupted implements startup recovery (spec §4.4): jobs left in
// downloading or processing go back to pending (placing cleared, work_dir
// kept, queue position kept). It returns the affected jobs.
func (s *Store) RequeueInterrupted(ctx context.Context) ([]*Job, error) {
	return s.queryJobs(ctx, `UPDATE jobs SET status = 'pending', placing = 0
		WHERE status IN ('downloading','processing') RETURNING `+jobColumns)
}
