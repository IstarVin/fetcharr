package store

import (
	"errors"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/jobs"
)

func insertWith(t *testing.T, s *Store, url string, st jobs.Status, placing bool) *Job {
	t.Helper()
	j := newJob(url)
	j.Status, j.Placing = st, placing
	return mustInsert(t, s, j)
}

func TestUpdateGuardedPlacing(t *testing.T) {
	s, _ := openTemp(t)
	cancel := Update{Status: Set(jobs.StatusCancelled)}
	notPlacing := UpdateGuard{Statuses: []jobs.Status{jobs.StatusProcessing}, Placing: Ptr(false)}

	// Placing was set between the caller's read and its write: the guard must refuse.
	placing := insertWith(t, s, "https://g/1", jobs.StatusProcessing, true)
	if _, err := s.UpdateGuarded(ctx, placing.ID, notPlacing, cancel); !errors.Is(err, ErrStatusMismatch) {
		t.Fatalf("err = %v, want ErrStatusMismatch", err)
	}
	got, _ := s.Get(ctx, placing.ID)
	if got.Status != jobs.StatusProcessing {
		t.Errorf("a refused guarded update changed the row: %s", got.Status)
	}

	free := insertWith(t, s, "https://g/2", jobs.StatusProcessing, false)
	j, err := s.UpdateGuarded(ctx, free.ID, notPlacing, cancel)
	if err != nil || j.Status != jobs.StatusCancelled {
		t.Fatalf("j=%v err=%v", j, err)
	}

	if _, err := s.UpdateGuarded(ctx, "nope", notPlacing, cancel); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	// A guard with no assignments is still enforced.
	if _, err := s.UpdateGuarded(ctx, placing.ID, notPlacing, Update{}); !errors.Is(err, ErrStatusMismatch) {
		t.Errorf("empty update: err = %v, want ErrStatusMismatch", err)
	}
	// The zero guard behaves like Update.
	if _, err := s.UpdateGuarded(ctx, placing.ID, UpdateGuard{}, Update{Placing: Set(false)}); err != nil {
		t.Errorf("zero guard: %v", err)
	}
}

func TestSetPlacingAtomicWithCancel(t *testing.T) {
	s, _ := openTemp(t)
	notPlacing := UpdateGuard{Statuses: []jobs.Status{jobs.StatusProcessing}, Placing: Ptr(false)}

	// Placing wins: a later cancel (guarded on placing=0) is refused.
	a := insertWith(t, s, "https://sp/a", jobs.StatusProcessing, false)
	j, err := s.SetPlacing(ctx, a.ID)
	if err != nil || !j.Placing {
		t.Fatalf("j=%v err=%v", j, err)
	}
	if _, err := s.UpdateGuarded(ctx, a.ID, notPlacing, Update{Status: Set(jobs.StatusCancelled)}); !errors.Is(err, ErrStatusMismatch) {
		t.Errorf("cancel after placing: %v", err)
	}

	// Cancel wins: the runner's SetPlacing must report a mismatch.
	b := insertWith(t, s, "https://sp/b", jobs.StatusProcessing, false)
	if _, err := s.UpdateGuarded(ctx, b.ID, notPlacing, Update{Status: Set(jobs.StatusCancelled)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetPlacing(ctx, b.ID); !errors.Is(err, ErrStatusMismatch) {
		t.Errorf("SetPlacing after cancel: %v, want ErrStatusMismatch", err)
	}
	got, _ := s.Get(ctx, b.ID)
	if got.Placing {
		t.Error("placing set on a cancelled job")
	}

	for _, st := range []jobs.Status{jobs.StatusPending, jobs.StatusDownloading, jobs.StatusPaused} {
		c := insertWith(t, s, "https://sp/"+string(st), st, false)
		if _, err := s.SetPlacing(ctx, c.ID); !errors.Is(err, ErrStatusMismatch) {
			t.Errorf("SetPlacing on %s: %v", st, err)
		}
	}
	if _, err := s.SetPlacing(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestDeleteIf(t *testing.T) {
	s, _ := openTemp(t)
	fin := jobs.FinishedStatuses()

	done := insertWith(t, s, "https://di/done", jobs.StatusCompleted, false)
	if err := s.DeleteIf(ctx, done.ID, fin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, done.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("row still there: %v", err)
	}
	if err := s.DeleteIf(ctx, done.ID, fin); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v, want ErrNotFound", err)
	}

	// A job that became active again since the caller's read is not deleted.
	active := insertWith(t, s, "https://di/active", jobs.StatusPending, false)
	if err := s.DeleteIf(ctx, active.ID, fin); !errors.Is(err, ErrStatusMismatch) {
		t.Errorf("active delete: %v, want ErrStatusMismatch", err)
	}
	if _, err := s.Get(ctx, active.ID); err != nil {
		t.Errorf("active job was deleted: %v", err)
	}
	if err := s.DeleteIf(ctx, active.ID, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("no statuses: %v", err)
	}
}
