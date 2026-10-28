package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func TestAuth(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		name, authorization string
		want                int
	}{
		{"missing", "", 401},
		{"wrong", "Bearer nope", 401},
		{"prefix of token", "Bearer " + testToken[:5], 401},
		{"token plus suffix", "Bearer " + testToken + "x", 401},
		{"wrong scheme", "Basic " + testToken, 401},
		{"no scheme", testToken, 401},
		{"empty bearer", "Bearer ", 401},
		{"ok", "Bearer " + testToken, 200},
		{"ok lowercase scheme", "bearer " + testToken, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.requestWith("GET", "/api/jobs", nil, c.authorization)
			if r.Code != c.want {
				t.Fatalf("status = %d, want %d", r.Code, c.want)
			}
			if c.want == 401 && r.ErrCode(t) != "unauthorized" {
				t.Errorf("code = %s", r.Raw)
			}
		})
	}
	// Every endpoint except health is protected, including unknown routes.
	for _, ep := range []struct{ m, p string }{
		{"GET", "/api/info"}, {"POST", "/api/jobs"}, {"POST", "/api/jobs/batch"}, {"DELETE", "/api/jobs"},
		{"GET", "/api/jobs/x"}, {"POST", "/api/jobs/x/pause"}, {"POST", "/api/batches/x/cancel"},
		{"GET", "/api/system/ytdlp/check"}, {"POST", "/api/system/ytdlp/update"},
	} {
		if r := e.requestWith(ep.m, ep.p, nil, ""); r.Code != 401 {
			t.Errorf("%s %s without token = %d, want 401", ep.m, ep.p, r.Code)
		}
	}
}

func TestHealthNeedsNoToken(t *testing.T) {
	e := newEnv(t)
	r := e.want(e.requestWith("GET", "/api/health", nil, ""), 200)
	assertEqualJSON(t, "health", map[string]any{"status": "ok"}, r.JSON(t))
}

func TestUnknownRouteAndMethodUseEnvelope(t *testing.T) {
	e := newEnv(t)
	r := e.want(e.request("GET", "/api/nope", nil), 404)
	if r.ErrCode(t) != "not_found" {
		t.Errorf("body %s", r.Raw)
	}
	r = e.want(e.request("PUT", "/api/jobs", nil), 405)
	if r.ErrCode(t) != "invalid_request" || !strings.Contains(r.Header.Get("Allow"), "GET") {
		t.Errorf("405: allow=%q body %s", r.Header.Get("Allow"), r.Raw)
	}
	e.want(e.request("GET", "/api/jobs/x/pause", nil), 405)
	r = e.want(e.request("POST", "/api/system/settings", nil), 405)
	if got := r.Header.Get("Allow"); got != "GET, HEAD, PUT" {
		t.Errorf("settings Allow = %q, want GET, HEAD, PUT", got)
	}
}

func TestHeadersNeverInAnyResponse(t *testing.T) {
	e := newEnv(t)
	// Created through the API with a secret header...
	body := map[string]any{
		"url": "https://x.example/secret", "category": "misc", "title": "S",
		"headers": map[string]string{"Authorization": secretHeaderValue, "Cookie": secretHeaderValue},
	}
	id := e.want(e.request("POST", "/api/jobs", body), 201).ID(t)
	// ...and seeded with one, in every state.
	for i, st := range jobs.AllStatuses() {
		e.seed(newJob(fmt.Sprintf("https://x.example/%d", i), st))
	}
	// The stored job really has the header (so the test is meaningful).
	j, err := e.st.Get(t.Context(), id)
	if err != nil || j.Headers["Cookie"] != secretHeaderValue {
		t.Fatalf("headers not stored: %v %v", j, err)
	}
	for _, p := range []string{"/api/jobs", "/api/jobs/" + id, "/api/jobs?limit=500&status=pending,failed", "/api/info"} {
		e.want(e.request("GET", p, nil), 200)
	}
	e.want(e.request("POST", "/api/jobs/"+id+"/pause", nil), 200)
	e.want(e.request("POST", "/api/jobs/"+id+"/resume", nil), 200)
	e.want(e.request("POST", "/api/jobs/"+id+"/cancel", nil), 200)
	e.want(e.request("POST", "/api/jobs/"+id+"/retry", `{"overwrite":true}`), 200)
	// Cleanup in newEnv greps all recorded bodies and logs; also check the DTO.
	b := fmt.Sprintf("%+v", NewJobDTO(j))
	if strings.Contains(b, secretHeaderValue) {
		t.Error("JobDTO leaks headers")
	}
}

