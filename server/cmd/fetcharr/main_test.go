package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func TestRunUsageErrorsAndBadConfig(t *testing.T) {
	if code := run([]string{"--bogus"}); code != 2 {
		t.Errorf("--bogus: exit code = %d, want 2", code)
	}
	if code := run([]string{"-h"}); code != 0 {
		t.Errorf("-h: exit code = %d, want 0", code)
	}
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if code := run([]string{"--config", missing}); code != 1 {
		t.Errorf("missing config: exit code = %d, want 1", code)
	}
	invalid := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(invalid, []byte("categories: []\nserver: {port: 0}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"--config", invalid}); code != 1 {
		t.Errorf("invalid config: exit code = %d, want 1", code)
	}
	// FETCHARR_CONFIG is honoured when the flag is absent.
	t.Setenv("FETCHARR_CONFIG", missing)
	if code := run(nil); code != 1 {
		t.Errorf("env config: exit code = %d, want 1", code)
	}
}

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	media := filepath.Join(dir, "media")
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = filepath.Join(dir, "data")
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.Categories = []config.Category{{ID: "misc", Label: "Misc", Path: media, Layout: naming.LayoutFlat}}
	return cfg
}

func get(t *testing.T, url string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

// serve starts the real server, recovers interrupted jobs, answers requests
// and shuts down cleanly when its context is cancelled.
func TestServeRecoversAndShutsDown(t *testing.T) {
	cfg := testConfig(t)

	// A crash left a job downloading.
	pre, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	j := &store.Job{Category: "misc", URL: "https://x.example/a", Title: store.Ptr("A"), Status: jobs.StatusDownloading}
	if err := pre.Insert(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	if err := pre.Close(); err != nil {
		t.Fatal(err)
	}

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), ln) }()

	base := "http://" + ln.Addr().String()
	if code, body := get(t, base+"/api/health"); code != 200 || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("health: %d %s", code, body)
	}
	code, body := get(t, base+"/api/jobs/"+j.ID)
	if code != 200 {
		t.Fatalf("get job: %d %s", code, body)
	}
	var got struct{ Status string }
	if err := json.Unmarshal(body, &got); err != nil || got.Status != "pending" {
		t.Errorf("interrupted job status = %q (%v), want pending", got.Status, err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not return after cancel")
	}
}
