package direct

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/ytdlp"
)

// payload starts with the zip magic so the extension is sniffed.
var payload = append([]byte("PK\x03\x04"), bytes.Repeat([]byte("0123456789"), 300_000)...)

func serve(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func rangeServer(t *testing.T, ranges *atomic.Int32) *httptest.Server {
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" && ranges != nil {
			ranges.Add(1)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(payload))
	})
}

func TestDownloadFull(t *testing.T) {
	dir := t.TempDir()
	var last ytdlp.Progress
	got, err := Download(context.Background(), http.DefaultClient, Spec{
		URL: rangeServer(t, nil).URL + "/hub/abc", Dir: dir,
		OnProgress: func(p ytdlp.Progress) { last = p },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "media.zip") {
		t.Fatalf("path = %s", got)
	}
	b, _ := os.ReadFile(got)
	if !bytes.Equal(b, payload) {
		t.Fatal("content differs")
	}
	if last.DownloadedBytes == nil || *last.DownloadedBytes != int64(len(payload)) || last.Percent == nil || *last.Percent != 100 {
		t.Errorf("final progress = %+v", last)
	}
}

func TestDownloadResumes(t *testing.T) {
	dir := t.TempDir()
	half := len(payload) / 2
	if err := os.WriteFile(filepath.Join(dir, PartName), payload[:half], 0o640); err != nil {
		t.Fatal(err)
	}
	var ranges atomic.Int32
	got, err := Download(context.Background(), http.DefaultClient, Spec{URL: rangeServer(t, &ranges).URL, Dir: dir, Resumable: true})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(got)
	if !bytes.Equal(b, payload) || ranges.Load() != 1 {
		t.Fatalf("resume: equal=%v ranges=%d", bytes.Equal(b, payload), ranges.Load())
	}
}

func TestDownloadRestartsWhenRangeIgnored(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, PartName), []byte("garbage"), 0o640); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) })
	got, err := Download(context.Background(), http.DefaultClient, Spec{URL: srv.URL, Dir: dir, Resumable: true})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(got); !bytes.Equal(b, payload) {
		t.Fatal("content differs after restart")
	}
}

func TestDownloadRejectsHTML(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html>ads</html>"))
	})
	_, err := Download(context.Background(), http.DefaultClient, Spec{URL: srv.URL + "/?sig=secret", Dir: t.TempDir()})
	if !IsRetryable(err) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadHTTPError(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	_, err := Download(context.Background(), http.DefaultClient, Spec{URL: srv.URL, Dir: t.TempDir()})
	if !IsRetryable(err) || !strings.Contains(err.Error(), "403") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadStall(t *testing.T) {
	release := make(chan struct{})
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("PK\x03\x04"))
		w.(http.Flusher).Flush()
		<-release
	})
	defer close(release)
	_, err := Download(context.Background(), http.DefaultClient, Spec{URL: srv.URL, Dir: t.TempDir(), StallTimeout: 200 * time.Millisecond})
	if !IsRetryable(err) || !strings.Contains(err.Error(), "no data") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownloadCancel(t *testing.T) {
	release := make(chan struct{})
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("PK"))
		w.(http.Flusher).Flush()
		<-release
	})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	_, err := Download(ctx, http.DefaultClient, Spec{URL: srv.URL, Dir: t.TempDir()})
	if err == nil || IsRetryable(err) {
		t.Fatalf("err = %v, want the context error", err)
	}
}

func TestExtensionSources(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cd" {
			w.Header().Set("Content-Disposition", `attachment; filename="Show.S01.1080p.7z"`)
		}
		_, _ = w.Write([]byte("\x1a\x45\xdf\xa3 matroska"))
	})
	for p, want := range map[string]string{"/cd": "media.7z", "/x": "media.mkv"} {
		got, err := Download(context.Background(), http.DefaultClient, Spec{URL: srv.URL + p, Dir: t.TempDir()})
		if err != nil || filepath.Base(got) != want {
			t.Errorf("%s: %s, %v; want %s", p, got, err, want)
		}
	}
}

func TestRateLimit(t *testing.T) {
	small := payload[:200_000]
	srv := serve(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(small) })
	start := time.Now()
	if _, err := Download(context.Background(), http.DefaultClient, Spec{URL: srv.URL, Dir: t.TempDir(), RateLimit: 400_000}); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Errorf("rate limit not applied: %s", d)
	}
}
