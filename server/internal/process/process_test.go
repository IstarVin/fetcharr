package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func withSubs(subs ...store.Subtitle) func(*store.Job) {
	return func(j *store.Job) { j.Subtitles = subs }
}

func failureCode(t *testing.T, err error) string {
	t.Helper()
	var f *runner.Failure
	if !errors.As(err, &f) {
		t.Fatalf("error %v is not a *runner.Failure", err)
	}
	return f.Code
}

func TestCompleteWithSRTPassthrough(t *testing.T) {
	srv := newSubServer(t, map[string]route{"/en.srt": {body: srtSample}})
	var gotDir atomic.Value
	done := make(chan struct{})
	f := newFixture(t, func(o *Options) {
		o.OnCompleted = func(_ *store.Job, dir string) {
			gotDir.Store(dir)
			close(done)
		}
	})
	j, media := f.job(withSubs(store.Subtitle{URL: srv.URL + "/en.srt", Lang: "en"}))

	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.root("movies"), "Dune (2024)")
	if got := readFile(t, filepath.Join(dir, "Dune (2024).mp4")); got != "video-bytes" {
		t.Errorf("media content = %q", got)
	}
	if got := readFile(t, filepath.Join(dir, "Dune (2024).en.srt")); got != srtSample {
		t.Errorf("subtitle content = %q", got)
	}
	if exists(f.cfg.JobWorkDir(j.ID)) {
		t.Error("work directory not deleted")
	}

	got := f.get(j.ID)
	if got.Status != jobs.StatusCompleted {
		t.Fatalf("status = %s", got.Status)
	}
	if got.FilePath == nil || *got.FilePath != filepath.Join(dir, "Dune (2024).mp4") {
		t.Errorf("file_path = %v", got.FilePath)
	}
	if got.Placing {
		t.Error("placing still set")
	}
	if got.FinishedAt == nil {
		t.Error("finished_at not set")
	}
	if len(got.Warnings) != 0 {
		t.Errorf("warnings = %v", got.Warnings)
	}
	p := got.Progress
	if p == nil || p.Percent == nil || *p.Percent != 100 || p.SpeedBps == nil || *p.SpeedBps != 0 ||
		p.ETASeconds == nil || *p.ETASeconds != 0 || p.TotalBytes == nil || *p.TotalBytes != int64(len("video-bytes")) {
		t.Errorf("progress = %+v", p)
	}
	if st := f.pub.statuses(j.ID); !slices.Equal(st, []jobs.Status{jobs.StatusCompleted}) {
		t.Errorf("published statuses = %v", st)
	}
	if !strings.Contains(f.logs.String(), `msg="job completed"`) {
		t.Errorf("no 'job completed' log:\n%s", f.logs)
	}

	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("OnCompleted not called")
	}
	if gotDir.Load() != dir {
		t.Errorf("refresh dir = %v, want %s", gotDir.Load(), dir)
	}
}

func TestLayoutsAndSubtitleNames(t *testing.T) {
	srv := newSubServer(t, map[string]route{
		"/a.srt": {body: srtSample}, "/b.srt": {body: srtSample}, "/c.srt": {body: srtSample},
	})
	f := newFixture(t, nil)
	j, media := f.job(func(j *store.Job) {
		j.Category = "tv"
		j.Title, j.Year = store.Ptr("Hello, Ms. Cobel"), store.Ptr(2022)
		j.ShowName, j.Season, j.Episode = store.Ptr("Severance"), store.Ptr(2), store.Ptr(1)
		j.Subtitles = []store.Subtitle{
			{URL: srv.URL + "/a.srt", Lang: "en"},
			{URL: srv.URL + "/b.srt", Lang: "en"},
			{URL: srv.URL + "/c.srt", Lang: ""},
		}
	})
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	got := listFiles(t, f.root("tv"))
	slices.Sort(got)
	base := filepath.Join("Severance (2022)", "Season 02", "Severance - S02E01 - Hello, Ms. Cobel")
	want := []string{base + ".en.2.srt", base + ".en.srt", base + ".mp4", base + ".und.srt"}
	if !slices.Equal(got, want) {
		t.Errorf("files = %v\nwant %v", got, want)
	}
	if rp := f.get(j.ID).FilePath; rp == nil || *rp != filepath.Join(f.root("tv"), base+".mp4") {
		t.Errorf("file_path = %v", rp)
	}
}

