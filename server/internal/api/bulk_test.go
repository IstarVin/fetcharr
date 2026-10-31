package api

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func TestRequestSizeCaps(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	headers := func(n int) map[string]any {
		h := map[string]any{}
		for i := range n {
			h["X-H"+itoa(i)] = "v"
		}
		return h
	}
	subs := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{"url": "https://s.example/" + itoa(i) + ".vtt", "lang": "en"}
		}
		return out
	}
	episodes := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = map[string]any{"episode": i + 1, "url": "https://cap.example/" + itoa(i)}
		}
		return out
	}
	single := func(k string, v any) map[string]any {
		b := map[string]any{"url": "https://cap.example/one", "category": "misc", "title": "T"}
		b[k] = v
		return b
	}
	batch := func(k string, v any) map[string]any {
		b := map[string]any{"category": "tv", "show_name": "S", "season": 1, "episodes": episodes(1)}
		b[k] = v
		return b
	}
	cases := []struct {
		name, path string
		body       map[string]any
		wantMsg    string
	}{
		{"url", "/api/jobs", single("url", "https://cap.example/"+long(2048)), "at most 2048 characters"},
		{"title", "/api/jobs", single("title", long(501)), "title must be at most 500"},
		{"show_name", "/api/jobs", single("show_name", long(501)), "show_name must be at most 500"},
		{"headers", "/api/jobs", single("headers", headers(51)), "at most 50 entries"},
		{"subtitles", "/api/jobs", single("subtitles", subs(21)), "at most 20 entries"},
		{"subtitle url", "/api/jobs", single("subtitles", []any{map[string]any{"url": "https://cap.example/" + long(2048)}}), "at most 2048 characters"},
		{"batch episodes", "/api/jobs/batch", batch("episodes", episodes(501)), "at most 500 episodes"},
		{"batch show_name", "/api/jobs/batch", batch("show_name", long(501)), "show_name must be at most 500"},
		{"batch headers", "/api/jobs/batch", batch("headers", headers(51)), "at most 50 entries"},
		{"batch episode title", "/api/jobs/batch", batch("episodes", []any{map[string]any{"episode": 1, "url": "https://cap.example/1", "title": long(501)}}), "title must be at most 500"},
		{"batch episode url", "/api/jobs/batch", batch("episodes", []any{map[string]any{"episode": 1, "url": "https://cap.example/" + long(2048)}}), "at most 2048 characters"},
		{"batch episode subtitles", "/api/jobs/batch", batch("episodes", []any{map[string]any{"episode": 1, "url": "https://cap.example/1", "subtitles": subs(21)}}), "at most 20 entries"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			r := e.want(e.request("POST", c.path, c.body), 400)
			if r.ErrCode(t) != "invalid_request" || !strings.Contains(string(r.Raw), c.wantMsg) {
				t.Errorf("want invalid_request containing %q, got %s", c.wantMsg, r.Raw)
			}
			if _, total, _ := e.svc.List(t.Context(), store.ListFilter{}); total != 0 {
				t.Errorf("nothing may be created, got %d jobs", total)
			}
		})
	}

	t.Run("exactly at the limits", func(t *testing.T) {
		e := newEnv(t)
		e.want(e.request("POST", "/api/jobs", map[string]any{
			"url": "https://cap.example/" + long(2048-len("https://cap.example/")), "category": "misc",
			"title": long(500), "headers": headers(50), "subtitles": subs(20)}), 201)
		e.want(e.request("POST", "/api/jobs/batch", map[string]any{
			"category": "tv", "show_name": long(500), "season": 1, "episodes": episodes(500)}), 201)
	})
}

func TestRetryMany(t *testing.T) {
	e := newEnv(t)
	failedA := e.seed(newJob("https://rm.example/fa", jobs.StatusFailed))
	failedB := e.seed(newJob("https://rm.example/fb", jobs.StatusFailed))
	cancelled := e.seed(newJob("https://rm.example/c", jobs.StatusCancelled))
	completed := e.seed(newJob("https://rm.example/done", jobs.StatusCompleted))
	// A failed job whose URL is active again: its retry is a duplicate.
	dup := e.seed(newJob("https://rm.example/dup", jobs.StatusFailed))
	e.seed(newJob("https://rm.example/dup", jobs.StatusPending))

	for _, bad := range []string{"completed", "pending", "failed,paused", "bogus", "failed,completed"} {
		if r := e.want(e.request("POST", "/api/jobs/retry?status="+bad, nil), 400); r.ErrCode(t) != "invalid_request" {
			t.Errorf("%s: %s", bad, r.Raw)
		}
	}
	statusOf := func(id string) string {
		return e.want(e.request("GET", "/api/jobs/"+id, nil), 200).Obj(t)["status"].(string)
	}
	if statusOf(failedA.ID) != "failed" {
		t.Fatal("a rejected request changed jobs")
	}

	// Default: failed only; the duplicate is skipped, not fatal.
	r := e.want(e.request("POST", "/api/jobs/retry", nil), 200).Obj(t)
	got := map[string]bool{}
	for _, j := range r["jobs"].([]any) {
		m := j.(map[string]any)
		got[m["id"].(string)] = true
		if m["status"] != "pending" {
			t.Errorf("retried job status = %v", m["status"])
		}
	}
	if len(got) != 2 || !got[failedA.ID] || !got[failedB.ID] {
		t.Errorf("retried %v, want exactly the two non-duplicate failed jobs", got)
	}
	if statusOf(dup.ID) != "failed" || statusOf(cancelled.ID) != "cancelled" || statusOf(completed.ID) != "completed" {
		t.Error("unselected or rejected jobs changed")
	}
	updates := 0
	for _, c := range e.pub.changes {
		if c.Kind == service.ChangeUpdated && (c.Job.ID == failedA.ID || c.Job.ID == failedB.ID) {
			updates++
		}
	}
	if updates != 2 {
		t.Errorf("published %d job_updated events for retried jobs, want 2", updates)
	}

	// The list form also takes cancelled jobs; the duplicate stays skipped.
	r = e.want(e.request("POST", "/api/jobs/retry?status=failed,cancelled", nil), 200).Obj(t)
	if jl := r["jobs"].([]any); len(jl) != 1 || jl[0].(map[string]any)["id"] != cancelled.ID {
		t.Errorf("retry failed,cancelled: %v", r)
	}
	// Nothing left: [] not null.
	r = e.want(e.request("POST", "/api/jobs/retry?status=cancelled", nil), 200).Obj(t)
	if jl, ok := r["jobs"].([]any); !ok || len(jl) != 0 {
		t.Errorf("empty result must be []: %s", e.bodies[len(e.bodies)-1])
	}
	// The per-job route is unaffected.
	e.want(e.request("POST", "/api/jobs/"+dup.ID+"/retry", nil), 409)
}

