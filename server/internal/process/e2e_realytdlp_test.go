package process

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/api"
	"github.com/istarvin/fetcharr/server/internal/events"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/service"
)

const e2eToken = "tok-123"

// makeMP4 generates a 2 second test video with ffmpeg.
func makeMP4(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.mp4")
	var last []byte
	for _, vcodec := range []string{"libx264", "mpeg4"} {
		cmd := exec.CommandContext(t.Context(), "ffmpeg", "-nostdin", "-loglevel", "error", "-y",
			"-f", "lavfi", "-i", "testsrc=d=2:s=160x120", "-f", "lavfi", "-i", "sine=d=2",
			"-shortest", "-c:v", vcodec, "-c:a", "aac", "-pix_fmt", "yuv420p", out)
		var err error
		if last, err = cmd.CombinedOutput(); err == nil {
			b, rerr := os.ReadFile(out)
			if rerr != nil {
				t.Fatal(rerr)
			}
			return b
		}
	}
	t.Fatalf("ffmpeg cannot generate a test video: %s", last)
	return nil
}

// TestEndToEndRealYtdlp runs the whole server stack (HTTP API, service, bus,
// runner, processor) with the real yt-dlp and ffmpeg against a local
// httptest server that insists on a custom request header. It never touches
// the network.
func TestEndToEndRealYtdlp(t *testing.T) {
	ytdlpBin, err := exec.LookPath("yt-dlp")
	if err != nil {
		t.Skip("yt-dlp not available")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	video := makeMP4(t)
	mp4 := filepath.Join(t.TempDir(), "source.mp4")
	if err := os.WriteFile(mp4, video, 0o600); err != nil {
		t.Fatal(err)
	}
	mkv := filepath.Join(t.TempDir(), "source.mkv")
	if out, err := exec.CommandContext(t.Context(), "ffmpeg", "-nostdin", "-loglevel", "error", "-y",
		"-i", mp4, "-c", "copy", mkv).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg cannot generate Matroska: %v: %s", err, out)
	}
	matroska, err := os.ReadFile(mkv)
	if err != nil {
		t.Fatal(err)
	}

	var rejected atomicCount
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test-Token") != e2eToken {
			rejected.inc()
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/media/ep2.mp4", "/media/film.mp4":
			http.ServeContent(w, r, "video.mp4", time.Time{}, bytes.NewReader(video))
		case "/media/matroska":
			// Some sources use video/matroska instead of video/x-matroska.
			// An extensionless URL makes yt-dlp infer the extension from it.
			w.Header().Set("Content-Type", "video/matroska")
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(matroska))
		case "/media/blocked.matroska.exe":
			w.Header().Set("Content-Type", "video/matroska")
			http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(matroska))
		case "/subs/ep2.en.vtt", "/subs/film.en.vtt":
			w.Header().Set("Content-Type", "text/vtt")
			_, _ = io.WriteString(w, vttSample)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(media.Close)

	bus := events.NewBus()
	t.Cleanup(bus.Close)
	f := newFixture(t, func(o *Options) { o.Publisher = bus })
	f.cfg.Ytdlp.Binary = ytdlpBin
	f.cfg.Downloads.ExtraArgs = []string{"--ignore-config"}
	rn := runner.New(runner.Options{
		Config: f.cfg, Store: f.st, Publisher: bus, Processor: f.proc,
		PollInterval: time.Hour, ProgressInterval: 100 * time.Millisecond,
	})
	svc := service.New(f.st, f.cfg, service.WithPublisher(bus), service.WithController(rn))
	rn.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = rn.Shutdown(ctx)
	})
	srv := httptest.NewServer(api.New(api.Options{
		Config: f.cfg, Service: svc, Version: "test", Logger: f.proc.log,
		YtdlpVersion: func(context.Context, string) (string, error) { return "test", nil },
	}))
	t.Cleanup(srv.Close)

	call := func(method, path string, body any) (int, map[string]any) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			rd = bytes.NewReader(raw)
		}
		req, err := http.NewRequestWithContext(t.Context(), method, srv.URL+path, rd)
		if err != nil {
			t.Fatal(err)
		}
		if rd != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		var out map[string]any
		_ = json.Unmarshal(b, &out)
		return resp.StatusCode, out
	}
	hdr := map[string]string{"X-Test-Token": e2eToken}
	create := func(body map[string]any) string {
		t.Helper()
		code, out := call("POST", "/api/jobs", body)
		if code != http.StatusCreated && code != http.StatusOK {
			t.Fatalf("POST /api/jobs = %d %v", code, out)
		}
		return out["id"].(string)
	}
	waitStatus := func(id, status string) map[string]any {
		t.Helper()
		deadline := time.Now().Add(60 * time.Second)
		for {
			code, job := call("GET", "/api/jobs/"+id, nil)
			if code != http.StatusOK {
				t.Fatalf("GET job = %d", code)
			}
			if job["status"] == status {
				return job
			}
			switch job["status"] {
			case "completed", "failed", "cancelled":
				t.Fatalf("job %s: %v: %v", job["status"], job["error_code"], job["error_message"])
			}
			if time.Now().After(deadline) {
				t.Fatalf("timeout; job = %v", job)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}

	tvID := create(map[string]any{
		"url": media.URL + "/media/ep2.mp4", "category": "tv",
		"show_name": "Severance", "season": 1, "episode": 2, "title": "Half Loop", "year": 2022,
		"headers":   hdr,
		"subtitles": []map[string]string{{"url": media.URL + "/subs/ep2.en.vtt", "lang": "en"}},
	})
	movieID := create(map[string]any{
		"url": media.URL + "/media/film.mp4", "category": "movies",
		"title": "Dune Part Two", "year": 2024,
		"headers":   hdr,
		"subtitles": []map[string]string{{"url": media.URL + "/subs/film.en.vtt", "lang": "en"}},
	})
	matroskaID := create(map[string]any{
		"url": media.URL + "/media/matroska", "category": "movies",
		"title": "Matroska Film", "headers": hdr,
		"subtitles": []map[string]string{{"url": media.URL + "/subs/film.en.vtt", "lang": "en"}},
	})
	unsafeID := create(map[string]any{
		"url": media.URL + "/media/blocked.matroska.exe", "category": "movies",
		"title": "Unsafe Extension", "headers": hdr,
	})

	type want struct {
		id, root, rel string // rel: media path relative to the category root, no extension
		topDir        string // first folder under the root
		ext           string
		media         []byte
	}
	cases := []want{
		{tvID, f.root("tv"), filepath.Join("Severance (2022)", "Season 01", "Severance - S01E02 - Half Loop"), "Severance (2022)", ".mp4", video},
		{movieID, f.root("movies"), filepath.Join("Dune Part Two (2024)", "Dune Part Two (2024)"), "Dune Part Two (2024)", ".mp4", video},
		{matroskaID, f.root("movies"), filepath.Join("Matroska Film", "Matroska Film"), "Matroska Film", ".mkv", matroska},
	}
	for _, c := range cases {
		job := waitStatus(c.id, "completed")
		fp, _ := job["file_path"].(string)
		wantPath := filepath.Join(c.root, c.rel+c.ext)
		if fp != wantPath {
			t.Fatalf("file_path = %q, want %q", fp, wantPath)
		}
		if got := []byte(readFile(t, fp)); !bytes.Equal(got, c.media) {
			t.Errorf("media file differs from source: got %d bytes, want %d", len(got), len(c.media))
		}
		srt := readFile(t, filepath.Join(c.root, c.rel+".en.srt"))
		if !strings.Contains(srt, "00:00:01,000 --> 00:00:02,500") || strings.Contains(srt, "WEBVTT") {
			t.Errorf("sidecar is not SRT:\n%s", srt)
		}
		if exists(f.cfg.JobWorkDir(c.id)) {
			t.Errorf("work directory of %s kept", c.id)
		}
		p, _ := job["progress"].(map[string]any)
		if p["percent"] != 100.0 || p["speed_bps"] != 0.0 || p["eta_seconds"] != 0.0 {
			t.Errorf("progress = %v", p)
		}
		if w, _ := job["warnings"].([]any); len(w) != 0 {
			t.Errorf("warnings = %v", w)
		}
		if job["finished_at"] == nil {
			t.Error("finished_at not set")
		}
	}
	unsafe := waitStatus(unsafeID, "failed")
	message, _ := unsafe["error_message"].(string)
	if unsafe["error_code"] != "download_failed" || !strings.Contains(message, "extension ('exe')") {
		t.Errorf("unsafe extension should still be rejected: %v", unsafe)
	}
	if n := rejected.get(); n != 0 {
		t.Errorf("%d requests reached the server without the custom header", n)
	}

	for _, c := range cases {
		if code, out := call("DELETE", "/api/jobs/"+c.id+"?delete_file=true", nil); code != http.StatusNoContent {
			t.Fatalf("DELETE = %d %v", code, out)
		}
		if exists(filepath.Join(c.root, c.topDir)) {
			t.Errorf("folder %s not removed: %v", c.topDir, listFiles(t, c.root))
		}
		if !exists(c.root) {
			t.Errorf("category root %s removed", c.root)
		}
		if code, _ := call("GET", "/api/jobs/"+c.id, nil); code != http.StatusNotFound {
			t.Errorf("GET after delete = %d", code)
		}
	}
}