func TestFlatLayoutRefreshDirIsRoot(t *testing.T) {
	var dir atomic.Value
	done := make(chan struct{})
	f := newFixture(t, func(o *Options) {
		o.OnCompleted = func(_ *store.Job, d string) { dir.Store(d); close(done) }
	})
	j, media := f.job(func(j *store.Job) { j.Category, j.Title, j.Year = "misc", store.Ptr("Conference Talk"), nil })
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	<-done
	if !exists(filepath.Join(f.root("misc"), "Conference Talk.mp4")) || dir.Load() != f.root("misc") {
		t.Errorf("files %v, refresh dir %v", listFiles(t, f.root("misc")), dir.Load())
	}
}

func TestVTTConvertedToSRT(t *testing.T) {
	needFFmpeg(t)
	srv := newSubServer(t, map[string]route{"/en.vtt": {body: vttSample}})
	f := newFixture(t, nil)
	j, media := f.job(withSubs(store.Subtitle{URL: srv.URL + "/en.vtt", Lang: "en"}))
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(f.root("movies"), "Dune (2024)", "Dune (2024).en.srt"))
	if !strings.Contains(got, "00:00:01,000 --> 00:00:02,500") || !strings.Contains(got, "Hello") || strings.Contains(got, "WEBVTT") {
		t.Errorf("not converted to SRT:\n%s", got)
	}
	if w := f.get(j.ID).Warnings; len(w) != 0 {
		t.Errorf("warnings = %v", w)
	}
}

func TestSubtitleFailuresAreWarnings(t *testing.T) {
	srv := newSubServer(t, map[string]route{
		"/en.srt":    {body: srtSample},
		"/empty.srt": {status: 200, body: "  \n"},
		"/boom":      {status: 500},
	})
	closed := newSubServer(t, nil)
	closedURL := closed.URL
	closed.Close()

	f := newFixture(t, func(o *Options) { o.MaxSubtitleBytes = 1000 })
	big := newSubServer(t, map[string]route{"/big.srt": {body: strings.Repeat("x", 2000)}})
	j, media := f.job(func(j *store.Job) {
		j.Headers = map[string]string{"Authorization": "Bearer s3cr3t"}
		j.Subtitles = []store.Subtitle{
			{URL: srv.URL + "/missing.srt?token=QUERYSECRET#frag", Lang: "es"},
			{URL: srv.URL + "/en.srt", Lang: "en"},
			{URL: srv.URL + "/empty.srt", Lang: "fr"},
			{URL: srv.URL + "/boom", Lang: "de"},
			{URL: closedURL + "/gone.srt?sig=ABC", Lang: "it"},
			{URL: big.URL + "/big.srt", Lang: "pt"},
		}
	})
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	got := f.get(j.ID)
	if got.Status != jobs.StatusCompleted {
		t.Fatalf("status = %s", got.Status)
	}
	files := listFiles(t, f.root("movies"))
	slices.Sort(files)
	base := filepath.Join("Dune (2024)", "Dune (2024)")
	if want := []string{base + ".en.srt", base + ".mp4"}; !slices.Equal(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}
	want := []string{
		"subtitle es (" + srv.URL + "/missing.srt): download failed: HTTP 404",
		"subtitle fr (" + srv.URL + "/empty.srt): download failed: empty file",
		"subtitle de (" + srv.URL + "/boom): download failed: HTTP 500",
		"subtitle pt (" + big.URL + "/big.srt): download failed: larger than 1000 bytes",
	}
	for _, w := range want {
		if !slices.Contains(got.Warnings, w) {
			t.Errorf("missing warning %q in %q", w, got.Warnings)
		}
	}
	if len(got.Warnings) != 5 {
		t.Errorf("want 5 warnings, got %q", got.Warnings)
	}
	all := strings.Join(got.Warnings, "\n")
	for _, secret := range []string{"QUERYSECRET", "sig=ABC", "s3cr3t", "frag", "?"} {
		if strings.Contains(all, secret) {
			t.Errorf("warnings leak %q: %s", secret, all)
		}
	}
	if !strings.Contains(all, "subtitle it ("+closedURL+"/gone.srt): download failed:") {
		t.Errorf("transport error warning missing: %s", all)
	}
}

