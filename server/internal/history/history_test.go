package history_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/history"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func TestLiveRetentionCanBeEnabledAndDisabled(t *testing.T) {
	st := openStore(t)
	var days atomic.Int64
	p := history.New(history.Options{Store: st, CurrentRetentionDays: func() int { return int(days.Load()) }, Now: func() time.Time { return now }})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	add(t, st, "old", jobs.StatusCompleted, 10, "")
	days.Store(7)
	p.Wake()
	deadline := time.After(3 * time.Second)
	for {
		_, err := st.Get(t.Context(), "old")
		if err == store.ErrNotFound {
			break
		}
		select {
		case <-deadline:
			t.Fatal("enabled pruner did not wake")
		case <-time.After(time.Millisecond):
		}
	}
	days.Store(0)
	add(t, st, "kept", jobs.StatusCompleted, 10, "")
	if n, err := p.Prune(t.Context()); err != nil || n != 0 {
		t.Fatalf("disabled pruning: %d, %v", n, err)
	}
}

var now = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)

type recorder struct {
	mu      sync.Mutex
	changes []service.Change
}

func (r *recorder) Publish(c service.Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, c)
}

func (r *recorder) all() []service.Change {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]service.Change(nil), r.changes...)
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "fetcharr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// add inserts a job; finishedDaysAgo < 0 means not finished.
func add(t *testing.T, st *store.Store, id string, status jobs.Status, finishedDaysAgo float64, file string) {
	t.Helper()
	j := &store.Job{ID: id, Category: "misc", URL: "https://example.com/" + id, Title: store.Ptr(id), Status: status}
	if finishedDaysAgo >= 0 {
		f := now.Add(-time.Duration(finishedDaysAgo * float64(24*time.Hour)))
		j.FinishedAt = &f
	}
	if file != "" {
		j.FilePath = &file
	}
	if err := st.Insert(t.Context(), j); err != nil {
		t.Fatal(err)
	}
}

func remaining(t *testing.T, st *store.Store) []string {
	t.Helper()
	js, _, err := st.List(t.Context(), store.ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, j := range js {
		ids = append(ids, j.ID)
	}
	sort.Strings(ids)
	return ids
}

func newPruner(st *store.Store, pub service.Publisher, days int) *history.Pruner {
	return history.New(history.Options{
		Store: st, Publisher: pub, RetentionDays: days, Now: func() time.Time { return now },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func TestPruneDeletesOnlyOldFinishedJobs(t *testing.T) {
	st := openStore(t)
	media := filepath.Join(t.TempDir(), "movie.mp4")
	if err := os.WriteFile(media, []byte("media"), 0o600); err != nil {
		t.Fatal(err)
	}
	add(t, st, "old-completed", jobs.StatusCompleted, 10, media)
	add(t, st, "old-failed", jobs.StatusFailed, 8, "")
	add(t, st, "old-cancelled", jobs.StatusCancelled, 30, "")
	add(t, st, "recent-completed", jobs.StatusCompleted, 1, "")
	add(t, st, "edge-completed", jobs.StatusCompleted, 6.9, "")
	// Active jobs are never pruned, however old.
	add(t, st, "pending", jobs.StatusPending, -1, "")
	add(t, st, "paused", jobs.StatusPaused, -1, "")
	add(t, st, "downloading", jobs.StatusDownloading, -1, "")
	add(t, st, "processing", jobs.StatusProcessing, -1, "")

	rec := &recorder{}
	n, err := newPruner(st, rec, 7).Prune(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("pruned %d, want 3", n)
	}
	want := []string{"downloading", "edge-completed", "paused", "pending", "processing", "recent-completed"}
	if got := remaining(t, st); !equal(got, want) {
		t.Errorf("remaining = %v, want %v", got, want)
	}

	changes := rec.all()
	if len(changes) != 1 || changes[0].Kind != service.ChangeRemoved || changes[0].Job != nil {
		t.Fatalf("changes = %+v", changes)
	}
	ids := append([]string(nil), changes[0].JobIDs...)
	sort.Strings(ids)
	if !equal(ids, []string{"old-cancelled", "old-completed", "old-failed"}) {
		t.Errorf("job_removed ids = %v", ids)
	}
	// Records only: the media file stays.
	if _, err := os.Stat(media); err != nil {
		t.Errorf("media file touched: %v", err)
	}

	// Nothing left to prune: no event.
	if n, err := newPruner(st, rec, 7).Prune(t.Context()); err != nil || n != 0 {
		t.Errorf("second Prune = %d, %v", n, err)
	}
	if len(rec.all()) != 1 {
		t.Errorf("second Prune published: %+v", rec.all())
	}
}

func TestPruneDisabledAtZeroRetention(t *testing.T) {
	st := openStore(t)
	add(t, st, "ancient", jobs.StatusCompleted, 3650, "")
	rec := &recorder{}
	p := newPruner(st, rec, 0)
	if p.Enabled() {
		t.Error("enabled at 0")
	}
	if n, err := p.Prune(t.Context()); err != nil || n != 0 {
		t.Errorf("Prune = %d, %v", n, err)
	}
	if got := remaining(t, st); !equal(got, []string{"ancient"}) {
		t.Errorf("remaining = %v", got)
	}
	done := make(chan struct{})
	go func() { p.Run(t.Context()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run blocked with retention 0")
	}
}

func TestRunPrunesAtStartupAndPeriodically(t *testing.T) {
	st := openStore(t)
	add(t, st, "old-1", jobs.StatusCompleted, 40, "")
	rec := &recorder{}
	p := history.New(history.Options{
		Store: st, Publisher: rec, RetentionDays: 7, Interval: 20 * time.Millisecond,
		Now: func() time.Time { return now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	waitFor(t, "startup pass", func() bool { return len(rec.all()) == 1 })
	add(t, st, "old-2", jobs.StatusFailed, 20, "")
	waitFor(t, "periodic pass", func() bool { return len(rec.all()) == 2 })
	if ids := rec.all()[1].JobIDs; !equal(ids, []string{"old-2"}) {
		t.Errorf("second event ids = %v", ids)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