func TestCreateValidation(t *testing.T) {
	cases := []struct {
		name string
		body any
		want int
		code string
	}{
		{"movie ok", map[string]any{"url": "https://a.example/m", "category": "movies", "title": "M", "year": 2024}, 201, ""},
		{"flat ok", map[string]any{"url": "http://a.example/f", "category": "misc", "title": "F"}, 201, ""},
		{"tv ok no title", map[string]any{"url": "https://a.example/t", "category": "tv", "show_name": "S", "season": 0, "episode": 0}, 201, ""},
		{"missing url", map[string]any{"category": "misc", "title": "F"}, 400, "invalid_request"},
		{"url not http", map[string]any{"url": "ftp://a.example/f", "category": "misc", "title": "F"}, 400, "invalid_request"},
		{"url no scheme", map[string]any{"url": "a.example/f", "category": "misc", "title": "F"}, 400, "invalid_request"},
		{"url no host", map[string]any{"url": "http://", "category": "misc", "title": "F"}, 400, "invalid_request"},
		{"unknown category", map[string]any{"url": "https://a.example/x", "category": "nope", "title": "F"}, 400, "invalid_request"},
		{"missing category", map[string]any{"url": "https://a.example/x", "title": "F"}, 400, "invalid_request"},
		{"missing title", map[string]any{"url": "https://a.example/x", "category": "movies"}, 400, "invalid_request"},
		{"title empty after sanitizing", map[string]any{"url": "https://a.example/x", "category": "movies", "title": "<>:\"/\\|?*"}, 400, "invalid_request"},
		{"tv missing show", map[string]any{"url": "https://a.example/x", "category": "tv", "season": 1, "episode": 1}, 400, "invalid_request"},
		{"tv missing season", map[string]any{"url": "https://a.example/x", "category": "tv", "show_name": "S", "episode": 1}, 400, "invalid_request"},
		{"tv missing episode", map[string]any{"url": "https://a.example/x", "category": "tv", "show_name": "S", "season": 1, "title": "T"}, 400, "invalid_request"},
		{"negative season", map[string]any{"url": "https://a.example/x", "category": "tv", "show_name": "S", "season": -1, "episode": 1}, 400, "invalid_request"},
		{"negative episode", map[string]any{"url": "https://a.example/x", "category": "tv", "show_name": "S", "season": 1, "episode": -1}, 400, "invalid_request"},
		{"year not 4 digits", map[string]any{"url": "https://a.example/x", "category": "movies", "title": "M", "year": 99}, 400, "invalid_request"},
		{"year 5 digits", map[string]any{"url": "https://a.example/x", "category": "movies", "title": "M", "year": 20240}, 400, "invalid_request"},
		{"fractional season", `{"url":"https://a.example/x","category":"tv","show_name":"S","season":1.5,"episode":1}`, 400, "invalid_request"},
		{"string season", `{"url":"https://a.example/x","category":"tv","show_name":"S","season":"1","episode":1}`, 400, "invalid_request"},
		{"subtitle missing url", map[string]any{"url": "https://a.example/x", "category": "misc", "title": "F", "subtitles": []any{map[string]any{"lang": "en"}}}, 400, "invalid_request"},
		{"subtitle url not http", map[string]any{"url": "https://a.example/x", "category": "misc", "title": "F", "subtitles": []any{map[string]any{"url": "file:///etc/passwd"}}}, 400, "invalid_request"},
		{"header name with colon", map[string]any{"url": "https://a.example/x", "category": "misc", "title": "F", "headers": map[string]string{"A:B": "c"}}, 400, "invalid_request"},
		{"header value with newline", map[string]any{"url": "https://a.example/x", "category": "misc", "title": "F", "headers": map[string]string{"A": "c\nd: e"}}, 400, "invalid_request"},
		{"unknown field rejected", `{"url":"https://a.example/x","category":"misc","title":"F","bogus":1}`, 400, "invalid_request"},
		{"not json", `{nope`, 400, "invalid_request"},
		{"empty body", ``, 400, "invalid_request"},
		{"trailing data", `{"url":"https://a.example/x","category":"misc","title":"F"} {}`, 400, "invalid_request"},
		{"json array", `[]`, 400, "invalid_request"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			r := e.request("POST", "/api/jobs", c.body)
			if r.Code != c.want {
				t.Fatalf("status = %d, want %d; body %s", r.Code, c.want, r.Raw)
			}
			if c.code != "" && r.ErrCode(t) != c.code {
				t.Errorf("code = %s", r.Raw)
			}
			if c.want != 201 {
				if _, total, _ := e.svc.List(t.Context(), store.ListFilter{}); total != 0 {
					t.Errorf("a rejected request created %d job(s)", total)
				}
			}
		})
	}
}

func TestCreateNormalisation(t *testing.T) {
	e := newEnv(t)
	// Fields that do not apply to the layout are null; a missing subtitle lang is "und".
	r := e.want(e.request("POST", "/api/jobs", map[string]any{
		"url": "https://a.example/n", "category": "misc", "title": "  Talk  ",
		"show_name": "ignored", "season": 3, "episode": 4, "year": 2020,
		"subtitles": []any{map[string]any{"url": "https://a.example/n.vtt"}},
	}), 201).Obj(t)
	for _, k := range []string{"show_name", "season", "episode", "year"} {
		if r[k] != nil {
			t.Errorf("%s = %v, want null for a flat category", k, r[k])
		}
	}
	if r["title"] != "Talk" {
		t.Errorf("title = %q", r["title"])
	}
	sub := r["subtitles"].([]any)[0].(map[string]any)
	if sub["lang"] != "und" {
		t.Errorf("lang = %v, want und", sub["lang"])
	}
	// A tv job without a title has title null.
	r = e.want(e.request("POST", "/api/jobs", map[string]any{
		"url": "https://a.example/tv", "category": "tv", "show_name": "S", "season": 1, "episode": 2,
	}), 201).Obj(t)
	if r["title"] != nil {
		t.Errorf("title = %v, want null", r["title"])
	}
}

func TestBodyTooLarge(t *testing.T) {
	e := newEnv(t)
	e.h = New(Options{Config: e.cfg, Service: e.svc, MaxBodyBytes: 64, Logger: e.logsLogger()})
	r := e.request("POST", "/api/jobs", `{"url":"https://a.example/x","category":"misc","title":"`+strings.Repeat("a", 200)+`"}`)
	if r.Code != 400 || r.ErrCode(t) != "invalid_request" {
		t.Fatalf("%d %s", r.Code, r.Raw)
	}
}