func TestSubtitleRequestsCarryJobHeaders(t *testing.T) {
	srv := newSubServer(t, map[string]route{"/en.srt": {body: srtSample}})
	f := newFixture(t, nil)
	j, media := f.job(func(j *store.Job) {
		j.Headers = map[string]string{"Authorization": "Bearer abc", "X-Custom": "v", "Referer": "https://site.example/"}
		j.Subtitles = []store.Subtitle{{URL: srv.URL + "/en.srt", Lang: "en"}}
	})
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	hs := srv.headers()
	if len(hs) != 1 {
		t.Fatalf("requests = %d", len(hs))
	}
	for k, v := range map[string]string{"Authorization": "Bearer abc", "X-Custom": "v", "Referer": "https://site.example/"} {
		if hs[0].Get(k) != v {
			t.Errorf("header %s = %q, want %q", k, hs[0].Get(k), v)
		}
	}
	if strings.Contains(f.logs.String(), "Bearer abc") {
		t.Error("header value in logs")
	}
}

func TestConversionFailureWarningIsRedacted(t *testing.T) {
	srv := newSubServer(t, map[string]route{"/en.vtt": {body: vttSample}})
	ff := script(t, `echo "Invalid data found: Bearer s3cr3t at http://cdn.example/x.vtt?token=abc" >&2; exit 1`)
	f := newFixture(t, func(o *Options) { o.FFmpeg = ff })
	j, media := f.job(func(j *store.Job) {
		j.Headers = map[string]string{"Authorization": "Bearer s3cr3t"}
		j.Subtitles = []store.Subtitle{{URL: srv.URL + "/en.vtt", Lang: "en"}}
	})
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	got := f.get(j.ID)
	if got.Status != jobs.StatusCompleted || len(got.Warnings) != 1 {
		t.Fatalf("status %s, warnings %q", got.Status, got.Warnings)
	}
	w := got.Warnings[0]
	if !strings.HasPrefix(w, "subtitle en ("+srv.URL+"/en.vtt): conversion failed: ") ||
		strings.Contains(w, "s3cr3t") || strings.Contains(w, "token=abc") || !strings.Contains(w, "[redacted]") {
		t.Errorf("warning = %q", w)
	}
}

func TestMissingFFmpegIsAWarning(t *testing.T) {
	srv := newSubServer(t, map[string]route{"/en.vtt": {body: vttSample}})
	f := newFixture(t, func(o *Options) { o.FFmpeg = "/nonexistent/ffmpeg" })
	j, media := f.job(withSubs(store.Subtitle{URL: srv.URL + "/en.vtt", Lang: "en"}))
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	got := f.get(j.ID)
	if got.Status != jobs.StatusCompleted || len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "conversion failed") {
		t.Errorf("status %s warnings %q", got.Status, got.Warnings)
	}
}

func TestSniff(t *testing.T) {
	for name, c := range map[string]struct {
		in   string
		want subFormat
	}{
		"srt":       {srtSample, formatSRT},
		"srt bom":   {"\xef\xbb\xbf" + srtSample, formatSRT},
		"srt crlf":  {strings.ReplaceAll(srtSample, "\n", "\r\n"), formatSRT},
		"vtt":       {vttSample, formatVTT},
		"vtt bom":   {"\xef\xbb\xbf" + vttSample, formatVTT},
		"ass":       {"[Script Info]\nTitle: x\n", formatASS},
		"ttml":      {`<tt xmlns="http://www.w3.org/ns/ttml"><body/></tt>`, formatOther},
		"html page": {"<html>Not found</html>", formatOther},
	} {
		if got := sniff([]byte(c.in)); got != c.want {
			t.Errorf("%s: sniff = %v, want %v", name, got, c.want)
		}
	}
}

