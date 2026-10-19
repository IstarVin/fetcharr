package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/config"
)

// The golden tests drive the real handler and compare it with contract/*.json.

var jobFixtureNames = []string{
	"job-pending.json", "job-downloading.json", "job-processing.json", "job-paused.json",
	"job-completed.json", "job-failed.json", "job-cancelled.json",
}

// Every job-<status>.json: a job seeded with exactly the fixture's values
// (including headers in the store) must come back byte-for-byte equal as JSON,
// without headers.
func TestGoldenJobObjects(t *testing.T) {
	e := newEnv(t)
	for _, name := range jobFixtureNames {
		e.seedFixture(name)
	}
	for _, name := range jobFixtureNames {
		t.Run(name, func(t *testing.T) {
			want := fixture(t, name).(map[string]any)
			r := e.want(e.request("GET", "/api/jobs/"+want["id"].(string), nil), 200)
			got := r.JSON(t)
			assertShape(t, name, want, got, nil)
			assertTimestamps(t, name, got)
			assertEqualJSON(t, name, want, got)
			if strings.Contains(strings.ToLower(string(r.Raw)), "header") {
				t.Errorf("%s: response mentions headers: %s", name, r.Raw)
			}
		})
	}
}

// jobs-list.json: all 7 jobs, newest created_at first.
func TestGoldenJobsList(t *testing.T) {
	e := newEnv(t)
	// Insert in scrambled order; the API must order by created_at DESC.
	for _, i := range []int{3, 0, 6, 1, 5, 2, 4} {
		e.seedFixture(jobFixtureNames[i])
	}
	r := e.want(e.request("GET", "/api/jobs", nil), 200)
	want, got := fixture(t, "jobs-list.json"), r.JSON(t)
	assertShape(t, "jobs-list", want, got, nil)
	assertTimestamps(t, "jobs-list", got)
	assertEqualJSON(t, "jobs-list", want, got)
}

func TestGoldenInfo(t *testing.T) {
	// Default probe: really run the fake yt-dlp for --version.
	e := newEnv(t)
	e.h = New(Options{Config: e.cfg, Service: e.svc, Version: "1.0.0", Logger: e.logsLogger()})
	if _, err := os.Stat(e.cfg.Ytdlp.Binary); err != nil {
		t.Skip(err)
	}
	r := e.want(e.request("GET", "/api/info", nil), 200)
	got := r.Obj(t)
	assertShape(t, "info", fixture(t, "info.json"), got, nil)
	if got["ytdlp_version"] != "2099.01.01" {
		t.Errorf("ytdlp_version = %v, want the fake binary's version", got["ytdlp_version"])
	}
	if got["ytdlp_managed"] != false || got["auth_required"] != true || got["concurrency"] != float64(2) {
		t.Errorf("unexpected info: %s", r.Raw)
	}
	cats := got["categories"].([]any)
	if len(cats) != 3 {
		t.Fatalf("categories: %s", r.Raw)
	}
	for i, id := range []string{"movies", "tv", "misc"} {
		c := cats[i].(map[string]any)
		if c["id"] != id {
			t.Errorf("category %d = %v, want %s", i, c["id"], id)
		}
		if c["free_bytes"].(float64) <= 0 {
			t.Errorf("free_bytes of %s = %v, want > 0", id, c["free_bytes"])
		}
	}
}