func TestDuplicate(t *testing.T) {
	e := newEnv(t)
	body := map[string]any{"url": "https://a.example/d", "category": "misc", "title": "D"}
	id := e.want(e.request("POST", "/api/jobs", body), 201).ID(t)
	for _, st := range []string{"first", "paused"} {
		if st == "paused" {
			e.want(e.request("POST", "/api/jobs/"+id+"/pause", nil), 200)
		}
		if r := e.want(e.request("POST", "/api/jobs", body), 409); r.ErrCode(t) != "duplicate" {
			t.Errorf("%s: %s", st, r.Raw)
		}
	}
	// After cancelling, the URL is free again.
	e.want(e.request("POST", "/api/jobs/"+id+"/cancel", nil), 200)
	id2 := e.want(e.request("POST", "/api/jobs", body), 201).ID(t)
	// Retrying the cancelled job now collides with the new active job.
	r := e.want(e.request("POST", "/api/jobs/"+id+"/retry", nil), 409)
	if r.ErrCode(t) != "duplicate" {
		t.Errorf("retry collision: %s", r.Raw)
	}
	got := e.want(e.request("GET", "/api/jobs/"+id, nil), 200).Obj(t)
	if got["status"] != "cancelled" {
		t.Errorf("status after failed retry = %v, want cancelled", got["status"])
	}
	// Same for a failed job.
	e.want(e.request("POST", "/api/jobs/"+id2+"/cancel", nil), 200)
	failed := e.seed(newJob("https://a.example/d", jobs.StatusFailed))
	e.want(e.request("POST", "/api/jobs", body), 201)
	if r := e.want(e.request("POST", "/api/jobs/"+failed.ID+"/retry", nil), 409); r.ErrCode(t) != "duplicate" {
		t.Errorf("failed retry collision: %s", r.Raw)
	}
}

func TestBatchValidationAndSkips(t *testing.T) {
	ep := func(n any, u string) map[string]any { return map[string]any{"episode": n, "url": u, "title": "E"} }
	base := func() map[string]any {
		return map[string]any{
			"category": "tv", "show_name": "Show", "season": 1, "year": 2020,
			"episodes": []any{ep(2, "https://b.example/2"), ep(1, "https://b.example/1")},
		}
	}
	with := func(k string, v any) map[string]any { b := base(); b[k] = v; return b }
	without := func(k string) map[string]any { b := base(); delete(b, k); return b }
	bad := map[string]map[string]any{
		"non-tv category":      with("category", "movies"),
		"unknown category":     with("category", "zzz"),
		"missing show_name":    without("show_name"),
		"missing season":       without("season"),
		"negative season":      with("season", -1),
		"missing episodes":     without("episodes"),
		"empty episodes":       with("episodes", []any{}),
		"episode no number":    with("episodes", []any{map[string]any{"url": "https://b.example/9"}}),
		"episode no url":       with("episodes", []any{map[string]any{"episode": 1}}),
		"episode url rtsp":     with("episodes", []any{ep(1, "rtsp://b.example/9")}),
		"episode neg number":   with("episodes", []any{ep(-1, "https://b.example/9")}),
		"episode bad subtitle": with("episodes", []any{map[string]any{"episode": 1, "url": "https://b.example/9", "subtitles": []any{map[string]any{"url": "ftp://x/y"}}}}),
		"one bad of two":       with("episodes", []any{ep(1, "https://b.example/1"), ep(2, "gopher://b.example/2")}),
	}
	for name, body := range bad {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			r := e.want(e.request("POST", "/api/jobs/batch", body), 400)
			if r.ErrCode(t) != "invalid_request" {
				t.Errorf("%s", r.Raw)
			}
			if _, total, _ := e.svc.List(t.Context(), store.ListFilter{}); total != 0 {
				t.Errorf("nothing may be created, got %d jobs", total)
			}
		})
	}

	t.Run("ascending order, queue order", func(t *testing.T) {
		e := newEnv(t)
		r := e.want(e.request("POST", "/api/jobs/batch", base()), 201).Obj(t)
		js := r["jobs"].([]any)
		if js[0].(map[string]any)["episode"] != float64(1) || js[1].(map[string]any)["episode"] != float64(2) {
			t.Errorf("not ascending: %s", e.bodies[len(e.bodies)-1])
		}
		pend, err := e.st.PendingJobs(t.Context(), 0)
		if err != nil || len(pend) != 2 || *pend[0].Episode != 1 {
			t.Errorf("dispatch order wrong: %v %v", pend, err)
		}
	})
	t.Run("all skipped is 409 duplicate", func(t *testing.T) {
		e := newEnv(t)
		e.want(e.request("POST", "/api/jobs/batch", base()), 201)
		r := e.want(e.request("POST", "/api/jobs/batch", base()), 409)
		if r.ErrCode(t) != "duplicate" {
			t.Errorf("%s", r.Raw)
		}
	})
	t.Run("some skipped", func(t *testing.T) {
		e := newEnv(t)
		e.seed(newJobWith("https://b.example/2", "tv"))
		r := e.want(e.request("POST", "/api/jobs/batch", base()), 201).Obj(t)
		assertEqualJSON(t, "skipped", []any{map[string]any{"episode": float64(2), "reason": "duplicate"}}, r["skipped"])
		if len(r["jobs"].([]any)) != 1 {
			t.Errorf("%v", r["jobs"])
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		e := newEnv(t)
		e.want(e.request("POST", "/api/jobs/batch", with("bogus", 1)), 400)
	})
	t.Run("in library skipped", func(t *testing.T) {
		e := newEnv(t)
		writeLibraryFile(t, e, "tv", "Show (2020)/Season 01/Show - S01E02 - E.mp4")
		r := e.want(e.request("POST", "/api/jobs/batch", base()), 201).Obj(t)
		assertEqualJSON(t, "skipped", []any{map[string]any{"episode": float64(2), "reason": "already_exists"}}, r["skipped"])
		if js := r["jobs"].([]any); len(js) != 1 || js[0].(map[string]any)["episode"] != float64(1) {
			t.Errorf("%v", r["jobs"])
		}
	})
	t.Run("all skipped, some in library, is 409 already_exists", func(t *testing.T) {
		e := newEnv(t)
		e.seed(newJobWith("https://b.example/1", "tv"))
		writeLibraryFile(t, e, "tv", "Show (2020)/Season 01/Show - S01E02 - E.mkv")
		r := e.want(e.request("POST", "/api/jobs/batch", base()), 409)
		if r.ErrCode(t) != "already_exists" {
			t.Errorf("%s", r.Raw)
		}
		// overwrite creates the episode in the library; the active url stays a duplicate.
		o := e.want(e.request("POST", "/api/jobs/batch", with("overwrite", true)), 201).Obj(t)
		assertEqualJSON(t, "skipped", []any{map[string]any{"episode": float64(1), "reason": "duplicate"}}, o["skipped"])
		listed, _, err := e.svc.List(t.Context(), storeFilterBatch(o["batch_id"].(string)))
		if err != nil || len(listed) != 1 || !listed[0].Overwrite {
			t.Errorf("listed = %v, %v; want one overwrite job", listed, err)
		}
	})
}

