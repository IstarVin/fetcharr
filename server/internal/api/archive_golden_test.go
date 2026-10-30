package api

import (
	"net/http"
	"testing"
)

// job-completed-archive.json: a completed season-pack job comes back as
// stored, with its archive spec and placed files.
func TestGoldenArchiveJob(t *testing.T) {
	e := newEnv(t)
	e.seedFixture("job-completed-archive.json")
	want := fixture(t, "job-completed-archive.json").(map[string]any)
	got := e.want(e.request("GET", "/api/jobs/"+want["id"].(string), nil), 200).JSON(t)
	assertShape(t, "job-completed-archive", want, got, nil)
	assertEqualJSON(t, "job-completed-archive", want, got)
}

// create-job-archive.json creates a pending tv job without an episode.
func TestGoldenCreateArchive(t *testing.T) {
	e := newEnv(t)
	got := e.want(e.request("POST", "/api/jobs", fixtureRaw(t, "create-job-archive.json")), http.StatusCreated).Obj(t)
	if got["status"] != "pending" || got["episode"] != nil || got["season"] != float64(1) {
		t.Fatalf("job = %v", got)
	}
	arch, ok := got["archive"].(map[string]any)
	if !ok || arch["episode_offset"] != float64(0) || arch["episode_titles"].(map[string]any)["2"] != "A Journey with a Pierrot" {
		t.Fatalf("archive = %v", got["archive"])
	}
	if files, ok := got["files"]; ok && len(files.([]any)) != 0 {
		t.Errorf("files = %v", files)
	}
	// The same season can't be queued twice, nor an episode of it.
	again := `{"url":"https://hubcloud.ist/drive/other","category":"tv","show_name":"Dead-End Job","season":1,"year":2026,"archive":{}}`
	e.want(e.request("POST", "/api/jobs", []byte(again)), http.StatusConflict)
	ep := `{"url":"https://cdn.example/e2.mkv","category":"tv","show_name":"Dead-End Job","season":1,"episode":2,"year":2026}`
	e.want(e.request("POST", "/api/jobs", []byte(ep)), http.StatusConflict)
	// Another season is fine.
	s2 := `{"url":"https://cdn.example/s2.zip","category":"tv","show_name":"Dead-End Job","season":2,"year":2026,"archive":{}}`
	e.want(e.request("POST", "/api/jobs", []byte(s2)), http.StatusCreated)
}

func TestCreateArchiveValidation(t *testing.T) {
	e := newEnv(t)
	for name, body := range map[string]string{
		"movie category":  `{"url":"https://x/a.zip","category":"movies","title":"T","archive":{}}`,
		"with episode":    `{"url":"https://x/b.zip","category":"tv","show_name":"S","season":1,"episode":1,"archive":{}}`,
		"no season":       `{"url":"https://x/c.zip","category":"tv","show_name":"S","archive":{}}`,
		"bad title key":   `{"url":"https://x/d.zip","category":"tv","show_name":"S","season":1,"archive":{"episode_titles":{"one":"x"}}}`,
		"offset too wide": `{"url":"https://x/e.zip","category":"tv","show_name":"S","season":1,"archive":{"episode_offset":100000}}`,
	} {
		t.Run(name, func(*testing.T) {
			e.want(e.request("POST", "/api/jobs", []byte(body)), http.StatusBadRequest)
		})
	}
}
