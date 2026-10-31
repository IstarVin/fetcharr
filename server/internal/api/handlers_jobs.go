package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
	"github.com/istarvin/fetcharr/server/internal/timefmt"
)

const (
	defaultLimit = 50
	maxLimit     = 500
	maxQueryLen  = 200
)

func (s *server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req service.CreateRequest
	if !s.decodeJSON(w, r, &req, false) {
		return
	}
	j, err := s.svc.Create(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, NewJobDTO(j))
}

func (s *server) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	var req service.BatchRequest
	if !s.decodeJSON(w, r, &req, false) {
		return
	}
	res, err := s.svc.CreateBatch(r.Context(), req)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	skipped := make([]SkippedEpisode, 0, len(res.Skipped))
	for _, sk := range res.Skipped {
		skipped = append(skipped, SkippedEpisode(sk))
	}
	writeJSON(w, http.StatusCreated, BatchCreatedDTO{
		BatchID: res.BatchID,
		Jobs:    NewJobDTOs(res.Jobs),
		Skipped: skipped,
	})
}

func (s *server) handleGet(w http.ResponseWriter, r *http.Request) {
	j, err := s.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, NewJobDTO(j))
}

// parseStatuses parses a comma-separated status list. Empty items are
// ignored; an unknown status is an error.
func parseStatuses(raw string) ([]jobs.Status, string) {
	var out []jobs.Status
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		st := jobs.Status(part)
		if !jobs.IsValid(st) {
			return nil, part
		}
		out = append(out, st)
	}
	return out, ""
}

func (s *server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.ListFilter{
		Category: q.Get("category"),
		BatchID:  q.Get("batch_id"),
		Limit:    defaultLimit,
	}
	statuses, bad := parseStatuses(q.Get("status"))
	if bad != "" {
		badRequest(w, "unknown status '%s'", bad)
		return
	}
	f.Statuses = statuses
	if v := q.Get("finished_after"); v != "" {
		t, err := timefmt.Parse(v)
		if err != nil {
			badRequest(w, "finished_after must be an RFC 3339 time")
			return
		}
		f.FinishedAfter = &t
	}
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		if len(v) > maxQueryLen {
			badRequest(w, "q must be at most %d bytes", maxQueryLen)
			return
		}
		f.Query = v
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			badRequest(w, "limit must be a positive integer")
			return
		}
		f.Limit = min(n, maxLimit)
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			badRequest(w, "offset must be a non-negative integer")
			return
		}
		f.Offset = n
	}
	list, total, err := s.svc.List(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ListDTO{Jobs: NewJobDTOs(list), Total: total})
}

// retryBody is the optional body of the retry endpoints.
type retryBody struct {
	Overwrite bool                       `json:"overwrite"`
	Source    *service.ReplacementSource `json:"source"`
}

func (s *server) handleAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var (
			j   *store.Job
			err error
		)
		switch action {
		case "pause":
			j, err = s.svc.Pause(r.Context(), id)
		case "resume":
			j, err = s.svc.Resume(r.Context(), id)
		case "cancel":
			j, err = s.svc.Cancel(r.Context(), id)
		case "retry":
			var body retryBody
			if !s.decodeJSON(w, r, &body, true) {
				return
			}
			j, err = s.svc.Retry(r.Context(), id, service.RetryOptions{Overwrite: body.Overwrite, Source: body.Source})
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, NewJobDTO(j))
	}
}

func (s *server) handleBatchAction(action string) http.HandlerFunc {
	ev := map[string]jobs.Event{
		"pause": jobs.EventPause, "resume": jobs.EventResume,
		"cancel": jobs.EventCancel, "retry": jobs.EventRetry,
	}[action]
	return func(w http.ResponseWriter, r *http.Request) {
		var opts service.RetryOptions
		if action == "retry" {
			var body retryBody
			if !s.decodeJSON(w, r, &body, true) {
				return
			}
			if body.Source != nil {
				badRequest(w, "source replacement is only supported for individual jobs")
				return
			}
			opts.Overwrite = body.Overwrite
		}
		updated, err := s.svc.BatchAction(r.Context(), r.PathValue("batch_id"), ev, opts)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string][]JobDTO{"jobs": NewJobDTOs(updated)})
	}
}

// handleRetryMany retries every failed and/or cancelled job (?status=, default
// failed) the way a plain single retry does, skipping rejected ones.
func (s *server) handleRetryMany(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("status")
	if raw == "" {
		raw = string(jobs.StatusFailed)
	}
	statuses, bad := parseStatuses(raw)
	if bad != "" {
		badRequest(w, "unknown status '%s'", bad)
		return
	}
	for _, st := range statuses {
		if st != jobs.StatusFailed && st != jobs.StatusCancelled {
			badRequest(w, "status must be failed, cancelled or both, not '%s'", st)
			return
		}
	}
	if len(statuses) == 0 {
		statuses = []jobs.Status{jobs.StatusFailed}
	}
	updated, err := s.svc.RetryMany(r.Context(), statuses)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]JobDTO{"jobs": NewJobDTOs(updated)})
}

func (s *server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var opts service.DeleteOptions
	if v := r.URL.Query().Get("delete_file"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(w, "delete_file must be true or false")
			return
		}
		opts.DeleteFile = b
	}
	if err := s.svc.Delete(r.Context(), r.PathValue("id"), opts); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleDeleteMany(w http.ResponseWriter, r *http.Request) {
	statuses, bad := parseStatuses(r.URL.Query().Get("status"))
	if bad != "" {
		badRequest(w, "unknown status '%s'", bad)
		return
	}
	var before *time.Time
	if v := r.URL.Query().Get("finished_before"); v != "" {
		t, err := timefmt.Parse(v)
		if err != nil {
			badRequest(w, "finished_before must be an RFC 3339 time")
			return
		}
		before = &t
	}
	n, err := s.svc.DeleteMany(r.Context(), statuses, before)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

func (s *server) handlePrioritize(w http.ResponseWriter, r *http.Request) {
	j, err := s.svc.Prioritize(r.Context(), r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, NewJobDTO(j))
}

func (s *server) handlePrioritizeBatch(w http.ResponseWriter, r *http.Request) {
	moved, err := s.svc.PrioritizeBatch(r.Context(), r.PathValue("batch_id"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]JobDTO{"jobs": NewJobDTOs(moved)})
}