func TestCreateAlreadyExists(t *testing.T) {
	body := func(overwrite bool) map[string]any {
		b := map[string]any{"url": "https://m.example/dune", "category": "movies", "title": "Dune", "year": 2021}
		if overwrite {
			b["overwrite"] = true
		}
		return b
	}
	e := newEnv(t)
	path := writeLibraryFile(t, e, "movies", "Dune (2021)/Dune (2021).webm")
	r := e.want(e.request("POST", "/api/jobs", body(false)), 409)
	if r.ErrCode(t) != "already_exists" || !strings.Contains(string(r.Raw), filepath.Base(path)) {
		t.Errorf("%s", r.Raw)
	}
	if _, total, _ := e.svc.List(t.Context(), store.ListFilter{}); total != 0 {
		t.Errorf("nothing may be created, got %d jobs", total)
	}
	id := e.want(e.request("POST", "/api/jobs", body(true)), 201).ID(t)
	j, err := e.st.Get(t.Context(), id)
	if err != nil || !j.Overwrite {
		t.Errorf("job = %+v, %v; want overwrite", j, err)
	}
	// Another layout target is unaffected.
	e.want(e.request("POST", "/api/jobs", map[string]any{"url": "https://m.example/other", "category": "movies", "title": "Dune", "year": 1984}), 201)
}

func TestListFilters(t *testing.T) {
	e := newEnv(t)
	base := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	mk := func(url, cat string, st jobs.Status, batch string, created time.Time, finished *time.Time) *store.Job {
		j := newJob(url, st)
		j.Category = cat
		j.CreatedAt, j.QueuedAt = created, created
		j.FinishedAt = finished
		if batch != "" {
			j.BatchID = &batch
		}
		return e.seed(j)
	}
	t1, t2, t3 := base.Add(1*time.Minute), base.Add(2*time.Minute), base.Add(3*time.Minute)
	a := mk("https://l.example/a", "misc", jobs.StatusPending, "", base, nil)
	b := mk("https://l.example/b", "movies", jobs.StatusCompleted, "", base.Add(time.Second), &t1)
	c := mk("https://l.example/c", "misc", jobs.StatusFailed, "batch-1", base.Add(2*time.Second), &t2)
	d := mk("https://l.example/d", "tv", jobs.StatusCancelled, "batch-1", base.Add(3*time.Second), &t3)
	f := mk("https://l.example/f", "misc", jobs.StatusPaused, "batch-2", base.Add(4*time.Second), nil)

	ids := func(path string) []string {
		t.Helper()
		r := e.want(e.request("GET", path, nil), 200).Obj(t)
		var out []string
		for _, j := range r["jobs"].([]any) {
			out = append(out, j.(map[string]any)["id"].(string))
		}
		return out
	}
	eq := func(path string, want ...*store.Job) {
		t.Helper()
		got := ids(path)
		if len(got) != len(want) {
			t.Errorf("%s: got %d jobs %v, want %d", path, len(got), got, len(want))
			return
		}
		for i := range want {
			if got[i] != want[i].ID {
				t.Errorf("%s: position %d = %s, want %s", path, i, got[i], want[i].ID)
			}
		}
	}
	eq("/api/jobs", f, d, c, b, a) // created_at DESC
	eq("/api/jobs?status=pending", a)
	eq("/api/jobs?status=failed,cancelled", d, c)
	eq("/api/jobs?status=failed,%20paused", f, c)
	eq("/api/jobs?category=misc", f, c, a)
	eq("/api/jobs?category=misc&status=pending,paused", f, a)
	eq("/api/jobs?batch_id=batch-1", d, c)
	eq("/api/jobs?batch_id=nope")
	eq("/api/jobs?finished_after="+url.QueryEscape("2026-10-14T12:01:00.000Z"), d, c) // strictly later
	eq("/api/jobs?finished_after="+url.QueryEscape("2026-10-14T12:01:00.999Z"), d, c)
	eq("/api/jobs?finished_after="+url.QueryEscape("2026-10-14T12:00:59.999Z"), d, c, b)
	eq("/api/jobs?finished_after="+url.QueryEscape("2026-10-14T13:00:00+01:00"), d, c, b) // offsets accepted
	eq("/api/jobs?limit=2", f, d)
	eq("/api/jobs?limit=2&offset=2", c, b)
	eq("/api/jobs?offset=4", a)
	eq("/api/jobs?offset=99")

	r := e.want(e.request("GET", "/api/jobs?limit=2", nil), 200).Obj(t)
	if r["total"] != float64(5) {
		t.Errorf("total = %v, want 5 (ignores limit)", r["total"])
	}
	r = e.want(e.request("GET", "/api/jobs?status=nothing-here", nil), 400).Obj(t)
	_ = r
	if r := e.want(e.request("GET", "/api/jobs?category=zzz", nil), 200).Obj(t); len(r["jobs"].([]any)) != 0 || r["total"] != float64(0) {
		t.Errorf("unknown category should list nothing: %v", r)
	}
	for _, q := range []string{"limit=0", "limit=-1", "limit=abc", "offset=-1", "offset=x", "finished_after=yesterday", "finished_after=2026-10-14"} {
		if r := e.want(e.request("GET", "/api/jobs?"+q, nil), 400); r.ErrCode(t) != "invalid_request" {
			t.Errorf("%s: %s", q, r.Raw)
		}
	}
}

