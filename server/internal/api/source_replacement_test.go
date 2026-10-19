package api

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func TestSourceReplacement(t *testing.T) {
	for _, status := range []jobs.Status{jobs.StatusFailed, jobs.StatusCancelled} {
		for _, mode := range []string{"clear", "keep", "replace"} {
			t.Run(string(status)+"/"+mode, func(t *testing.T) {
				e := newEnv(t)
				j := newJob("https://old.example/video", status)
				j.Headers = map[string]string{"Cookie": "old-private-cookie"}
				j.Subtitles = []store.Subtitle{{URL: "https://old.example/sub", Lang: "en"}}
				j.BatchID = store.Ptr("batch")
				e.seed(j)
				headers := ""
				if mode == "replace" {
					headers = `,"headers":{"Referer":"new-private-header"}`
				}
				body := fmt.Sprintf(`{"source":{"url":"https://new.example/video","headers_mode":%q,"subtitles":[{"url":"https://new.example/sub"}]%s}}`, mode, headers)
				r := e.want(e.request("POST", "/api/jobs/"+j.ID+"/retry", body), 200)
				if strings.Contains(string(r.Raw), "private") || strings.Contains(string(r.Raw), `"headers"`) {
					t.Fatal("private headers leaked")
				}
				got, err := e.st.Get(t.Context(), j.ID)
				if err != nil {
					t.Fatal(err)
				}
				if got.URL != "https://new.example/video" || got.Status != jobs.StatusPending || !got.ResetWorkDir || *got.BatchID != *j.BatchID || *got.Title != *j.Title || got.CreatedAt != j.CreatedAt {
					t.Fatalf("identity/source not preserved: %v", got)
				}
				wantHeaders := map[string]string{}
				if mode == "keep" {
					wantHeaders = j.Headers
				}
				if mode == "replace" {
					wantHeaders = map[string]string{"Referer": "new-private-header"}
				}
				if !reflect.DeepEqual(got.Headers, wantHeaders) || len(got.Subtitles) != 1 || got.Subtitles[0].Lang != "und" {
					t.Fatal("source inputs not replaced correctly")
				}
			})
		}
	}
}

func TestSourceReplacementRejectsWithoutMutation(t *testing.T) {
	cases := []struct{ name, source string }{
		{"url", `{"url":"file:///tmp/video","headers_mode":"clear","subtitles":[]}`},
		{"mode", `{"url":"https://new.example/video","subtitles":[]}`},
		{"missing subtitles", `{"url":"https://new.example/video","headers_mode":"clear"}`},
		{"null subtitles", `{"url":"https://new.example/video","headers_mode":"clear","subtitles":null}`},
		{"missing headers", `{"url":"https://new.example/video","headers_mode":"replace","subtitles":[]}`},
		{"unexpected headers", `{"url":"https://new.example/video","headers_mode":"keep","headers":{},"subtitles":[]}`},
		{"invalid header", `{"url":"https://new.example/video","headers_mode":"replace","headers":{"Bad:Name":"x"},"subtitles":[]}`},
		{"invalid subtitle", `{"url":"https://new.example/video","headers_mode":"clear","subtitles":[{"url":"bad"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			j := e.seed(newJob("https://old.example/video", jobs.StatusFailed))
			before, _ := e.st.Get(t.Context(), j.ID)
			e.want(e.request("POST", "/api/jobs/"+j.ID+"/retry", `{"source":`+tc.source+`}`), 400)
			got, _ := e.st.Get(t.Context(), j.ID)
			if !reflect.DeepEqual(got, before) {
				t.Fatal("invalid request changed job")
			}
		})
	}
	e := newEnv(t)
	body := `{"source":{"url":"https://new.example/video","headers_mode":"clear","subtitles":[]}}`
	e.want(e.request("POST", "/api/jobs/missing/retry", body), 404)
	for _, status := range []jobs.Status{jobs.StatusPending, jobs.StatusDownloading, jobs.StatusProcessing, jobs.StatusPaused, jobs.StatusCompleted} {
		j := e.seed(newJob("https://old.example/"+string(status), status))
		e.want(e.request("POST", "/api/jobs/"+j.ID+"/retry", body), 409)
	}
	e.seed(newJob("https://new.example/video", jobs.StatusPending))
	j := e.seed(newJob("https://old.example/failed", jobs.StatusFailed))
	before, _ := e.st.Get(t.Context(), j.ID)
	r := e.want(e.request("POST", "/api/jobs/"+j.ID+"/retry", body), 409)
	if r.ErrCode(t) != "duplicate" {
		t.Fatal("expected duplicate")
	}
	got, _ := e.st.Get(t.Context(), j.ID)
	if !reflect.DeepEqual(got, before) {
		t.Fatal("duplicate changed original")
	}
	e.want(e.request("POST", "/api/batches/batch/retry", body), 400)
}

func TestReplacementContractFixture(t *testing.T) {
	e := newEnv(t)
	j := e.seed(newJob("https://old.example/source", jobs.StatusFailed))
	e.want(e.request("POST", "/api/jobs/"+j.ID+"/retry", string(fixtureRaw(t, "retry-source.json"))), 200)
}
