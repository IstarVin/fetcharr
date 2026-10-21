package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real external downloader: pause must leave aria2's control
// file usable, and resume must request the remaining bytes and preserve data.
func TestRealAria2cStopThenResume(t *testing.T) {
	bin, err := exec.LookPath("yt-dlp")
	if err != nil {
		t.Skip("yt-dlp not available")
	}
	if _, err := exec.LookPath("aria2c"); err != nil {
		t.Skip("aria2c not available")
	}
	data := bytes.Repeat([]byte("fetcharr-aria2c-test\n"), 400_000)
	var resuming, ranged atomic.Bool
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if resuming.Load() && strings.HasPrefix(r.Header.Get("Range"), "bytes=") && r.Header.Get("Range") != "bytes=0-" {
			ranged.Store(true)
		}
		http.ServeContent(w, r, "source.mp4", time.Time{}, bytes.NewReader(data))
	}))
	defer media.Close()
	dir := t.TempDir()
	spec := Spec{URL: media.URL + "/source.mp4", WorkDir: dir, MergeFormat: "mp4",
		ExtraArgs: []string{"--ignore-config", "--proxy", "", "--downloader-args", "aria2c:-x1 -s1 --auto-save-interval=1 --max-download-limit=1M"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, bin, BuildArgs(spec), Options{Dir: dir})
		done <- err
	}()
	// Always join Run, including when a prerequisite below fails.
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	part := filepath.Join(dir, "media.mp4.part")
	deadline := time.Now().Add(20 * time.Second)
	for {
		st, err := os.Stat(part)
		_, controlErr := os.Stat(part + ".aria2")
		if err == nil && st.Size() >= 1<<20 && controlErr == nil {
			break
		}
		select {
		case err := <-done:
			joined = true
			t.Fatalf("download exited before pause: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("aria2c did not leave a resumable partial download")
		}
		time.Sleep(25 * time.Millisecond)
	}
	cancel()
	err = <-done
	joined = true
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("pause = %v, want ErrStopped", err)
	}
	if _, err := os.Stat(part + ".aria2"); err != nil {
		t.Fatalf("aria2c control file missing after pause: %v", err)
	}
	resuming.Store(true)
	spec.ExtraArgs = []string{"--ignore-config", "--proxy", "", "--downloader-args", "aria2c:-x1 -s1"}
	resumeCtx, resumeCancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer resumeCancel()
	var last Progress
	result, err := Run(resumeCtx, bin, BuildArgs(spec), Options{Dir: dir, OnProgress: func(p Progress) { last = p }})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(result.FilePath)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("resumed file differs from source: %v", err)
	}
	if !ranged.Load() {
		t.Error("resume did not request a nonzero byte range")
	}
	if last.Percent == nil || *last.Percent != 100 {
		t.Fatalf("completion progress = %+v", last)
	}
}