func TestGoldenCreateSingle(t *testing.T) {
	// create-job-<layout>.json -> the job fixture of the same ids, but pending.
	cases := []struct{ req, shapeOf string }{
		{"create-job-movie.json", "job-processing.json"},
		{"create-job-tv.json", "job-failed.json"},
		{"create-job-flat.json", "job-paused.json"},
	}
	for _, c := range cases {
		t.Run(c.req, func(t *testing.T) {
			e := newEnv(t)
			reqBody := fixture(t, c.req).(map[string]any)
			r := e.want(e.request("POST", "/api/jobs", fixtureRaw(t, c.req)), http.StatusCreated)
			got := r.Obj(t)
			assertShape(t, c.req, fixture(t, c.shapeOf), got, jobStateKeys)
			assertTimestamps(t, c.req, got)
			if got["status"] != "pending" {
				t.Errorf("status = %v, want pending", got["status"])
			}
			for _, k := range []string{"progress", "file_path", "error_code", "error_message", "started_at", "finished_at", "batch_id"} {
				if got[k] != nil {
					t.Errorf("%s = %v, want null", k, got[k])
				}
			}
			if got["created_at"] != got["queued_at"] {
				t.Errorf("queued_at %v != created_at %v", got["queued_at"], got["created_at"])
			}
			for _, k := range []string{"url", "category", "title", "show_name", "season", "episode", "year"} {
				if rv, ok := reqBody[k]; ok {
					if got[k] != rv {
						t.Errorf("%s = %v, want %v", k, got[k], rv)
					}
				} else if got[k] != nil {
					t.Errorf("%s = %v, want null (not in request)", k, got[k])
				}
			}
			if _, ok := got["headers"]; ok {
				t.Error("response has a headers field")
			}
			// Subtitles echo the request; the id is a fresh uuid and the job is fetchable.
			if want, ok := reqBody["subtitles"]; ok {
				assertEqualJSON(t, "subtitles", want, got["subtitles"])
			} else if len(got["subtitles"].([]any)) != 0 {
				t.Errorf("subtitles = %v, want []", got["subtitles"])
			}
			e.want(e.request("GET", "/api/jobs/"+got["id"].(string), nil), 200)
		})
	}
}

func TestGoldenCreateBatch(t *testing.T) {
	e := newEnv(t)
	req := fixture(t, "create-batch.json").(map[string]any)
	// Episode 3 must be skipped as a duplicate of an active job.
	e.seed(newJobWith("https://cdn.example/sev/s02e03.m3u8", "tv"))

	r := e.want(e.request("POST", "/api/jobs/batch", fixtureRaw(t, "create-batch.json")), http.StatusCreated)
	got := r.Obj(t)
	wantResp := fixture(t, "batch-create-response.json").(map[string]any)
	assertShape(t, "batch", wantResp, got, nil)
	assertTimestamps(t, "batch", got)
	assertEqualJSON(t, "skipped", wantResp["skipped"], got["skipped"])

	batchID, _ := got["batch_id"].(string)
	if batchID == "" {
		t.Fatalf("no batch_id: %s", r.Raw)
	}
	js := got["jobs"].([]any)
	if len(js) != 2 {
		t.Fatalf("jobs = %d, want 2: %s", len(js), r.Raw)
	}
	wantJobs := wantResp["jobs"].([]any)
	var prevCreated string
	for i, jv := range js {
		j := jv.(map[string]any)
		w := wantJobs[i].(map[string]any)
		for _, k := range []string{"category", "url", "title", "show_name", "season", "episode", "year", "status", "subtitles", "progress", "warnings"} {
			assertEqualJSON(t, "jobs["+itoa(i)+"]."+k, w[k], j[k])
		}
		if j["batch_id"] != batchID {
			t.Errorf("jobs[%d].batch_id = %v, want %s", i, j["batch_id"], batchID)
		}
		if c := j["created_at"].(string); c <= prevCreated {
			t.Errorf("created_at not strictly increasing: %q after %q", c, prevCreated)
		} else {
			prevCreated = c
		}
	}
	// Headers merged: batch Referer for ep 1; Referer + episode Cookie for ep 2.
	byEp := map[int]map[string]string{}
	listed, _, err := e.svc.List(t.Context(), storeFilterBatch(batchID))
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range listed {
		byEp[*j.Episode] = j.Headers
	}
	if h := byEp[1]; len(h) != 1 || h["Referer"] != "https://example.com/" {
		t.Errorf("episode 1 headers = %v", h)
	}
	if h := byEp[2]; len(h) != 2 || h["Referer"] != "https://example.com/" || h["Cookie"] != "session=abc123" {
		t.Errorf("episode 2 headers = %v", h)
	}
	if len(req["episodes"].([]any)) != 3 {
		t.Fatal("fixture changed")
	}
}

