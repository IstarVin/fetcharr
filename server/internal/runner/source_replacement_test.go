package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func TestReplacementDiscardsOldAttemptFiles(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{})
	release := g.r.Hold()
	j := g.insert(func(j *store.Job) { j.Status = jobs.StatusFailed })
	dir := g.workDir(j.ID)
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"media.mp4", "media.mp4.part", "old.en.srt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("old source"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, err := g.svc.Retry(t.Context(), j.ID, service.RetryOptions{Source: &service.ReplacementSource{URL: "https://fresh.example/video", HeadersMode: "clear", Subtitles: []service.SubtitleInput{}}})
	if err != nil {
		t.Fatal(err)
	}
	// Closing and reopening the runner simulates a restart after the durable retry commit.
	g.shutdown()
	g.start(nil)
	release()
	done := g.waitStatus(j.ID, jobs.StatusProcessing)
	if done.ResetWorkDir || done.URL != "https://fresh.example/video" {
		t.Fatal("replacement/reset not committed")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.en.srt")); !os.IsNotExist(err) {
		t.Fatal("stale subtitle remains")
	}
	b, err := os.ReadFile(filepath.Join(dir, "media.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range b {
		if value != 0 {
			t.Fatal("old partial content was reused")
		}
	}
	if len(b) < 4_000_000 || len(b) > 4<<20 {
		t.Fatalf("unexpected fresh media size=%d", len(b))
	}
}

func TestResetFailureRetainsFlagAndRetryRecovers(t *testing.T) {
	setDuration(t, "0")
	g := newRig(t, rigOpts{})
	release := g.r.Hold()
	j := g.insert(func(j *store.Job) { j.ResetWorkDir = true })
	dir := g.workDir(j.ID)
	if err := os.MkdirAll(dir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old.part"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	// A non-writable job directory prevents removing its contents without mocking filesystem operations.
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0750) })
	release()
	failed := g.waitStatus(j.ID, jobs.StatusFailed)
	if !failed.ResetWorkDir || failed.ErrorCode == nil || *failed.ErrorCode != "download_failed" {
		t.Fatal("cleanup failure lost reset requirement")
	}
	if err := os.Chmod(dir, 0750); err != nil {
		t.Fatal(err)
	}
	if _, err := g.svc.Retry(context.Background(), j.ID, service.RetryOptions{}); err != nil {
		t.Fatal(err)
	}
	done := g.waitStatus(j.ID, jobs.StatusProcessing)
	if done.ResetWorkDir {
		t.Fatal("successful retry did not clear reset")
	}
	if _, err := os.Stat(filepath.Join(dir, "old.part")); !os.IsNotExist(err) {
		t.Fatal("old partial remains")
	}
}
