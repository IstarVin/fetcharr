package process

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestPlacedFilesHaveReadableMode(t *testing.T) {
	needFFmpeg(t)
	exdev := func(oldpath, newpath string) error {
		if strings.HasSuffix(oldpath, ".partial") {
			return os.Rename(oldpath, newpath)
		}
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
	}
	for name, rename := range map[string]func(string, string) error{"rename": nil, "copy": exdev} {
		t.Run(name, func(t *testing.T) {
			srv := newSubServer(t, map[string]route{"/en.srt": {body: srtSample}, "/es.vtt": {body: vttSample}})
			f := newFixture(t, func(o *Options) { o.Rename = rename })
			j, media := f.job(withSubs(
				store.Subtitle{URL: srv.URL + "/en.srt", Lang: "en"},
				store.Subtitle{URL: srv.URL + "/es.vtt", Lang: "es"}))
			if err := os.Chmod(media, 0o600); err != nil { // e.g. yt-dlp under a strict umask
				t.Fatal(err)
			}
			if err := f.process(j, media); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(f.root("movies"), "Dune (2024)")
			for _, n := range []string{"Dune (2024).mp4", "Dune (2024).en.srt", "Dune (2024).es.srt"} {
				if m := mode(t, filepath.Join(dir, n)); m != 0o644 {
					t.Errorf("%s mode = %o, want 644", n, m)
				}
			}
		})
	}
}

func TestSubtitleGzipWithClientAcceptEncoding(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Accept-Encoding"))
		mu.Unlock()
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			var b bytes.Buffer
			zw := gzip.NewWriter(&b)
			_, _ = zw.Write([]byte(srtSample))
			_ = zw.Close()
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(b.Bytes())
			return
		}
		_, _ = io.WriteString(w, srtSample)
	}))
	t.Cleanup(srv.Close)
	f := newFixture(t, nil)
	j, media := f.job(func(j *store.Job) {
		j.Headers = map[string]string{"accept-encoding": "br, gzip", "X-Other": "1"}
		j.Subtitles = []store.Subtitle{{URL: srv.URL + "/en.srt", Lang: "en"}}
	})
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	got := f.get(j.ID)
	if len(got.Warnings) != 0 {
		t.Fatalf("warnings = %q (seen %q)", got.Warnings, seen)
	}
	sidecar := filepath.Join(f.root("movies"), "Dune (2024)", "Dune (2024).en.srt")
	if readFile(t, sidecar) != srtSample {
		t.Errorf("sidecar = %q, want decoded SRT", readFile(t, sidecar))
	}
	for _, ae := range seen {
		if strings.Contains(ae, "br") {
			t.Errorf("client Accept-Encoding forwarded: %q", ae)
		}
	}
}

func TestContainmentBeforeExistingFileCheck(t *testing.T) {
	f := newFixture(t, nil)
	outside := filepath.Join(t.TempDir(), "outside")
	writeFile(t, filepath.Join(outside, "Dune (2024).mkv"), "x") // would match as an existing file
	if err := os.Symlink(outside, filepath.Join(f.root("movies"), "Dune (2024)")); err != nil {
		t.Fatal(err)
	}
	j, media := f.job()
	err := f.process(j, media)
	if code := failureCode(t, err); code != runner.CodeProcessingFailed {
		t.Fatalf("code = %s (%v), want processing_failed", code, err)
	}
	if strings.Contains(err.Error(), outside) && strings.Contains(err.Error(), "already exists") {
		t.Errorf("message names the outside file: %v", err)
	}
}

func TestPlantedPartialSymlinkIsNotFollowed(t *testing.T) {
	victim := filepath.Join(t.TempDir(), "victim")
	writeFile(t, victim, "precious")
	f := newFixture(t, func(o *Options) {
		o.Rename = func(oldpath, newpath string) error {
			if strings.HasSuffix(oldpath, ".partial") {
				return os.Rename(oldpath, newpath)
			}
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
		}
	})
	dir := filepath.Join(f.root("movies"), "Dune (2024)")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "Dune (2024).mp4.partial")); err != nil {
		t.Fatal(err)
	}
	j, media := f.job()
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	if readFile(t, victim) != "precious" {
		t.Fatal("symlink target overwritten")
	}
	if got := readFile(t, filepath.Join(dir, "Dune (2024).mp4")); got != "video-bytes" {
		t.Errorf("media = %q", got)
	}
	if exists(filepath.Join(dir, "Dune (2024).mp4.partial")) {
		t.Error(".partial left behind")
	}
}

