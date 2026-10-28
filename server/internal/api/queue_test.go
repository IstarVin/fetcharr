package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/settings"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func TestInfoAdvertisesNewCapabilities(t *testing.T) {
	e := newEnv(t)
	info := e.want(e.request("GET", "/api/info", nil), 200).Obj(t)
	for _, k := range []string{"queue_priority_supported", "history_search_supported"} {
		if info[k] != true {
			t.Errorf("%s = %v", k, info[k])
		}
	}
	if info["download_window"] != nil {
		t.Errorf("download_window = %v, want null without a window", info["download_window"])
	}
}

func TestPrioritizeEndpoints(t *testing.T) {
	e := newEnv(t)
	a := e.seed(&store.Job{Category: "misc", URL: "https://x/a", Title: store.Ptr("A")})
	b := e.seed(&store.Job{Category: "misc", URL: "https://x/b", Title: store.Ptr("B")})
	got := e.want(e.request("POST", "/api/jobs/"+b.ID+"/prioritize", nil), 200).Obj(t)
	if got["id"] != b.ID {
		t.Fatalf("returned %v", got["id"])
	}
	pending, _ := e.st.PendingJobs(t.Context(), 0)
	if pending[0].ID != b.ID || pending[1].ID != a.ID {
		t.Fatal("prioritized job is not first")
	}
	paused := e.seed(&store.Job{Category: "misc", URL: "https://x/p", Title: store.Ptr("P"), Status: jobs.StatusPaused})
	if code := e.want(e.request("POST", "/api/jobs/"+paused.ID+"/prioritize", nil), 409).ErrCode(t); code != "invalid_state" {
		t.Fatalf("code = %s", code)
	}
	e.want(e.request("POST", "/api/jobs/nope/prioritize", nil), 404)
	e.want(e.request("POST", "/api/batches/nope/prioritize", nil), 404)
	e.want(e.requestWith("POST", "/api/jobs/"+a.ID+"/prioritize", nil, ""), 401)
}

func TestListSearch(t *testing.T) {
	e := newEnv(t)
	e.seed(&store.Job{Category: "movies", URL: "https://x/1", Title: store.Ptr("Dune"), Status: jobs.StatusCompleted})
	e.seed(&store.Job{Category: "movies", URL: "https://x/2", Title: store.Ptr("Arrival"), Status: jobs.StatusFailed})
	got := e.want(e.request("GET", "/api/jobs?status=completed,failed&q=dun", nil), 200).Obj(t)
	if got["total"] != float64(1) {
		t.Fatalf("total = %v", got["total"])
	}
	e.want(e.request("GET", "/api/jobs?q="+strings.Repeat("x", maxQueryLen+1), nil), 400)
}

type fakeTester struct{ err error }

func (f *fakeTester) Test(context.Context, config.Notification) error { return f.err }

func TestNotificationTestEndpoint(t *testing.T) {
	e := newEnv(t)
	m, err := settings.Open(t.Context(), e.cfg, e.st)
	if err != nil {
		t.Fatal(err)
	}
	tester := &fakeTester{}
	e.h = New(Options{Config: e.cfg, Service: e.svc, Settings: m, Notifier: tester, Logger: e.logsLogger()})
	var d settings.Document
	if err := json.Unmarshal(e.want(e.request("GET", "/api/system/settings", nil), 200).Raw, &d); err != nil {
		t.Fatal(err)
	}
	d.Notifications = []settings.Notification{{ID: "n1", Type: "webhook", URL: "https://hooks.example/x", OnFailed: true}}
	e.want(e.request("PUT", "/api/system/settings", d), 200)

	e.want(e.request("POST", "/api/system/notifications/test", map[string]string{"id": "n1"}), 204)
	e.want(e.request("POST", "/api/system/notifications/test", map[string]string{"id": "other"}), 404)
	tester.err = errors.New("the target answered 401 Unauthorized")
	r := e.want(e.request("POST", "/api/system/notifications/test", map[string]string{"id": "n1"}), 400)
	if !strings.Contains(string(r.Raw), "401") {
		t.Fatalf("body = %s", r.Raw)
	}
}
