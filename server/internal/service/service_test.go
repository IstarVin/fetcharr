package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/store"
)

type recorder struct {
	mu      sync.Mutex
	changes []Change
	wakes   int
	stops   []string
	removed []string
}

func (r *recorder) Publish(c Change) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.changes = append(r.changes, c)
}
func (r *recorder) Wake() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.wakes++
}
func (r *recorder) Stop(id string, reason StopReason) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops = append(r.stops, id+":"+string(reason))
}
func (r *recorder) RemoveWorkDirs(ids ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removed = append(r.removed, ids...)
}
func (r *recorder) kinds() []ChangeKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []ChangeKind
	for _, c := range r.changes {
		out = append(out, c.Kind)
	}
	return out
}

func newSvc(t *testing.T) (*Service, *store.Store, *recorder, *time.Time) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.Categories = []config.Category{
		{ID: "tv", Label: "TV", Path: dir, Layout: naming.LayoutTV},
		{ID: "misc", Label: "Misc", Path: dir, Layout: naming.LayoutFlat},
	}
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	rec := &recorder{}
	clock := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	svc := New(st, cfg, WithPublisher(rec), WithController(rec), WithClock(func() time.Time { return clock }))
	return svc, st, rec, &clock
}

func seed(t *testing.T, st *store.Store, url string, status jobs.Status) *store.Job {
	t.Helper()
	j := &store.Job{Category: "misc", URL: url, Title: store.Ptr("t " + url), Status: status}
	if err := st.Insert(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	return j
}

func wantCode(t *testing.T, err error, code Code) {
	t.Helper()
	got, ok := CodeOf(err)
	if !ok || got != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func TestCreateWakesAndPublishes(t *testing.T) {
	svc, st, rec, _ := newSvc(t)
	j, err := svc.Create(t.Context(), CreateRequest{URL: "https://a/x", Category: "misc", Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if j.WorkDir == "" || j.Status != jobs.StatusPending {
		t.Errorf("job = %+v", j)
	}
	if rec.wakes != 1 || len(rec.kinds()) != 1 || rec.kinds()[0] != ChangeAdded {
		t.Errorf("wakes=%d kinds=%v", rec.wakes, rec.kinds())
	}
	// A failed create has no side effects.
	_, err = svc.Create(t.Context(), CreateRequest{URL: "https://a/x", Category: "misc", Title: "T"})
	wantCode(t, err, CodeDuplicate)
	if rec.wakes != 1 || len(rec.kinds()) != 1 {
		t.Errorf("failed create had side effects: wakes=%d kinds=%v", rec.wakes, rec.kinds())
	}
	if _, total, _ := st.List(t.Context(), store.ListFilter{}); total != 1 {
		t.Errorf("total = %d", total)
	}
}

func TestPauseCancelStopHooks(t *testing.T) {
	svc, st, rec, _ := newSvc(t)
	dl := seed(t, st, "https://a/dl", jobs.StatusDownloading)
	pe := seed(t, st, "https://a/pe", jobs.StatusPending)
	pr := seed(t, st, "https://a/pr", jobs.StatusProcessing)
	pa := seed(t, st, "https://a/pa", jobs.StatusPaused)

	if _, err := svc.Pause(t.Context(), dl.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pause(t.Context(), pe.ID); err != nil {
		t.Fatal(err)
	}
	if len(rec.stops) != 1 || rec.stops[0] != dl.ID+":pause" {
		t.Errorf("stops after pause = %v (pause of a pending job has nothing to stop)", rec.stops)
	}
	for _, id := range []string{pr.ID, pa.ID, pe.ID} {
		if _, err := svc.Cancel(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{dl.ID + ":pause", pr.ID + ":cancel", pa.ID + ":cancel", pe.ID + ":cancel"}
	if len(rec.stops) != len(want) {
		t.Fatalf("stops = %v, want %v", rec.stops, want)
	}
	for i := range want {
		if rec.stops[i] != want[i] {
			t.Errorf("stops[%d] = %s, want %s", i, rec.stops[i], want[i])
		}
	}
	// A refused action calls nothing.
	n := len(rec.stops)
	_, err := svc.Cancel(t.Context(), dl.ID) // paused -> cancelled is fine
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Pause(t.Context(), dl.ID)
	wantCode(t, err, CodeInvalidState)
	if len(rec.stops) != n+1 {
		t.Errorf("refused pause called Stop: %v", rec.stops)
	}
}

type gatedController struct {
	Controller
	entered chan struct{}
	release chan struct{}
}

func (c *gatedController) Stop(id string, reason StopReason) {
	close(c.entered)
	<-c.release
	c.Controller.Stop(id, reason)
}

// Hold the stop hook after the state commit, then schedule the competing
// action until it blocks. Its pending state must wait for that stop hook.
func TestLifecycleActionsWaitForStopHook(t *testing.T) {
	for _, name := range []string{"pause-resume", "cancel-retry", "cancel-replace"} {
		cancel := name != "pause-resume"
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				svc, st, rec, _ := newSvc(t)
				ctl := &gatedController{Controller: rec, entered: make(chan struct{}), release: make(chan struct{})}
				release := sync.OnceFunc(func() { close(ctl.release) })
				defer release()
				svc.SetController(ctl)
				j := seed(t, st, "https://a/hook-order", jobs.StatusDownloading)
				stopped := make(chan error, 1)
				go func() {
					var err error
					if cancel {
						_, err = svc.Cancel(t.Context(), j.ID)
					} else {
						_, err = svc.Pause(t.Context(), j.ID)
					}
					stopped <- err
				}()
				<-ctl.entered
				resumed := make(chan error, 1)
				go func() {
					var err error
					if cancel {
						opts := RetryOptions{}
						if name == "cancel-replace" {
							opts.Source = &ReplacementSource{URL: "https://a/fresh", HeadersMode: "clear", Subtitles: []SubtitleInput{}}
						}
						_, err = svc.Retry(t.Context(), j.ID, opts)
					} else {
						_, err = svc.Resume(t.Context(), j.ID)
					}
					resumed <- err
				}()
				synctest.Wait()
				overtook := false
				select {
				case err := <-resumed:
					overtook = true
					t.Errorf("pending action overtook the stop hook: %v", err)
				default:
				}
				row, err := st.Get(t.Context(), j.ID)
				if err != nil {
					t.Fatal(err)
				}
				want := jobs.StatusPaused
				if cancel {
					want = jobs.StatusCancelled
				}
				if row.Status != want {
					t.Errorf("status before stop hook returned = %s, want %s", row.Status, want)
				}
				release()
				if err := <-stopped; err != nil {
					t.Fatal(err)
				}
				if !overtook {
					if err := <-resumed; err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}

func TestResumeRetryWakeAndQueueTime(t *testing.T) {
	svc, st, rec, clock := newSvc(t)
	paused := seed(t, st, "https://a/p", jobs.StatusPaused)
	failed := seed(t, st, "https://a/f", jobs.StatusFailed)

	*clock = clock.Add(time.Hour)
	r, err := svc.Resume(t.Context(), paused.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !r.QueuedAt.Equal(*clock) || r.Status != jobs.StatusPending {
		t.Errorf("resume: queued_at = %v", r.QueuedAt)
	}
	*clock = clock.Add(time.Hour)
	r, err = svc.Retry(t.Context(), failed.ID, RetryOptions{Overwrite: true})
	if err != nil {
		t.Fatal(err)
	}
	if !r.QueuedAt.Equal(*clock) || !r.Overwrite {
		t.Errorf("retry: %+v", r)
	}
	if rec.wakes != 2 {
		t.Errorf("wakes = %d, want 2", rec.wakes)
	}
	// Cancel sets finished_at from the clock.
	c, err := svc.Cancel(t.Context(), paused.ID)
	if err != nil || c.FinishedAt == nil || !c.FinishedAt.Equal(*clock) {
		t.Errorf("cancel: %+v %v", c, err)
	}
}

func TestPlacingBlocksCancel(t *testing.T) {
	svc, st, _, _ := newSvc(t)
	j := seed(t, st, "https://a/pl", jobs.StatusProcessing)
	if _, err := st.Update(t.Context(), j.ID, store.Update{Placing: store.Set(true)}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Cancel(t.Context(), j.ID)
	wantCode(t, err, CodeInvalidState)
}

func TestDeleteAndDeleteManyPublish(t *testing.T) {
	svc, st, rec, _ := newSvc(t)
	a := seed(t, st, "https://a/1", jobs.StatusCompleted)
	seed(t, st, "https://a/2", jobs.StatusFailed)
	seed(t, st, "https://a/3", jobs.StatusPending)

	wantCode(t, svc.Delete(t.Context(), "nope", DeleteOptions{}), CodeNotFound)
	if err := svc.Delete(t.Context(), a.ID, DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	n, err := svc.DeleteMany(t.Context(), nil, nil)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	_, err = svc.DeleteMany(t.Context(), []jobs.Status{jobs.StatusPaused}, nil)
	wantCode(t, err, CodeInvalidRequest)
	if k := rec.kinds(); len(k) != 2 || k[0] != ChangeRemoved || k[1] != ChangeRemoved {
		t.Errorf("kinds = %v", k)
	}
	// Nothing deleted -> nothing published.
	if _, err := svc.DeleteMany(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(rec.kinds()) != 2 {
		t.Errorf("empty clear published an event")
	}
	if len(rec.removed) != 2 || rec.removed[0] != a.ID {
		t.Errorf("work dir removal hook got %v", rec.removed)
	}
}

func TestBatchHeaderMergeCaseInsensitive(t *testing.T) {
	svc, st, _, _ := newSvc(t)
	one, two := 1, 2
	res, err := svc.CreateBatch(t.Context(), BatchRequest{
		Category: "tv", ShowName: "S", Season: &one,
		Headers: map[string]string{"Referer": "a", "X-Base": "b"},
		Episodes: []BatchEpisode{
			{Episode: &two, URL: "https://a/e2", Headers: map[string]string{"referer": "override"}},
			{Episode: &one, URL: "https://a/e1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Jobs) != 2 || *res.Jobs[0].Episode != 1 {
		t.Fatalf("jobs = %+v", res.Jobs)
	}
	got, _ := st.Get(t.Context(), res.Jobs[1].ID)
	if len(got.Headers) != 2 || got.Headers["referer"] != "override" || got.Headers["X-Base"] != "b" {
		t.Errorf("headers = %v", got.Headers)
	}
}

// A runner starts placing after the service read the job but before it wrote
// the cancel: the guarded write must lose, and the retry must report
// invalid_state instead of cancelling a half-placed job.
func TestCancelLosesRaceAgainstPlacing(t *testing.T) {
	svc, st, rec, _ := newSvc(t)
	j := seed(t, st, "https://a/race", jobs.StatusProcessing)
	// updateFor calls the clock after the read and before the write.
	svc.now = func() time.Time {
		if _, err := st.SetPlacing(t.Context(), j.ID); err != nil && !errors.Is(err, store.ErrStatusMismatch) {
			t.Error(err)
		}
		return time.Now()
	}
	_, err := svc.Cancel(t.Context(), j.ID)
	wantCode(t, err, CodeInvalidState)
	got, _ := st.Get(t.Context(), j.ID)
	if got.Status != jobs.StatusProcessing || !got.Placing {
		t.Errorf("job = %s placing=%v, want processing/placing", got.Status, got.Placing)
	}
	if len(rec.stops) != 0 || len(rec.kinds()) != 0 {
		t.Errorf("a lost cancel had side effects: stops=%v kinds=%v", rec.stops, rec.kinds())
	}
}

func TestDeleteRefusesJobThatBecameActive(t *testing.T) {
	svc, st, rec, _ := newSvc(t)
	j := seed(t, st, "https://a/del", jobs.StatusFailed)
	// Retry (via the store, as another request would) before the delete.
	if _, err := st.Update(t.Context(), j.ID, store.Update{Status: store.Set(jobs.StatusPending)}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, svc.Delete(t.Context(), j.ID, DeleteOptions{}), CodeInvalidState)
	if _, err := st.Get(t.Context(), j.ID); err != nil {
		t.Errorf("active job deleted: %v", err)
	}
	if len(rec.kinds()) != 0 || len(rec.removed) != 0 {
		t.Errorf("published %v, removed work dirs %v", rec.kinds(), rec.removed)
	}
}