func TestFFmpegArgumentsAreHardened(t *testing.T) {
	srv := newSubServer(t, map[string]route{"/en.vtt": {body: vttSample}, "/x.ass": {body: "[Script Info]\nTitle: x\n"}})
	argsFile := filepath.Join(t.TempDir(), "args")
	// Records its arguments and fails; the conversion result is irrelevant.
	ff := script(t, `echo "$@" >> "`+argsFile+`"; exit 1`)
	f := newFixture(t, func(o *Options) { o.FFmpeg = ff })
	j, media := f.job(withSubs(
		store.Subtitle{URL: srv.URL + "/en.vtt", Lang: "en"},
		store.Subtitle{URL: srv.URL + "/x.ass", Lang: "ja"}))
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(readFile(t, argsFile)), "\n")
	if len(lines) != 2 {
		t.Fatalf("ffmpeg calls: %q", lines)
	}
	for i, demuxer := range []string{"webvtt", "ass"} {
		a := lines[i]
		pw, fm, in := strings.Index(a, "-protocol_whitelist file"), strings.Index(a, "-f "+demuxer+" "), strings.Index(a, "-i ")
		if pw < 0 || fm < 0 || in < 0 || pw > in || fm > in {
			t.Errorf("args %d = %q: want -protocol_whitelist file and -f %s before -i", i, a, demuxer)
		}
	}
}

func TestHostileSubtitleContentIsNotProbed(t *testing.T) {
	needFFmpeg(t)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, secret, "TOP-SECRET-CONTENT")
	srv := newSubServer(t, map[string]route{
		"/a.vtt": {body: "ffconcat version 1.0\nfile " + secret + "\n"},
		"/b.xyz": {body: "#EXTM3U\n" + secret + "\n"},
	})
	f := newFixture(t, nil)
	j, media := f.job(withSubs(
		store.Subtitle{URL: srv.URL + "/a.vtt", Lang: "en"},
		store.Subtitle{URL: srv.URL + "/b.xyz", Lang: "fr"}))
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	got := f.get(j.ID)
	if got.Status != jobs.StatusCompleted || len(got.Warnings) != 2 {
		t.Fatalf("status %s warnings %q", got.Status, got.Warnings)
	}
	if !strings.Contains(got.Warnings[1], "unsupported subtitle format") {
		t.Errorf("warning = %q", got.Warnings[1])
	}
	for _, name := range listFiles(t, f.root("movies")) {
		if strings.HasSuffix(name, ".srt") {
			t.Errorf("sidecar %s created from hostile content", name)
		}
	}
}

func TestRedirectDropsHeadersOnOtherHost(t *testing.T) {
	var mu sync.Mutex
	var other, same []http.Header
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		other = append(other, r.Header.Clone())
		mu.Unlock()
		_, _ = io.WriteString(w, srtSample)
	}))
	t.Cleanup(target.Close)
	var origin *httptest.Server
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cross":
			http.Redirect(w, r, target.URL+"/final.srt", http.StatusFound)
		case "/local":
			http.Redirect(w, r, origin.URL+"/final.srt", http.StatusFound)
		default:
			mu.Lock()
			same = append(same, r.Header.Clone())
			mu.Unlock()
			_, _ = io.WriteString(w, srtSample)
		}
	}))
	t.Cleanup(origin.Close)

	f := newFixture(t, nil)
	j, media := f.job(func(j *store.Job) {
		j.Headers = map[string]string{"X-Test-Token": "secret-token", "Referer": "https://site.example/"}
		j.Subtitles = []store.Subtitle{
			{URL: origin.URL + "/cross", Lang: "en"},
			{URL: origin.URL + "/local", Lang: "fr"},
		}
	})
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	if w := f.get(j.ID).Warnings; len(w) != 0 {
		t.Fatalf("warnings = %q", w)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(other) != 1 || len(same) != 1 {
		t.Fatalf("requests: other %d, same %d", len(other), len(same))
	}
	for _, h := range []string{"X-Test-Token", "Referer"} {
		if other[0].Get(h) != "" {
			t.Errorf("header %s forwarded to another host", h)
		}
		if same[0].Get(h) == "" {
			t.Errorf("header %s dropped on a same-host redirect", h)
		}
	}
}