func TestGoldenErrors(t *testing.T) {
	check := func(t *testing.T, fixtureName string, r resp, status int) {
		t.Helper()
		if r.Code != status {
			t.Fatalf("status = %d, want %d; body %s", r.Code, status, r.Raw)
		}
		want := fixture(t, fixtureName)
		got := r.JSON(t)
		assertShape(t, fixtureName, want, got, nil)
		wantCode := want.(map[string]any)["error"].(map[string]any)["code"]
		if r.ErrCode(t) != wantCode {
			t.Errorf("error.code = %q, want %q", r.ErrCode(t), wantCode)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q", ct)
		}
	}

	t.Run("invalid_request", func(t *testing.T) {
		e := newEnv(t)
		check(t, "error-invalid_request.json", e.request("POST", "/api/jobs",
			map[string]any{"url": "https://x.example/a", "category": "foo", "title": "A"}), 400)
	})
	t.Run("unauthorized", func(t *testing.T) {
		e := newEnv(t)
		check(t, "error-unauthorized.json", e.requestWith("GET", "/api/jobs", nil, ""), 401)
	})
	t.Run("forbidden", func(t *testing.T) {
		e := newEnv(t)
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/api/jobs", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Sec-Fetch-Site", "cross-site")
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		check(t, "error-forbidden.json", resp{Code: rec.Code, Header: rec.Header(), Raw: rec.Body.Bytes()}, 403)
	})
	t.Run("not_found", func(t *testing.T) {
		e := newEnv(t)
		check(t, "error-not_found.json", e.request("GET", "/api/jobs/00000000-0000-4000-8000-000000000000", nil), 404)
	})
	t.Run("invalid_state", func(t *testing.T) {
		e := newEnv(t)
		j := e.seedFixture("job-completed.json")
		check(t, "error-invalid_state.json", e.request("POST", "/api/jobs/"+j.ID+"/pause", nil), 409)
	})
	t.Run("duplicate", func(t *testing.T) {
		e := newEnv(t)
		e.seedFixture("job-processing.json")
		check(t, "error-duplicate.json", e.request("POST", "/api/jobs", fixtureRaw(t, "create-job-movie.json")), 409)
	})
	t.Run("ytdlp_unmanaged", func(t *testing.T) {
		e := newEnv(t) // ytdlp.binary is set
		check(t, "error-ytdlp_unmanaged.json", e.request("POST", "/api/system/ytdlp/update", nil), 409)
		if e.updater.calls != 0 {
			t.Error("updater was called in unmanaged mode")
		}
	})
	t.Run("ytdlp_busy", func(t *testing.T) {
		e := newEnv(t, func(c *config.Config) { c.Ytdlp.Binary = "" })
		e.updater.err = ErrYtdlpBusy
		check(t, "error-ytdlp_busy.json", e.request("POST", "/api/system/ytdlp/update", nil), 409)
	})
}

func TestGoldenYtdlpUpdate(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Ytdlp.Binary = "" })
	r := e.want(e.request("POST", "/api/system/ytdlp/update", nil), 200)
	got := r.JSON(t)
	assertShape(t, "ytdlp-update", fixture(t, "ytdlp-update.json"), got, nil)
	assertEqualJSON(t, "ytdlp-update", fixture(t, "ytdlp-update.json"), got)

	// Default stub (S5 not implemented): 500 internal, not a crash.
	e.h = New(Options{Config: e.cfg, Service: e.svc, Logger: e.logsLogger()})
	r = e.request("POST", "/api/system/ytdlp/update", nil)
	if r.Code != 500 || r.ErrCode(t) != "internal" {
		t.Errorf("stub updater: %d %s", r.Code, r.Raw)
	}
}

// Every contract request body must also be accepted by the unauthenticated
// server (auth_token empty) without a token.
func TestNoTokenConfiguredMeansNoAuth(t *testing.T) {
	e := newEnv(t, func(c *config.Config) { c.Server.AuthToken = "" })
	e.want(e.requestWith("GET", "/api/jobs", nil, ""), 200)
	info := e.want(e.requestWith("GET", "/api/info", nil, ""), 200).Obj(t)
	if info["auth_required"] != false {
		t.Errorf("auth_required = %v", info["auth_required"])
	}
}

func TestContractDirIsReadable(t *testing.T) {
	m, err := filepath.Glob(filepath.Join(contractDir(t), "*.json"))
	if err != nil || len(m) == 0 {
		t.Fatalf("no contract fixtures found: %v", err)
	}
}
