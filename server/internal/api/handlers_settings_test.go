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

func TestSettingsAPIDownloadControlsAndNotifications(t *testing.T) {
	e := newEnv(t)
	m, err := settings.Open(t.Context(), e.cfg, e.st)
	if err != nil {
		t.Fatal(err)
	}
	e.h = New(Options{Config: e.cfg, Service: e.svc, Settings: m, Logger: e.logsLogger()})
	var d settings.Document
	if err := json.Unmarshal(e.want(e.request("GET", "/api/system/settings", nil), 200).Raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Notifications == nil {
		t.Fatal("notifications must be an array")
	}
	token := "secret-token"
	d.Downloads.RateLimit, d.Downloads.WindowStart, d.Downloads.WindowEnd, d.Downloads.AutoRetries = 2_000_000, "23:00", "06:00", 3
	d.Notifications = []settings.Notification{{ID: "n1", Type: "ntfy", URL: "https://ntfy.example/topic", OnCompleted: true, Credential: &token}}
	saved := e.want(e.request("PUT", "/api/system/settings", d), 200)
	if strings.Contains(string(saved.Raw), token) {
		t.Fatal("notification credential exposed")
	}
	if err := json.Unmarshal(saved.Raw, &d); err != nil {
		t.Fatal(err)
	}
	if !d.Notifications[0].HasCredential || d.Downloads.WindowStart != "23:00" {
		t.Fatalf("not saved: %+v", d)
	}

	// An older client sends neither the new download fields nor notifications.
	body, _ := json.Marshal(d)
	var legacy map[string]any
	_ = json.Unmarshal(body, &legacy)
	delete(legacy, "notifications")
	dl := legacy["downloads"].(map[string]any)
	for _, k := range []string{"rate_limit", "window_start", "window_end", "auto_retries"} {
		delete(dl, k)
	}
	if err := json.Unmarshal(e.want(e.request("PUT", "/api/system/settings", legacy), 200).Raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Downloads.RateLimit != 2_000_000 || d.Downloads.WindowEnd != "06:00" || d.Downloads.AutoRetries != 3 || len(d.Notifications) != 1 {
		t.Fatalf("legacy save reset new settings: %+v", d)
	}
	if n, ok := m.Notification("n1"); !ok || n.Token != token {
		t.Fatal("legacy save lost the notification credential")
	}

	info := e.want(e.request("GET", "/api/info", nil), 200).Obj(t)
	w, ok := info["download_window"].(map[string]any)
	if !ok || w["start"] != "23:00" || w["end"] != "06:00" {
		t.Fatalf("download_window missing from info: %v", info["download_window"])
	}

	bad := d
	bad.Downloads.WindowStart = "7pm"
	e.want(e.request("PUT", "/api/system/settings", bad), 400)
	bad = d
	bad.Notifications = []settings.Notification{{ID: "n2", Type: "ntfy", URL: "not a url"}}
	e.want(e.request("PUT", "/api/system/settings", bad), 400)
}