func TestSecondExistingFileCheck(t *testing.T) {
	f := newFixture(t, nil)
	existing := filepath.Join(f.root("movies"), "Dune (2024)", "Dune (2024).mkv")
	writeFile(t, existing, "old")
	j, media := f.job()

	err := f.process(j, media)
	if code := failureCode(t, err); code != runner.CodeAlreadyExists {
		t.Fatalf("code = %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), existing) {
		t.Errorf("message %q does not name the file", err)
	}
	if readFile(t, existing) != "old" || !exists(media) {
		t.Error("files touched")
	}
	if got := f.get(j.ID); got.Placing || got.Status != jobs.StatusProcessing {
		t.Errorf("status %s placing %v", got.Status, got.Placing)
	}
}

func TestOverwriteReplacesMediaAndSidecars(t *testing.T) {
	srv := newSubServer(t, map[string]route{"/en.srt": {body: srtSample}})
	f := newFixture(t, nil)
	dir := filepath.Join(f.root("movies"), "Dune (2024)")
	writeFile(t, filepath.Join(dir, "Dune (2024).mp4"), "old mp4")
	writeFile(t, filepath.Join(dir, "Dune (2024).mkv"), "old mkv")
	writeFile(t, filepath.Join(dir, "Dune (2024).en.srt"), "old en")
	writeFile(t, filepath.Join(dir, "Dune (2024).fr.srt"), "old fr")
	writeFile(t, filepath.Join(dir, "Dune (2024) Extras.mkv"), "other file")
	j, media := f.job(func(j *store.Job) {
		j.Overwrite = true
		j.Subtitles = []store.Subtitle{{URL: srv.URL + "/en.srt", Lang: "en"}}
	})
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	files := listFiles(t, dir)
	slices.Sort(files)
	if want := []string{"Dune (2024) Extras.mkv", "Dune (2024).en.srt", "Dune (2024).mp4"}; !slices.Equal(files, want) {
		t.Errorf("files = %v, want %v", files, want)
	}
	if readFile(t, filepath.Join(dir, "Dune (2024).mp4")) != "video-bytes" || readFile(t, filepath.Join(dir, "Dune (2024).en.srt")) != srtSample {
		t.Error("files not replaced")
	}
	if f.get(j.ID).Status != jobs.StatusCompleted {
		t.Error("not completed")
	}
}

func TestCrossFilesystemCopiesThroughPartial(t *testing.T) {
	var sawPartial atomic.Bool
	srv := newSubServer(t, map[string]route{"/en.srt": {body: srtSample}})
	f := newFixture(t, func(o *Options) {
		o.Rename = func(oldpath, newpath string) error {
			if strings.HasSuffix(oldpath, ".partial") {
				// The library holds only the finished rename target.
				if b, err := os.ReadFile(oldpath); err == nil && len(b) > 0 && !exists(newpath) {
					sawPartial.Store(true)
				}
				return os.Rename(oldpath, newpath)
			}
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
		}
	})
	j, media := f.job(withSubs(store.Subtitle{URL: srv.URL + "/en.srt", Lang: "en"}))
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.root("movies"), "Dune (2024)")
	files := listFiles(t, dir)
	slices.Sort(files)
	if want := []string{"Dune (2024).en.srt", "Dune (2024).mp4"}; !slices.Equal(files, want) {
		t.Errorf("files = %v (a .partial must not remain)", files)
	}
	if readFile(t, filepath.Join(dir, "Dune (2024).mp4")) != "video-bytes" {
		t.Error("content differs")
	}
	if !sawPartial.Load() {
		t.Error("copy did not go through a .partial file")
	}
	if f.get(j.ID).Status != jobs.StatusCompleted {
		t.Error("not completed")
	}
}

func TestCrossFilesystemCancelledCopyRollsBack(t *testing.T) {
	f := newFixture(t, func(o *Options) {
		o.Rename = func(oldpath, newpath string) error {
			if strings.HasSuffix(oldpath, ".partial") {
				return os.Rename(oldpath, newpath)
			}
			return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
		}
	})
	_, media := f.job()
	wd := filepath.Dir(media)
	sub := filepath.Join(wd, "subtitle-0.srt")
	writeFile(t, sub, srtSample)

	tgt, err := f.proc.targetFor("movies", "Dune", 2024)
	if err != nil {
		t.Fatal(err)
	}
	// The subtitle copies (ctx is live for the first read), the media copy
	// then sees a cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	n := 0
	f.proc.rename = func(oldpath, newpath string) error {
		if strings.HasSuffix(oldpath, ".partial") {
			n++
			if n == 1 {
				cancel() // the subtitle is placed; the media copy must stop
			}
			return os.Rename(oldpath, newpath)
		}
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: syscall.EXDEV}
	}
	err = f.proc.place(ctx, tgt, media, []subtitle{{path: sub, lang: "en"}}, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if files := listFiles(t, tgt.Dir); len(files) != 0 {
		t.Errorf("library not rolled back: %v", files)
	}
	if !exists(media) {
		t.Error("media removed from the work directory")
	}
}

