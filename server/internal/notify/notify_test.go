package notify

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/events"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

type received struct {
	title, tags, auth, body string
}

type sink struct {
	mu   sync.Mutex
	got  []received
	srv  *httptest.Server
	code int
}

func newSink(t *testing.T) *sink {
	s := &sink{code: 200}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		title, _ := new(mime.WordDecoder).DecodeHeader(r.Header.Get("Title"))
		s.mu.Lock()
		s.got = append(s.got, received{title, r.Header.Get("Tags"), r.Header.Get("Authorization"), string(b)})
		code := s.code
		s.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *sink) all() []received {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]received{}, s.got...)
}

func setup(t *testing.T, targets ...config.Notification) (*Notifier, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	n := New(Options{Bus: events.NewBus(), Store: st, Targets: func() []config.Notification { return targets }})
	return n, st
}

func finish(t *testing.T, st *store.Store, j *store.Job, status jobs.Status, at time.Time) *store.Job {
	t.Helper()
	u := store.Update{Status: store.Set(status), FinishedAt: store.Set(&at)}
	if status == jobs.StatusFailed {
		u.ErrorCode = store.Set(store.Ptr("low_disk_space"))
	}
	row, err := st.Update(context.Background(), j.ID, u)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestNtfyCompletedAndFailedFilters(t *testing.T) {
	s := newSink(t)
	n, st := setup(t, config.Notification{Type: config.NotificationNtfy, URL: s.srv.URL, Token: "tok", OnCompleted: true})
	j := &store.Job{Category: "movies", URL: "https://a/1", Title: store.Ptr("Dune Part Two"), Year: store.Ptr(2024)}
	if err := st.Insert(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	n.handle(t.Context(), finish(t, st, j, jobs.StatusCompleted, time.Now()))
	f := &store.Job{Category: "movies", URL: "https://a/2", Title: store.Ptr("Other")}
	if err := st.Insert(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	n.handle(t.Context(), finish(t, st, f, jobs.StatusFailed, time.Now())) // not subscribed
	n.sends.Wait()

	got := s.all()
	if len(got) != 1 {
		t.Fatalf("got %d notifications, want 1: %+v", len(got), got)
	}
	if got[0].title != "Downloaded" || got[0].body != "Dune Part Two (2024)" || got[0].auth != "Bearer tok" || got[0].tags != "white_check_mark" {
		t.Fatalf("unexpected notification %+v", got[0])
	}
}

func TestBatchSummaryThenRetriedEpisode(t *testing.T) {
	s := newSink(t)
	n, st := setup(t, config.Notification{Type: config.NotificationNtfy, URL: s.srv.URL, OnCompleted: true, OnFailed: true})
	batch := "b1"
	var eps []*store.Job
	for i := 1; i <= 2; i++ {
		eps = append(eps, &store.Job{BatchID: &batch, Category: "tv", URL: "https://e/" + string(rune('0'+i)),
			ShowName: store.Ptr("Severance"), Season: store.Ptr(2), Episode: store.Ptr(i)})
	}
	if _, err := st.InsertBatch(t.Context(), eps); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	n.handle(t.Context(), finish(t, st, eps[0], jobs.StatusCompleted, now))
	n.sends.Wait()
	if len(s.all()) != 0 {
		t.Fatal("summary sent while an episode is still active")
	}
	last := finish(t, st, eps[1], jobs.StatusFailed, now.Add(time.Second))
	n.handle(t.Context(), last)
	n.handle(t.Context(), last) // a repeated event is not announced twice
	n.sends.Wait()
	got := s.all()
	if len(got) != 1 || got[0].body != "Severance S02 — 1 downloaded, 1 failed" || got[0].tags != "warning" {
		t.Fatalf("summary = %+v", got)
	}

	// The failed episode is retried and completes later: its own notification.
	n.handle(t.Context(), finish(t, st, eps[1], jobs.StatusCompleted, now.Add(time.Hour)))
	n.sends.Wait()
	got = s.all()
	if len(got) != 2 || got[1].body != "Severance — S02E02" {
		t.Fatalf("retried episode = %+v", got)
	}
}

func TestWebhookPayloadOmitsSourceURL(t *testing.T) {
	s := newSink(t)
	n, _ := setup(t)
	j := &store.Job{ID: "j1", URL: "https://secret.example/?token=abc", Title: store.Ptr("X"), Status: jobs.StatusFailed,
		ErrorCode: store.Ptr("download_failed")}
	err := n.Send(t.Context(), config.Notification{Type: config.NotificationWebhook, URL: s.srv.URL}, jobMessage(j))
	if err != nil {
		t.Fatal(err)
	}
	body := s.all()[0].body
	if strings.Contains(body, "secret.example") {
		t.Fatal("source URL leaked into the webhook payload")
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	if p["event"] != EventFailed || p["text"] != "Failed · X — Download failed" || p["content"] != p["text"] {
		t.Fatalf("payload = %v", p)
	}
}

func TestSendErrorsHideTheURL(t *testing.T) {
	s := newSink(t)
	s.code = http.StatusUnauthorized
	n, _ := setup(t)
	target := config.Notification{Type: config.NotificationNtfy, URL: s.srv.URL + "/secret-topic"}
	if err := n.Test(t.Context(), target); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want the 401 status", err)
	}
	target.URL = "http://127.0.0.1:1/secret-topic"
	err := n.Test(t.Context(), target)
	if err == nil || strings.Contains(err.Error(), "secret-topic") {
		t.Fatalf("err = %v, want a transport error without the URL", err)
	}
}

func TestRunConsumesBus(t *testing.T) {
	s := newSink(t)
	n, st := setup(t, config.Notification{Type: config.NotificationNtfy, URL: s.srv.URL, OnCompleted: true})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()
	j := &store.Job{Category: "movies", URL: "https://a/1", Title: store.Ptr("Y")}
	if err := st.Insert(t.Context(), j); err != nil {
		t.Fatal(err)
	}
	row := finish(t, st, j, jobs.StatusCompleted, time.Now())
	deadline := time.Now().Add(5 * time.Second)
	for len(s.all()) == 0 && time.Now().Before(deadline) {
		n.bus.Publish(service.Change{Kind: service.ChangeUpdated, Job: row})
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done
	if len(s.all()) != 1 {
		t.Fatalf("got %d notifications via the bus, want 1", len(s.all()))
	}
}
