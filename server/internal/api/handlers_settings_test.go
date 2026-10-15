package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/settings"
)

func TestSettingsAPIAuthenticationValidationAndConflicts(t *testing.T) {
	e := newEnv(t)
	m, err := settings.Open(t.Context(), e.cfg, e.st)
	if err != nil {
		t.Fatal(err)
	}
	e.h = New(Options{Config: e.cfg, Service: e.svc, Settings: m, Logger: e.logsLogger()})
	for _, method := range []string{"GET", "PUT"} {
		e.want(e.requestWith(method, "/api/system/settings", nil, ""), 401)
	}
	get := e.want(e.request("GET", "/api/system/settings", nil), 200)
	var d settings.Document
	if err := json.Unmarshal(get.Raw, &d); err != nil {
		t.Fatal(err)
	}
	d.Downloads.Concurrency = 4
	saved := e.want(e.request("PUT", "/api/system/settings", d), 200)
	if strings.Contains(string(saved.Raw), "extra_args") || strings.Contains(string(saved.Raw), "auth_token") {
		t.Fatal("private deployment fields exposed")
	}
	e.want(e.request("PUT", "/api/system/settings", d), 409)
	if err := json.Unmarshal(saved.Raw, &d); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"server", "data_dir", "extra_args"} {
		body, _ := json.Marshal(d)
		var request map[string]any
		_ = json.Unmarshal(body, &request)
		request[field] = "cannot edit"
		e.want(e.request("PUT", "/api/system/settings", request), 400)
	}
	info := e.want(e.request("GET", "/api/info", nil), 200).Obj(t)
	if info["settings_supported"] != true || info["setup_required"] != false || info["concurrency"] != float64(4) {
		t.Fatalf("info is stale: %v", info)
	}
	d.Categories = d.Categories[:0]
	e.want(e.request("PUT", "/api/system/settings", d), 200)
	if e.want(e.request("GET", "/api/info", nil), 200).Obj(t)["setup_required"] != true {
		t.Fatal("empty categories did not set setup_required")
	}
}