func TestPlacementFailureKeepsWorkDirAndRollsBack(t *testing.T) {
	srv := newSubServer(t, map[string]route{"/en.srt": {body: srtSample}})
	f := newFixture(t, func(o *Options) {
		o.Rename = func(oldpath, newpath string) error {
			if strings.HasSuffix(newpath, ".mp4") {
				return errors.New("disk on fire")
			}
			return os.Rename(oldpath, newpath)
		}
	})
	j, media := f.job(withSubs(store.Subtitle{URL: srv.URL + "/en.srt", Lang: "en"}))
	err := f.process(j, media)
	if code := failureCode(t, err); code != runner.CodeProcessingFailed || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("err = %v", err)
	}
	if files := listFiles(t, f.root("movies")); len(files) != 0 {
		t.Errorf("library has %v", files)
	}
	if !exists(media) {
		t.Error("work directory not kept")
	}
	if got := f.get(j.ID); got.Placing || got.Status != jobs.StatusProcessing {
		t.Errorf("status %s placing %v (runner fails the job; placing must be cleared)", got.Status, got.Placing)
	}
}

func TestContainmentRejectsSymlinkedFolder(t *testing.T) {
	cases := map[string]struct {
		setup func(root, outside string) error
		job   func(*store.Job)
	}{
		"movie folder is a symlink": {
			setup: func(root, outside string) error { return os.Symlink(outside, filepath.Join(root, "Dune (2024)")) },
		},
		"tv show folder is a symlink, season missing": {
			setup: func(root, outside string) error { return os.Symlink(outside, filepath.Join(root, "Show (2020)")) },
			job: func(j *store.Job) {
				j.Category, j.ShowName, j.Season, j.Episode, j.Year = "tv", store.Ptr("Show"), store.Ptr(1), store.Ptr(2), store.Ptr(2020)
			},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, nil)
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			cat := "movies"
			if c.job != nil {
				cat = "tv"
			}
			if err := c.setup(f.root(cat), outside); err != nil {
				t.Fatal(err)
			}
			mods := []func(*store.Job){}
			if c.job != nil {
				mods = append(mods, c.job)
			}
			j, media := f.job(mods...)
			err := f.process(j, media)
			if code := failureCode(t, err); code != runner.CodeProcessingFailed || !strings.Contains(err.Error(), "outside") {
				t.Fatalf("err = %v", err)
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("files written outside the root: %v", entries)
			}
			if !exists(media) || f.get(j.ID).Placing {
				t.Error("work dir must be kept and placing never set")
			}
		})
	}
}

func TestShutdownBeforePlacingReturnsContextError(t *testing.T) {
	f := newFixture(t, nil)
	j, media := f.job()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := f.proc.Process(ctx, j, media)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if files := listFiles(t, f.root("movies")); len(files) != 0 || !exists(media) || f.get(j.ID).Placing {
		t.Error("state changed")
	}
}

func TestCancelWonBeforePlacingAbortsSilently(t *testing.T) {
	f := newFixture(t, nil)
	j, media := f.job()
	// A cancel committed after the processor read its row.
	if _, err := f.st.Update(context.Background(), j.ID, store.Update{Status: store.Set(jobs.StatusCancelled)}); err != nil {
		t.Fatal(err)
	}
	if err := f.process(j, media); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if files := listFiles(t, f.root("movies")); len(files) != 0 || !exists(media) {
		t.Errorf("files moved: %v", files)
	}
	if got := f.get(j.ID); got.Status != jobs.StatusCancelled || got.Placing {
		t.Errorf("status %s placing %v", got.Status, got.Placing)
	}
}

func TestOnCompletedPanicIsContained(t *testing.T) {
	done := make(chan struct{})
	f := newFixture(t, func(o *Options) {
		o.OnCompleted = func(*store.Job, string) {
			defer close(done)
			panic("boom")
		}
	})
	j, media := f.job()
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	<-done
	time.Sleep(50 * time.Millisecond)
	if !strings.Contains(f.logs.String(), "completion hook panicked") {
		t.Error("panic not logged")
	}
}

func TestCompletionLogsNeverContainHeadersOrQuery(t *testing.T) {
	f := newFixture(t, nil)
	j, media := f.job(func(j *store.Job) {
		j.URL = "https://example.com/v?token=SECRETQ"
		j.Headers = map[string]string{"Cookie": "sid=SECRETC"}
	})
	if err := f.process(j, media); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"SECRETQ", "SECRETC"} {
		if strings.Contains(f.logs.String(), s) {
			t.Errorf("log leaks %s:\n%s", s, f.logs)
		}
	}
}