func TestDeleteManyFinishedBefore(t *testing.T) {
	e := newEnv(t)
	at := func(url string, st jobs.Status, ago time.Duration) *store.Job {
		j := newJob(url, st)
		f := time.Now().UTC().Add(-ago)
		j.FinishedAt = &f
		return e.seed(j)
	}
	old := at("https://fb.example/old", jobs.StatusFailed, 48*time.Hour)
	oldDone := at("https://fb.example/olddone", jobs.StatusCompleted, 72*time.Hour)
	recent := at("https://fb.example/recent", jobs.StatusFailed, time.Hour)
	active := e.seed(newJob("https://fb.example/active", jobs.StatusPending))

	for _, bad := range []string{"yesterday", "2026-01-01", "1700000000"} {
		if r := e.want(e.request("DELETE", "/api/jobs?finished_before="+bad, nil), 400); r.ErrCode(t) != "invalid_request" {
			t.Errorf("%s: %s", bad, r.Raw)
		}
	}
	if _, total, _ := e.svc.List(t.Context(), store.ListFilter{}); total != 4 {
		t.Fatalf("a rejected clear deleted jobs: total = %d", total)
	}
	cutoff := url.QueryEscape(time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339))
	r := e.want(e.request("DELETE", "/api/jobs?status=failed&finished_before="+cutoff, nil), 200).Obj(t)
	assertEqualJSON(t, "deleted", map[string]any{"deleted": float64(1)}, r)
	e.want(e.request("GET", "/api/jobs/"+old.ID, nil), 404)
	e.want(e.request("GET", "/api/jobs/"+oldDone.ID, nil), 200)
	r = e.want(e.request("DELETE", "/api/jobs?finished_before="+cutoff, nil), 200).Obj(t)
	assertEqualJSON(t, "deleted", map[string]any{"deleted": float64(1)}, r)
	e.want(e.request("GET", "/api/jobs/"+recent.ID, nil), 200)
	e.want(e.request("GET", "/api/jobs/"+active.ID, nil), 200)
}

// A failed job that can never be retried (its category was removed) must not
// block the others: it is skipped like any other rejected retry.
func TestRetryManySkipsInvalidJob(t *testing.T) {
	e := newEnv(t)
	bad := newJob("https://rm.example/bad", jobs.StatusFailed)
	bad.Category = "removed-category"
	e.seed(bad)
	good := e.seed(newJob("https://rm.example/good", jobs.StatusFailed))

	r := e.want(e.request("POST", "/api/jobs/retry", nil), 200).Obj(t)
	jl := r["jobs"].([]any)
	if len(jl) != 1 || jl[0].(map[string]any)["id"] != good.ID {
		t.Errorf("retried %v, want only the valid failed job", r)
	}
	if got := e.want(e.request("GET", "/api/jobs/"+bad.ID, nil), 200).Obj(t)["status"]; got != "failed" {
		t.Errorf("invalid job status = %v, want failed", got)
	}
}

func TestBatchRetrySkipsInvalidMember(t *testing.T) {
	e := newEnv(t)
	bad := newJob("https://br.example/bad", jobs.StatusFailed)
	bad.BatchID = store.Ptr("b1")
	bad.Category = "removed-category"
	e.seed(bad)
	g := newJob("https://br.example/good", jobs.StatusFailed)
	g.BatchID = store.Ptr("b1")
	good := e.seed(g)

	r := e.want(e.request("POST", "/api/batches/b1/retry", nil), 200).Obj(t)
	jl := r["jobs"].([]any)
	if len(jl) != 1 || jl[0].(map[string]any)["id"] != good.ID {
		t.Errorf("retried %v, want only the valid member", r)
	}
	if got := e.want(e.request("GET", "/api/jobs/"+bad.ID, nil), 200).Obj(t)["status"]; got != "failed" {
		t.Errorf("invalid member status = %v, want failed", got)
	}
}
