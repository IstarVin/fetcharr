package process

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// stack is the fixture plus a service and a runner (fake yt-dlp) whose
// Processor is the one under test.
type stack struct {
	*fixture
	svc *service.Service
	rn  *runner.Runner
}

func newStack(t *testing.T, mod func(*Options)) *stack {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	t.Setenv("FAKE_YTDLP_DURATION", "0")
	bin, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake-ytdlp"))
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, mod)
	f.cfg.Ytdlp.Binary = bin
	rn := runner.New(runner.Options{
		Config: f.cfg, Store: f.st, Publisher: f.pub, Processor: f.proc,
		Binary: bin, PollInterval: time.Hour, KillGrace: 2 * time.Second,
		ProgressInterval: 20 * time.Millisecond,
	})
	svc := service.New(f.st, f.cfg, service.WithPublisher(f.pub), service.WithController(rn))
	rn.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = rn.Shutdown(ctx)
	})
	return &stack{fixture: f, svc: svc, rn: rn}
}

func (s *stack) wait(id, what string, ok func(*store.Job) bool) *store.Job {
	s.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		j := s.get(id)
		if ok(j) {
			return j
		}
		if time.Now().After(deadline) {
			msg := ""
			if j.ErrorMessage != nil {
				msg = *j.ErrorMessage
			}
			s.t.Fatalf("timeout waiting for %s: status %s, error %q", what, j.Status, msg)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *stack) waitStatus(id string, st jobs.Status) *store.Job {
	s.t.Helper()
	return s.wait(id, string(st), func(j *store.Job) bool { return j.Status == st })
}

func waitFile(t *testing.T, p string) string {
	t.Helper()
	deadline := time.Now().Add(waitTimeout)
	for {
		if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
			return strings.TrimSpace(string(b))
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", p)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitDead(t *testing.T, pidText string) {
	t.Helper()
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(waitTimeout)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("process %d still running", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *stack) createMovie(subs ...service.SubtitleInput) *store.Job {
	s.t.Helper()
	j, err := s.svc.Create(context.Background(), service.CreateRequest{
		Category: "movies", URL: "https://example.com/dune", Title: "Dune", Year: store.Ptr(2024), Subtitles: subs,
	})
	if err != nil {
		s.t.Fatal(err)
	}
	return j
}

func TestEndToEndWithFakeYtdlp(t *testing.T) {
	needFFmpeg(t)
	srv := newSubServer(t, map[string]route{"/en.vtt": {body: vttSample}})
	s := newStack(t, nil)
	j := s.createMovie(service.SubtitleInput{URL: srv.URL + "/en.vtt", Lang: "en"},
		service.SubtitleInput{URL: srv.URL + "/es.vtt", Lang: "es"})
	got := s.waitStatus(j.ID, jobs.StatusCompleted)

	dir := filepath.Join(s.root("movies"), "Dune (2024)")
	if got.FilePath == nil || *got.FilePath != filepath.Join(dir, "Dune (2024).mp4") || !exists(*got.FilePath) {
		t.Fatalf("file_path = %v", got.FilePath)
	}
	if !exists(filepath.Join(dir, "Dune (2024).en.srt")) || exists(filepath.Join(dir, "Dune (2024).es.srt")) {
		t.Errorf("files = %v", listFiles(t, dir))
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "subtitle es") || !strings.Contains(got.Warnings[0], "HTTP 404") {
		t.Errorf("warnings = %q", got.Warnings)
	}
	if exists(s.cfg.JobWorkDir(j.ID)) {
		t.Error("work directory kept")
	}
}

// D7 (a): a cancel while ffmpeg converts a subtitle works: ffmpeg is killed,
// the work directory is deleted and nothing reaches the library.
func TestCancelDuringSubtitleConversion(t *testing.T) {
	srv := newSubServer(t, map[string]route{"/en.vtt": {body: vttSample}})
	marks := t.TempDir()
	ff := script(t, `echo $$ > "`+marks+`/ffmpeg.pid"
sleep 300 &
echo $! > "`+marks+`/child.pid"
wait`)
	s := newStack(t, func(o *Options) { o.FFmpeg = ff; o.KillGrace = time.Second })
	j := s.createMovie(service.SubtitleInput{URL: srv.URL + "/en.vtt", Lang: "en"})

	pid := waitFile(t, filepath.Join(marks, "ffmpeg.pid"))
	child := waitFile(t, filepath.Join(marks, "child.pid"))
	if got := s.get(j.ID); got.Status != jobs.StatusProcessing || got.Placing {
		t.Fatalf("status %s placing %v", got.Status, got.Placing)
	}
	if _, err := s.svc.Cancel(context.Background(), j.ID); err != nil {
		t.Fatalf("cancel during conversion: %v", err)
	}
	waitDead(t, pid)
	waitDead(t, child)
	deadline := time.Now().Add(waitTimeout)
	for exists(s.cfg.JobWorkDir(j.ID)) {
		if time.Now().After(deadline) {
			t.Fatal("work directory not deleted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := s.get(j.ID); got.Status != jobs.StatusCancelled {
		t.Errorf("status = %s", got.Status)
	}
	if files := listFiles(t, s.media); len(files) != 0 {
		t.Errorf("files in the library: %v", files)
	}
}

// D7 (b): once placement started, cancel is refused with invalid_state and
// the job completes.
func TestCancelDuringPlacementIsRefused(t *testing.T) {
	s := newStack(t, nil)
	reached, release := make(chan struct{}), make(chan struct{})
	s.proc.afterSetPlacing = func() {
		close(reached)
		<-release
	}
	j := s.createMovie()
	select {
	case <-reached:
	case <-time.After(waitTimeout):
		t.Fatal("placement never started")
	}
	_, err := s.svc.Cancel(context.Background(), j.ID)
	if code, ok := service.CodeOf(err); !ok || code != service.CodeInvalidState {
		t.Fatalf("cancel during placement: %v, want invalid_state", err)
	}
	if got := s.get(j.ID); got.Status != jobs.StatusProcessing || !got.Placing {
		t.Fatalf("status %s placing %v", got.Status, got.Placing)
	}
	close(release)
	got := s.waitStatus(j.ID, jobs.StatusCompleted)
	if got.FilePath == nil || !exists(*got.FilePath) {
		t.Errorf("file_path = %v", got.FilePath)
	}
}

// A retry with overwrite replaces the file that made the first run fail with
// already_exists; delete_file then removes file and folders.
func TestRetryWithOverwriteAndDeleteFile(t *testing.T) {
	s := newStack(t, nil)
	dir := filepath.Join(s.root("movies"), "Dune (2024)")
	old := filepath.Join(dir, "Dune (2024).mkv")
	writeFile(t, old, "old")

	j := s.createMovie()
	failed := s.waitStatus(j.ID, jobs.StatusFailed)
	if failed.ErrorCode == nil || *failed.ErrorCode != runner.CodeAlreadyExists {
		t.Fatalf("error_code = %v", failed.ErrorCode)
	}
	if _, err := s.svc.Retry(context.Background(), j.ID, service.RetryOptions{Overwrite: true}); err != nil {
		t.Fatal(err)
	}
	got := s.waitStatus(j.ID, jobs.StatusCompleted)
	if got.FilePath == nil || *got.FilePath != filepath.Join(dir, "Dune (2024).mp4") || !exists(*got.FilePath) {
		t.Fatalf("file_path = %v", got.FilePath)
	}
	if exists(old) {
		t.Error("old file with another extension kept")
	}

	// delete without the flag keeps the file.
	if err := s.svc.Delete(context.Background(), j.ID, service.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if !exists(*got.FilePath) {
		t.Fatal("file deleted without delete_file")
	}
}

func TestDeleteFileThroughService(t *testing.T) {
	s := newStack(t, nil)
	j := s.createMovie()
	got := s.waitStatus(j.ID, jobs.StatusCompleted)
	sidecar := filepath.Join(s.root("movies"), "Dune (2024)", "Dune (2024).en.srt")
	writeFile(t, sidecar, srtSample)

	// A second job's folder must survive.
	other := filepath.Join(s.root("movies"), "Other (2000)", "Other (2000).mp4")
	writeFile(t, other, "x")

	if err := s.svc.Delete(context.Background(), j.ID, service.DeleteOptions{DeleteFile: true}); err != nil {
		t.Fatal(err)
	}
	if exists(*got.FilePath) || exists(sidecar) || exists(filepath.Dir(*got.FilePath)) {
		t.Errorf("files left: %v", listFiles(t, s.root("movies")))
	}
	if !exists(other) || !exists(s.root("movies")) {
		t.Error("other files or the category root removed")
	}
	if _, err := s.st.Get(context.Background(), j.ID); err == nil {
		t.Error("record kept")
	}
}

func TestDeleteFileIgnoredForFailedAndMissingFileIsFine(t *testing.T) {
	f := newFixture(t, nil)
	svc := service.New(f.st, f.cfg)
	keep := filepath.Join(f.root("movies"), "Dune (2024)", "Dune (2024).mp4")
	writeFile(t, keep, "x")

	failed, _ := f.job(func(j *store.Job) { j.Status = jobs.StatusFailed; j.FilePath = &keep })
	if err := svc.Delete(context.Background(), failed.ID, service.DeleteOptions{DeleteFile: true}); err != nil {
		t.Fatal(err)
	}
	if !exists(keep) {
		t.Fatal("file of a failed job deleted")
	}

	gone := filepath.Join(f.root("movies"), "Gone (2000)", "Gone (2000).mp4")
	done, _ := f.job(func(j *store.Job) {
		j.Status = jobs.StatusCompleted
		j.FilePath = &gone
		j.DestinationRoot = f.root("movies")
	})
	if err := svc.Delete(context.Background(), done.ID, service.DeleteOptions{DeleteFile: true}); err != nil {
		t.Fatalf("missing file: %v", err)
	}

	outside := filepath.Join(t.TempDir(), "x.mp4")
	writeFile(t, outside, "x")
	esc, _ := f.job(func(j *store.Job) {
		j.Status = jobs.StatusCompleted
		j.FilePath = &outside
		j.DestinationRoot = f.root("movies")
	})
	err := svc.Delete(context.Background(), esc.ID, service.DeleteOptions{DeleteFile: true})
	if code, ok := service.CodeOf(err); !ok || code != service.CodeInvalidRequest {
		t.Fatalf("outside root: %v", err)
	}
	if !exists(outside) {
		t.Fatal("file outside the root deleted")
	}
	if _, err := f.st.Get(context.Background(), esc.ID); err != nil {
		t.Error("record deleted although the file was not")
	}
}