func TestListPaginationDefaultsAndMax(t *testing.T) {
	e := newEnv(t)
	var batch []*store.Job
	for i := range 520 {
		batch = append(batch, newJob(fmt.Sprintf("https://p.example/%d", i), jobs.StatusPending))
	}
	if _, err := e.st.InsertBatch(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	count := func(q string) (int, float64) {
		r := e.want(e.request("GET", "/api/jobs"+q, nil), 200).Obj(t)
		return len(r["jobs"].([]any)), r["total"].(float64)
	}
	if n, total := count(""); n != 50 || total != 520 {
		t.Errorf("default page = %d of %v, want 50 of 520", n, total)
	}
	if n, _ := count("?limit=500"); n != 500 {
		t.Errorf("limit=500 -> %d", n)
	}
	if n, _ := count("?limit=100000"); n != 500 {
		t.Errorf("limit above max must clamp to 500, got %d", n)
	}
}

func TestLifecycleAndInvalidState(t *testing.T) {
	e := newEnv(t)
	act := func(id, action string, want int) resp {
		t.Helper()
		return e.want(e.request("POST", "/api/jobs/"+id+"/"+action, nil), want)
	}
	status := func(id string) string {
		return e.want(e.request("GET", "/api/jobs/"+id, nil), 200).Obj(t)["status"].(string)
	}
	// Full happy path through the API.
	id := e.want(e.request("POST", "/api/jobs", map[string]any{"url": "https://a.example/life", "category": "misc", "title": "L"}), 201).ID(t)
	r := act(id, "pause", 200).Obj(t)
	if r["status"] != "paused" {
		t.Errorf("pause -> %v", r["status"])
	}
	createdQueued := r["queued_at"]
	time.Sleep(3 * time.Millisecond)
	r = act(id, "resume", 200).Obj(t)
	if r["status"] != "pending" || r["queued_at"].(string) <= createdQueued.(string) {
		t.Errorf("resume must go to the back of the queue: %v", r)
	}
	r = act(id, "cancel", 200).Obj(t)
	if r["status"] != "cancelled" || r["finished_at"] == nil {
		t.Errorf("cancel -> %v", r)
	}
	r = act(id, "retry", 200).Obj(t)
	if r["status"] != "pending" || r["finished_at"] != nil || r["progress"] != nil || r["error_code"] != nil {
		t.Errorf("retry -> %v", r)
	}
	act(id, "cancel", 200)

	// invalid_state matrix: every (status, action) pair outside spec §2.4.
	allowed := map[jobs.Status]map[string]bool{
		jobs.StatusPending:     {"pause": true, "cancel": true},
		jobs.StatusDownloading: {"pause": true, "cancel": true},
		jobs.StatusProcessing:  {"cancel": true},
		jobs.StatusPaused:      {"resume": true, "cancel": true},
		jobs.StatusCompleted:   {},
		jobs.StatusFailed:      {"retry": true},
		jobs.StatusCancelled:   {"retry": true},
	}
	for _, st := range jobs.AllStatuses() {
		for _, action := range []string{"pause", "resume", "cancel", "retry"} {
			t.Run(string(st)+"/"+action, func(t *testing.T) {
				j := e.seed(newJob(fmt.Sprintf("https://m.example/%s-%s", st, action), st))
				if allowed[st][action] {
					e.want(e.request("POST", "/api/jobs/"+j.ID+"/"+action, nil), 200)
					return
				}
				r := e.want(e.request("POST", "/api/jobs/"+j.ID+"/"+action, nil), 409)
				if r.ErrCode(t) != "invalid_state" {
					t.Errorf("%s", r.Raw)
				}
				if got := status(j.ID); got != string(st) {
					t.Errorf("a refused action changed the status to %s", got)
				}
			})
		}
	}

	t.Run("cancel while placing is refused", func(t *testing.T) {
		j := newJob("https://m.example/placing", jobs.StatusProcessing)
		j.Placing = true
		e.seed(j)
		r := act(j.ID, "cancel", 409)
		if r.ErrCode(t) != "invalid_state" || status(j.ID) != "processing" {
			t.Errorf("%s", r.Raw)
		}
	})
	t.Run("retry clears error warnings progress and sets overwrite", func(t *testing.T) {
		j := newJob("https://m.example/failed", jobs.StatusFailed)
		j.ErrorCode, j.ErrorMessage = store.Ptr("download_failed"), store.Ptr("boom")
		j.Warnings = []string{"w"}
		j.Progress = &store.Progress{Percent: store.Ptr(12.5)}
		e.seed(j)
		r := e.want(e.request("POST", "/api/jobs/"+j.ID+"/retry", `{"overwrite":true}`), 200).Obj(t)
		if r["status"] != "pending" || r["error_code"] != nil || r["error_message"] != nil || r["progress"] != nil ||
			r["started_at"] != nil || r["finished_at"] != nil || len(r["warnings"].([]any)) != 0 {
			t.Errorf("retry did not reset: %v", r)
		}
		got, _ := e.st.Get(t.Context(), j.ID)
		if !got.Overwrite {
			t.Error("overwrite not stored")
		}
		// Unknown field in the retry body is rejected.
		k := e.seed(newJob("https://m.example/failed2", jobs.StatusFailed))
		e.want(e.request("POST", "/api/jobs/"+k.ID+"/retry", `{"bogus":1}`), 400)
		e.want(e.request("POST", "/api/jobs/"+k.ID+"/retry", `{"overwrite":"yes"}`), 400)
		e.want(e.request("POST", "/api/jobs/"+k.ID+"/retry", `{}`), 200)
	})
	t.Run("pause keeps progress", func(t *testing.T) {
		j := newJob("https://m.example/prog", jobs.StatusDownloading)
		j.Progress = &store.Progress{Percent: store.Ptr(42.5), DownloadedBytes: store.Ptr(int64(7))}
		e.seed(j)
		r := act(j.ID, "pause", 200).Obj(t)
		if r["progress"].(map[string]any)["percent"] != 42.5 {
			t.Errorf("%v", r["progress"])
		}
	})
}

func TestNotFound(t *testing.T) {
	e := newEnv(t)
	ghost := "00000000-0000-4000-8000-000000000000"
	for _, c := range []struct{ m, p string }{
		{"GET", "/api/jobs/" + ghost},
		{"POST", "/api/jobs/" + ghost + "/pause"},
		{"POST", "/api/jobs/" + ghost + "/resume"},
		{"POST", "/api/jobs/" + ghost + "/cancel"},
		{"POST", "/api/jobs/" + ghost + "/retry"},
		{"DELETE", "/api/jobs/" + ghost},
		{"POST", "/api/batches/" + ghost + "/pause"},
		{"POST", "/api/batches/" + ghost + "/resume"},
		{"POST", "/api/batches/" + ghost + "/cancel"},
		{"POST", "/api/batches/" + ghost + "/retry"},
	} {
		r := e.want(e.request(c.m, c.p, nil), 404)
		if r.ErrCode(t) != "not_found" {
			t.Errorf("%s %s: %s", c.m, c.p, r.Raw)
		}
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	for _, st := range jobs.ActiveStatuses() {
		j := e.seed(newJob("https://d.example/"+string(st), st))
		r := e.want(e.request("DELETE", "/api/jobs/"+j.ID, nil), 409)
		if r.ErrCode(t) != "invalid_state" {
			t.Errorf("%s: %s", st, r.Raw)
		}
		e.want(e.request("GET", "/api/jobs/"+j.ID, nil), 200)
	}
	for _, st := range jobs.FinishedStatuses() {
		j := e.seed(newJob("https://d.example/"+string(st), st))
		r := e.want(e.request("DELETE", "/api/jobs/"+j.ID, nil), 204)
		if len(r.Raw) != 0 {
			t.Errorf("204 must have no body: %q", r.Raw)
		}
		e.want(e.request("GET", "/api/jobs/"+j.ID, nil), 404)
	}
	j := e.seed(newJob("https://d.example/df", jobs.StatusCompleted))
	e.want(e.request("DELETE", "/api/jobs/"+j.ID+"?delete_file=maybe", nil), 400)
	e.want(e.request("DELETE", "/api/jobs/"+j.ID+"?delete_file=true", nil), 204)
}

func TestDeleteMany(t *testing.T) {
	e := newEnv(t)
	seedAll := func() map[jobs.Status]*store.Job {
		out := map[jobs.Status]*store.Job{}
		for _, st := range jobs.AllStatuses() {
			out[st] = e.seed(newJob("https://dm.example/"+string(st)+"-"+itoa(len(e.bodies)), st))
		}
		return out
	}
	all := seedAll()
	for _, bad := range []string{"pending", "downloading", "processing", "paused", "completed,paused", "bogus"} {
		if r := e.want(e.request("DELETE", "/api/jobs?status="+bad, nil), 400); r.ErrCode(t) != "invalid_request" {
			t.Errorf("%s: %s", bad, r.Raw)
		}
	}
	if _, total, _ := e.svc.List(t.Context(), store.ListFilter{}); total != 7 {
		t.Fatalf("a rejected clear deleted jobs: total = %d", total)
	}
	r := e.want(e.request("DELETE", "/api/jobs?status=failed", nil), 200).Obj(t)
	assertEqualJSON(t, "deleted", map[string]any{"deleted": float64(1)}, r)
	e.want(e.request("GET", "/api/jobs/"+all[jobs.StatusFailed].ID, nil), 404)
	r = e.want(e.request("DELETE", "/api/jobs", nil), 200).Obj(t) // default: all finished
	if r["deleted"] != float64(2) {
		t.Errorf("default clear deleted %v, want 2 (completed, cancelled)", r["deleted"])
	}
	for _, st := range jobs.ActiveStatuses() {
		e.want(e.request("GET", "/api/jobs/"+all[st].ID, nil), 200)
	}
	r = e.want(e.request("DELETE", "/api/jobs", nil), 200).Obj(t)
	if r["deleted"] != float64(0) {
		t.Errorf("%v", r)
	}
	// job_removed carried every id.
	var removed int
	for _, c := range e.pub.changes {
		if c.Kind == service.ChangeRemoved {
			removed += len(c.JobIDs)
		}
	}
	if removed != 3 {
		t.Errorf("published %d removed ids, want 3", removed)
	}
}

func TestBatchActions(t *testing.T) {
	e := newEnv(t)
	create := func(url string, eps ...int) string {
		var episodes []any
		for _, n := range eps {
			episodes = append(episodes, map[string]any{"episode": n, "url": fmt.Sprintf("%s/%d", url, n)})
		}
		r := e.want(e.request("POST", "/api/jobs/batch", map[string]any{
			"category": "tv", "show_name": "B", "season": 1, "episodes": episodes}), 201).Obj(t)
		return r["batch_id"].(string)
	}
	statusesOf := func(batch string) map[int]string {
		out := map[int]string{}
		r := e.want(e.request("GET", "/api/jobs?batch_id="+batch, nil), 200).Obj(t)
		for _, j := range r["jobs"].([]any) {
			m := j.(map[string]any)
			out[int(m["episode"].(float64))] = m["status"].(string)
		}
		return out
	}
	batch := create("https://bb.example/a", 1, 2, 3)
	list := e.store3(batch)

	// Make the batch mixed: ep1 pending, ep2 downloading, ep3 completed.
	setStatus(t, e, list[2], jobs.StatusDownloading)
	setStatus(t, e, list[3], jobs.StatusCompleted)

	r := e.want(e.request("POST", "/api/batches/"+batch+"/pause", nil), 200).Obj(t)
	if n := len(r["jobs"].([]any)); n != 2 {
		t.Errorf("pause updated %d jobs, want 2 (completed skipped)", n)
	}
	assertEqualJSON(t, "after pause", map[int]string{1: "paused", 2: "paused", 3: "completed"}, statusesOf(batch))

	r = e.want(e.request("POST", "/api/batches/"+batch+"/resume", nil), 200).Obj(t)
	if len(r["jobs"].([]any)) != 2 {
		t.Errorf("resume: %v", r)
	}
	assertEqualJSON(t, "after resume", map[int]string{1: "pending", 2: "pending", 3: "completed"}, statusesOf(batch))

	r = e.want(e.request("POST", "/api/batches/"+batch+"/cancel", nil), 200).Obj(t)
	if len(r["jobs"].([]any)) != 2 {
		t.Errorf("cancel: %v", r)
	}
	r = e.want(e.request("POST", "/api/batches/"+batch+"/retry", `{"overwrite":true}`), 200).Obj(t)
	if len(r["jobs"].([]any)) != 2 {
		t.Errorf("retry: %v", r)
	}
	assertEqualJSON(t, "after retry", map[int]string{1: "pending", 2: "pending", 3: "completed"}, statusesOf(batch))
	// Nothing to do: still 200 with an empty list.
	r = e.want(e.request("POST", "/api/batches/"+batch+"/resume", nil), 200).Obj(t)
	if jl, ok := r["jobs"].([]any); !ok || len(jl) != 0 {
		t.Errorf("empty result must be [] not null: %s", e.bodies[len(e.bodies)-1])
	}

	// A retry that collides is skipped, not fatal.
	b2 := create("https://bb.example/z", 4)
	l2 := e.store3(b2)
	e.want(e.request("POST", "/api/jobs/"+l2[4]+"/cancel", nil), 200)
	e.want(e.request("POST", "/api/jobs", map[string]any{"url": "https://bb.example/z/4", "category": "tv", "show_name": "B", "season": 1, "episode": 4}), 201)
	r = e.want(e.request("POST", "/api/batches/"+b2+"/retry", nil), 200).Obj(t)
	if len(r["jobs"].([]any)) != 0 {
		t.Errorf("colliding retry should be skipped: %v", r)
	}
}

// store3 returns episode -> job id for a batch.
func (e *env) store3(batch string) map[int]string {
	e.t.Helper()
	js, err := e.st.ListByBatch(e.t.Context(), batch)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[int]string{}
	for _, j := range js {
		out[*j.Episode] = j.ID
	}
	return out
}

func setStatus(t *testing.T, e *env, id string, st jobs.Status) {
	t.Helper()
	u := store.Update{Status: store.Set(st)}
	if jobs.IsFinished(st) {
		now := time.Now().UTC()
		u.FinishedAt = store.Set(&now)
	}
	if _, err := e.st.Update(t.Context(), id, u); err != nil {
		t.Fatal(err)
	}
}

func TestRecoversPanics(t *testing.T) {
	e := newEnv(t)
	s := &server{log: e.logsLogger()}
	h := s.logRequests(s.recoverPanics(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/api/x?token=abc", nil))
	if rec.Code != 500 {
		t.Fatalf("status = %d", rec.Code)
	}
	r := resp{Code: rec.Code, Raw: rec.Body.Bytes()}
	if r.ErrCode(t) != "internal" {
		t.Errorf("%s", r.Raw)
	}
	if strings.Contains(string(r.Raw), "boom") {
		t.Error("panic value leaked to the client")
	}
	if !strings.Contains(e.logs.String(), "panic in handler") {
		t.Errorf("panic not logged: %s", e.logs.String())
	}
}

func TestRequestLogHasPathOnly(t *testing.T) {
	e := newEnv(t)
	e.requestWith("GET", "/api/jobs?category=secretcat&batch_id=secretbatch", nil, "Bearer "+testToken)
	// Also with a header carrying a secret.
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/api/info?x=querysecret", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Cookie", "cookiesecret")
	req.Header.Set("X-Custom", "headersecret")
	e.h.ServeHTTP(httptest.NewRecorder(), req)
	logs := e.logs.String()
	if !strings.Contains(logs, "path=/api/jobs") || !strings.Contains(logs, "status=200") {
		t.Fatalf("no request line: %s", logs)
	}
	for _, s := range []string{"secretcat", "secretbatch", "querysecret", "cookiesecret", "headersecret", testToken, "Bearer"} {
		if strings.Contains(logs, s) {
			t.Errorf("log contains %q: %s", s, logs)
		}
	}
}

func TestInfoWithoutBinaryDoesNotFail(t *testing.T) {
	e := newEnv(t)
	e.h = New(Options{Config: e.cfg, Service: e.svc, Logger: e.logsLogger()})
	e.cfg.Ytdlp.Binary = e.cfg.DataDir + "/does-not-exist"
	r := e.want(e.request("GET", "/api/info", nil), 200).Obj(t)
	if r["ytdlp_version"] != "" {
		t.Errorf("ytdlp_version = %v, want empty when unknown", r["ytdlp_version"])
	}
}

func TestYtdlpVersionCached(t *testing.T) {
	calls := 0
	c := newVersionCache(func(context.Context, string) (string, error) { calls++; return "v1", nil })
	for range 3 {
		if v := c.get(t.Context(), "bin"); v != "v1" {
			t.Fatalf("v = %q", v)
		}
	}
	if calls != 1 {
		t.Errorf("probe called %d times, want 1", calls)
	}
	c.invalidate()
	c.get(t.Context(), "bin")
	if calls != 2 {
		t.Errorf("after invalidate: %d calls", calls)
	}
}

func TestYtdlpVersionRefreshesWhenBinaryChanges(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(bin, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	c := newVersionCache(func(context.Context, string) (string, error) { calls++; return "v" + strconv.Itoa(calls), nil })
	if v := c.get(t.Context(), bin); v != "v1" {
		t.Fatalf("v = %q", v)
	}
	if v := c.get(t.Context(), bin); v != "v1" || calls != 1 {
		t.Fatalf("unchanged file: v = %q after %d probes", v, calls)
	}
	// The background auto-update replaces the file: the next read re-probes.
	if err := os.WriteFile(bin, []byte("v2-longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := c.get(t.Context(), bin); v != "v2" || calls != 2 {
		t.Fatalf("changed file: v = %q after %d probes", v, calls)
	}
}

func TestWebSocketRouteIsAuthenticated(t *testing.T) {
	e := newEnv(t)
	reached := 0
	e.h = New(Options{Config: e.cfg, Service: e.svc, Logger: e.logsLogger(),
		WebSocket: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			reached++
			w.WriteHeader(http.StatusNoContent)
		})})
	for _, auth := range []string{"", "Bearer wrong"} {
		r := e.requestWith("GET", "/api/ws", nil, auth)
		if r.Code != 401 || r.ErrCode(t) != "unauthorized" {
			t.Errorf("auth %q: %d %s", auth, r.Code, r.Raw)
		}
	}
	if reached != 0 {
		t.Fatal("handler reached without a valid token")
	}
	e.want(e.request("GET", "/api/ws", nil), 204)
	if reached != 1 {
		t.Errorf("handler reached %d times, want 1", reached)
	}
	// Without a handler the route does not exist.
	e.h = New(Options{Config: e.cfg, Service: e.svc, Logger: e.logsLogger()})
	e.want(e.request("GET", "/api/ws", nil), 404)
}

func TestSubtitleLangIsCapped(t *testing.T) {
	e := newEnv(t)
	r := e.want(e.request("POST", "/api/jobs", map[string]any{
		"url": "https://a.example/lang", "category": "misc", "title": "L",
		"subtitles": []any{map[string]any{"url": "https://a.example/l.vtt", "lang": strings.Repeat("é", 200)}},
	}), 201).Obj(t)
	lang := r["subtitles"].([]any)[0].(map[string]any)["lang"].(string)
	if lang == "" || len(lang) > 16 {
		t.Errorf("lang = %q (%d bytes), want 1-16 bytes", lang, len(lang))
	}
}

func TestYtdlpCheck(t *testing.T) {
	e := newEnv(t)
	e.cfg.Ytdlp.Binary = ""
	for _, tc := range []struct {
		latest    string
		available bool
	}{
		{"2099.01.01", false}, {"2099.01.02", true}, {"2098.12.31", false},
	} {
		e.updater.latest = tc.latest
		got := e.want(e.request("GET", "/api/system/ytdlp/check", nil), 200).Obj(t)
		if got["current"] != "2099.01.01" || got["latest"] != tc.latest || got["update_available"] != tc.available {
			t.Errorf("unexpected check result: %v", got)
		}
	}
	if e.updater.calls != 0 {
		t.Fatal("checking performed an update")
	}
	e.updater.err = errors.New("upstream unavailable")
	e.want(e.request("GET", "/api/system/ytdlp/check", nil), 500)
	e.cfg.Ytdlp.Binary = "custom-yt-dlp"
	e.want(e.request("GET", "/api/system/ytdlp/check", nil), 409)
}
