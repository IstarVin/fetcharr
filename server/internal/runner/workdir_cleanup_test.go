package runner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func mkWorkDir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "video.mp4.part"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteRemovesWorkDirOfFailedJob(t *testing.T) {
	g := newRig(t, rigOpts{})
	j := g.insert(func(j *store.Job) { j.Status = jobs.StatusFailed })
	other := g.insert(func(j *store.Job) { j.Status = jobs.StatusFailed })
	mkWorkDir(t, g.workDir(j.ID))
	mkWorkDir(t, g.workDir(other.ID))

	if err := g.svc.Delete(t.Context(), j.ID, service.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	waitGone(t, g.workDir(j.ID))
	if !exists(g.workDir(other.ID)) {
		t.Error("work dir of another job removed")
	}

	n, err := g.svc.DeleteMany(t.Context(), []jobs.Status{jobs.StatusFailed}, nil)
	if err != nil || n != 1 {
		t.Fatalf("DeleteMany = %d, %v", n, err)
	}
	waitGone(t, g.workDir(other.ID))
}

func TestRemoveWorkDirsIgnoresUnsafeIDs(t *testing.T) {
	g := newRig(t, rigOpts{})
	outside := filepath.Join(g.cfg.DataDir, "keep")
	mkWorkDir(t, outside)
	g.r.RemoveWorkDirs("", ".", "..", "../keep", "a/b")
	g.shutdown() // waits for background deletions
	if !exists(outside) || !exists(g.cfg.WorkDir()) {
		t.Fatal("unsafe id removed a directory outside the job work dirs")
	}
}

func TestSweepWorkDirs(t *testing.T) {
	g := newRig(t, rigOpts{})
	keep := map[string]jobs.Status{
		"pending": jobs.StatusPending, "downloading": jobs.StatusDownloading,
		"processing": jobs.StatusProcessing, "paused": jobs.StatusPaused, "failed": jobs.StatusFailed,
	}
	drop := map[string]jobs.Status{"completed": jobs.StatusCompleted, "cancelled": jobs.StatusCancelled}
	ids := map[string]string{}
	for name, st := range keep {
		j := g.insert(func(j *store.Job) { j.Status = st })
		ids[name] = j.ID
	}
	for name, st := range drop {
		j := g.insert(func(j *store.Job) { j.Status = st })
		ids[name] = j.ID
	}
	ids["orphan"] = "orphan-without-job"
	for _, id := range ids {
		mkWorkDir(t, g.workDir(id))
	}

	if n := g.r.SweepWorkDirs(t.Context()); n != 3 {
		t.Errorf("removed %d, want 3", n)
	}
	for name := range keep {
		if !exists(g.workDir(ids[name])) {
			t.Errorf("%s work dir removed", name)
		}
	}
	for _, name := range []string{"completed", "cancelled", "orphan"} {
		if exists(g.workDir(ids[name])) {
			t.Errorf("%s work dir kept", name)
		}
	}
}
