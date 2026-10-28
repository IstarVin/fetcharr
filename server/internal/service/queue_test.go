package service

import (
	"testing"

	"github.com/istarvin/fetcharr/server/internal/jobs"
)

func TestCreateRejectsSecondJobForSameLibraryFile(t *testing.T) {
	svc, _, _, _ := newSvc(t)
	one, two := 1, 2
	ep := func(url string, n *int) CreateRequest {
		return CreateRequest{Category: "tv", URL: url, ShowName: "Severance", Season: &one, Episode: n}
	}
	if _, err := svc.Create(t.Context(), ep("https://cdn/a?token=1", &one)); err != nil {
		t.Fatal(err)
	}
	// Same episode from a fresh stream link, and with different case.
	_, err := svc.Create(t.Context(), ep("https://cdn/a?token=2", &one))
	wantCode(t, err, CodeDuplicate)
	req := ep("https://cdn/a?token=3", &one)
	req.ShowName = "SEVERANCE"
	_, err = svc.Create(t.Context(), req)
	wantCode(t, err, CodeDuplicate)
	// Overwrite does not let two jobs write one file at once.
	req = ep("https://cdn/a?token=4", &one)
	req.Overwrite = true
	_, err = svc.Create(t.Context(), req)
	wantCode(t, err, CodeDuplicate)
	if _, err := svc.Create(t.Context(), ep("https://cdn/b", &two)); err != nil {
		t.Fatalf("another episode: %v", err)
	}
}

func TestBatchSkipsEpisodesAlreadyQueued(t *testing.T) {
	svc, _, _, _ := newSvc(t)
	one, two, three := 1, 2, 3
	if _, err := svc.Create(t.Context(), CreateRequest{Category: "tv", URL: "https://old/e1", ShowName: "S", Season: &one, Episode: &one}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.CreateBatch(t.Context(), BatchRequest{
		Category: "tv", ShowName: "S", Season: &one,
		Episodes: []BatchEpisode{
			{Episode: &one, URL: "https://new/e1"},
			{Episode: &two, URL: "https://new/e2"},
			{Episode: &two, URL: "https://new/e2-mirror"},
			{Episode: &three, URL: "https://new/e3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 2 {
		t.Fatalf("created %d jobs, want episodes 2 and 3", len(res.Jobs))
	}
	if len(res.Skipped) != 2 || res.Skipped[0].Episode != 1 || res.Skipped[1].Episode != 2 {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
	for _, sk := range res.Skipped {
		if sk.Reason != SkipReasonDuplicate {
			t.Errorf("episode %d: reason %s", sk.Episode, sk.Reason)
		}
	}
}

func TestRetryRejectedWhileAnotherJobWritesTheFile(t *testing.T) {
	svc, st, _, _ := newSvc(t)
	failed := seed(t, st, "https://a/old", jobs.StatusFailed)
	_, err := svc.Create(t.Context(), CreateRequest{Category: "misc", URL: "https://a/new", Title: *failed.Title})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Retry(t.Context(), failed.ID, RetryOptions{})
	wantCode(t, err, CodeDuplicate)
}

func TestPrioritize(t *testing.T) {
	svc, st, rec, _ := newSvc(t)
	a := seed(t, st, "https://a/1", jobs.StatusPending)
	b := seed(t, st, "https://a/2", jobs.StatusPending)
	paused := seed(t, st, "https://a/3", jobs.StatusPaused)

	got, err := svc.Prioritize(t.Context(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.QueuedAt.Before(a.QueuedAt) {
		t.Fatalf("queued_at %v not before %v", got.QueuedAt, a.QueuedAt)
	}
	if k := rec.kinds(); len(k) != 1 || k[0] != ChangeUpdated {
		t.Fatalf("changes = %v", k)
	}
	_, err = svc.Prioritize(t.Context(), paused.ID)
	wantCode(t, err, CodeInvalidState)
	_, err = svc.Prioritize(t.Context(), "missing")
	wantCode(t, err, CodeNotFound)
}

func TestPrioritizeBatch(t *testing.T) {
	svc, st, _, _ := newSvc(t)
	first := seed(t, st, "https://a/first", jobs.StatusPending)
	one, two := 1, 2
	res, err := svc.CreateBatch(t.Context(), BatchRequest{
		Category: "tv", ShowName: "S", Season: &one,
		Episodes: []BatchEpisode{{Episode: &one, URL: "https://b/1"}, {Episode: &two, URL: "https://b/2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	moved, err := svc.PrioritizeBatch(t.Context(), res.BatchID)
	if err != nil || len(moved) != 2 {
		t.Fatalf("moved %v, %v", moved, err)
	}
	order, err := st.PendingJobs(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if order[0].ID != res.Jobs[0].ID || order[1].ID != res.Jobs[1].ID || order[2].ID != first.ID {
		t.Fatalf("dispatch order wrong: %v", order)
	}
	_, err = svc.PrioritizeBatch(t.Context(), "missing")
	wantCode(t, err, CodeNotFound)
}
