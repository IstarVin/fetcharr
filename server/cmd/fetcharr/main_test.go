package main

import (
	"bytes"
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

	"github.com/coder/websocket"

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
	var logs bytes.Buffer // read only after serve returned
	go func() { done <- serve(ctx, cfg, slog.New(slog.NewTextHandler(&logs, nil)), ln) }()

	base := "http://" + ln.Addr().String()
	if code, body := get(t, base+"/api/health"); code != 200 || !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("health: %d %s", code, body)
	}
	// Recovery sets the job back to pending, then the runner picks it up at
	// once. The test config has no yt-dlp binary, so the run fails: reaching
	// failed/download_failed proves the job left "downloading" and was
	// dispatched again.
	var got struct {
		Status    string `json:"status"`
		ErrorCode string `json:"error_code"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, body := get(t, base+"/api/jobs/"+j.ID)
		if code != 200 {
			t.Fatalf("get job: %d %s", code, body)
		}
		got.Status, got.ErrorCode = "", ""
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Status == "failed" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got.Status != "failed" || got.ErrorCode != "download_failed" {
		t.Errorf("recovered job = %+v, want failed/download_failed", got)
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
	if !strings.Contains(logs.String(), "requeued interrupted jobs") {
		t.Errorf("startup recovery was not logged:\n%s", logs.String())
	}
}

// The WebSocket endpoint is wired: a snapshot arrives, and a graceful stop
// disconnects the client (close code 1001) without hanging.
func TestServeWebSocketAndShutdown(t *testing.T) {
	cfg := testConfig(t)
	pre, err := store.Open(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	j := &store.Job{Category: "misc", URL: "https://x.example/ws", Title: store.Ptr("W"), Status: jobs.StatusPaused}
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

	dctx, dcancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer dcancel()
	c, resp, err := websocket.Dial(dctx, "ws://"+ln.Addr().String()+"/api/ws", nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	_, raw, err := c.Read(dctx)
	if err != nil {
		t.Fatal(err)
	}
	var snap struct {
		Type string
		Jobs []struct{ ID, Status string }
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Type != "snapshot" || len(snap.Jobs) != 1 || snap.Jobs[0].ID != j.ID || snap.Jobs[0].Status != "paused" {
		t.Errorf("snapshot = %s", raw)
	}

	cancel()
	_, _, err = c.Read(dctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
		t.Errorf("close status = %v (%v), want going away", got, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("serve did not return after cancel")
	}
}
