package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// Every ws-*.json fixture is reproduced exactly (same keys, types and values)
// by the hub, with ms timestamps and without request headers.
func TestGoldenFixtures(t *testing.T) {
	h := newHarness(t)
	for _, j := range fixtureJobs(t, "ws-snapshot.json") {
		if err := h.st.Insert(t.Context(), j); err != nil {
			t.Fatal(err)
		}
	}
	c := h.dial()
	h.waitClients(1)

	check := func(name string, got msg) {
		t.Helper()
		want := fixture(t, name)
		if got.Type != want["type"] {
			t.Errorf("%s: type = %q, want %q", name, got.Type, want["type"])
		}
		assertShape(t, name, want, got.Obj)
		assertTimestamps(t, name, got.Obj)
		if !reflect.DeepEqual(want, got.Obj) {
			t.Errorf("%s: values differ\nwant %v\n got %v", name, want, got.Obj)
		}
	}

	check("ws-snapshot.json", read(t, c))

	// job_added / job_updated: publish the fixture's job.
	h.bus.Publish(service.Change{Kind: service.ChangeAdded, Job: fixtureJobs(t, "ws-job_added.json")[0]})
	check("ws-job_added.json", read(t, c))
	h.bus.Publish(service.Change{Kind: service.ChangeUpdated, Job: fixtureJobs(t, "ws-job_updated.json")[0]})
	check("ws-job_updated.json", read(t, c))

	ids := fixture(t, "ws-job_removed.json")["job_ids"].([]any)
	var strIDs []string
	for _, id := range ids {
		strIDs = append(strIDs, id.(string))
	}
	h.bus.Publish(service.Change{Kind: service.ChangeRemoved, JobIDs: strIDs})
	check("ws-job_removed.json", read(t, c))
}

// An empty snapshot has "jobs": [] (an array, not null).
func TestEmptySnapshotIsArray(t *testing.T) {
	h := newHarness(t, emptySnapshot()...)
	m := read(t, h.dial())
	if m.Type != "snapshot" || !strings.Contains(string(m.Raw), `"jobs":[]`) {
		t.Errorf("empty snapshot = %s", m.Raw)
	}
}

// The Done-when scenario: snapshot, then the event stream of the smoke script.
func TestSmokeSequenceEventStream(t *testing.T) {
	h := newHarness(t)
	c := h.dial()
	snap := read(t, c)
	if jobsArr, _ := snap.Obj["jobs"].([]any); snap.Type != "snapshot" || len(jobsArr) != 0 {
		t.Fatalf("first message = %s, want an empty snapshot", snap.Raw)
	}

	status, body := h.api("POST", "/api/jobs", map[string]any{
		"category": "misc", "url": "https://cdn.example/smoke.mp4", "title": "Smoke",
		"headers": map[string]string{"Cookie": secretHeader},
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, body)
	}
	id := jsonField(t, body, "id")
	if status, body = h.api("GET", "/api/jobs", nil); status != 200 {
		t.Fatalf("list: %d %s", status, body)
	}
	for _, step := range []struct{ method, path string }{
		{"POST", "/api/jobs/" + id + "/pause"},
		{"POST", "/api/jobs/" + id + "/resume"},
		{"POST", "/api/jobs/" + id + "/cancel"},
		{"DELETE", "/api/jobs/" + id},
	} {
		if status, body = h.api(step.method, step.path, nil); status/100 != 2 {
			t.Fatalf("%s %s: %d %s", step.method, step.path, status, body)
		}
	}

	want := []struct{ typ, status string }{
		{"job_added", "pending"},
		{"job_updated", "paused"},
		{"job_updated", "pending"},
		{"job_updated", "cancelled"},
		{"job_removed", ""},
	}
	for i, w := range want {
		m := read(t, c)
		if m.Type != w.typ {
			t.Fatalf("event %d: type = %q, want %q (%s)", i, m.Type, w.typ, m.Raw)
		}
		if w.typ == "job_removed" {
			ids, _ := m.Obj["job_ids"].([]any)
			if len(ids) != 1 || ids[0] != id {
				t.Errorf("job_removed ids = %v, want [%s]", ids, id)
			}
			continue
		}
		j := m.job()
		if j["id"] != id || j["status"] != w.status {
			t.Errorf("event %d: id/status = %v/%v, want %s/%s", i, j["id"], j["status"], id, w.status)
		}
		assertTimestamps(t, "event", m.Obj)
	}
	expectSilence(t, c, 150*time.Millisecond)
}

// Events that happen while a client is connecting are not lost: the client
// registers before the snapshot is read.
func TestNoEventLostAroundSnapshot(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	h := newHarness(t, func(o *Options) {
		inner := o.Snapshot
		o.Snapshot = func(ctx context.Context) ([]*store.Job, error) {
			js, err := inner(ctx)
			close(started)
			<-release // the event below is published after the snapshot was read
			return js, err
		}
	})
	c := h.dial()
	<-started
	j := newJob(jobs.StatusPending, 0)
	h.bus.Publish(added(j))
	close(release)
	if m := read(t, c); m.Type != "snapshot" {
		t.Fatalf("first message = %s, want snapshot", m.Raw)
	}
	if m := read(t, c); m.Type != "job_added" || m.job()["id"] != j.ID {
		t.Fatalf("second message = %s, want job_added %s", m.Raw, j.ID)
	}
}

func TestAuthOnUpgrade(t *testing.T) {
	h := newHarness(t)
	for name, hdr := range map[string]http.Header{
		"no token":     nil,
		"wrong token":  authHeader("nope"),
		"wrong scheme": {"Authorization": {"Basic " + testToken}},
	} {
		ctx, cancel := timeoutCtx(t)
		c, resp, err := websocket.Dial(ctx, h.wsURL(), &websocket.DialOptions{HTTPHeader: hdr})
		cancel()
		if err == nil {
			_ = c.CloseNow()
			t.Errorf("%s: upgrade succeeded", name)
			continue
		}
		if resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: response = %v, want 401", name, resp)
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
	}
	if h.hub.Clients() != 0 {
		t.Errorf("rejected upgrades registered %d clients", h.hub.Clients())
	}
	h.dial() // the right token works
}

// A request to /api/ws that is not a WebSocket upgrade gets an HTTP error.
func TestNotAnUpgrade(t *testing.T) {
	h := newHarness(t)
	req, _ := http.NewRequestWithContext(t.Context(), "GET", h.srv.URL+"/api/ws", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUpgradeRequired && resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

// Anything the client sends is ignored.
func TestClientMessagesIgnored(t *testing.T) {
	h := newHarness(t, emptySnapshot()...)
	c := h.dial()
	read(t, c)
	for _, p := range []string{`{"type":"hello"}`, "garbage", `{"type":"job_removed","job_ids":["x"]}`} {
		if err := c.Write(t.Context(), websocket.MessageText, []byte(p)); err != nil {
			t.Fatal(err)
		}
	}
	j := newJob(jobs.StatusPending, 0)
	h.bus.Publish(added(j))
	if m := read(t, c); m.Type != "job_added" {
		t.Errorf("got %s after ignored client messages", m.Raw)
	}
	if h.hub.Clients() != 1 {
		t.Errorf("clients = %d, want 1", h.hub.Clients())
	}
}

// Progress-only updates are limited per job; the last value is flushed; a
// status change is immediate and supersedes held-back progress.
func TestProgressThrottle(t *testing.T) {
	const interval = 250 * time.Millisecond
	h := newHarness(t, emptySnapshot(func(o *Options) { o.ProgressInterval = interval })...)
	c := h.dial()
	read(t, c)
	h.waitClients(1)

	j := newJob(jobs.StatusDownloading, 0)
	h.bus.Publish(added(j))
	if m := read(t, c); m.Type != "job_added" {
		t.Fatalf("got %s", m.Raw)
	}

	// 50 progress-only updates in a burst: one trailing delivery of the last.
	start := time.Now()
	for i := 1; i <= 50; i++ {
		h.bus.Publish(updated(withProgress(j, float64(i))))
	}
	var got []msg
	for time.Since(start) < interval+200*time.Millisecond {
		if m, err := tryRead(c, 50*time.Millisecond); err == nil {
			got = append(got, m)
		}
	}
	if len(got) < 1 || len(got) > 2 {
		t.Fatalf("%d deliveries for the burst, want 1 or 2", len(got))
	}
	if last := got[len(got)-1]; percentOf(last) != 50 {
		t.Errorf("last delivered percent = %v, want 50", percentOf(last))
	}

	// Held-back progress, then a status change: the change is delivered
	// immediately and carries the newest progress; nothing trails after it.
	time.Sleep(interval) // let the interval elapse
	for i := 51; i <= 60; i++ {
		h.bus.Publish(updated(withProgress(j, float64(i))))
	}
	h.bus.Publish(updated(withProgress(withStatus(j, jobs.StatusProcessing), 61)))
	var sawProcessing bool
	deadline := time.Now().Add(interval / 2)
	for time.Now().Before(deadline) {
		m, err := tryRead(c, 20*time.Millisecond)
		if err != nil {
			continue
		}
		if m.job()["status"] == "processing" {
			sawProcessing = true
			if percentOf(m) != 61 {
				t.Errorf("status change carries percent %v, want 61", percentOf(m))
			}
		}
	}
	if !sawProcessing {
		t.Error("status change was not delivered within half an interval")
	}
	expectSilence(t, c, 2*interval) // the held-back 60 was superseded

	// After a quiet interval a progress update goes out immediately again.
	h.bus.Publish(updated(withProgress(withStatus(j, jobs.StatusProcessing), 70)))
	m, err := tryRead(c, interval/2)
	if err != nil || percentOf(m) != 70 {
		t.Errorf("progress after a quiet interval: %v %v, want immediate 70", m.Raw, err)
	}
}

// Throttling is per job: a busy job does not hold back another job.
func TestProgressThrottlePerJob(t *testing.T) {
	h := newHarness(t, emptySnapshot(func(o *Options) { o.ProgressInterval = time.Hour })...)
	c := h.dial()
	read(t, c)
	a, b := newJob(jobs.StatusDownloading, 0), newJob(jobs.StatusDownloading, 0)
	h.bus.Publish(added(a))
	h.bus.Publish(added(b))
	read(t, c)
	read(t, c)
	h.bus.Publish(updated(withProgress(a, 10))) // held back for an hour
	h.bus.Publish(updated(withStatus(b, jobs.StatusPaused)))
	if m := read(t, c); m.job()["id"] != b.ID || m.job()["status"] != "paused" {
		t.Errorf("got %s, want b paused", m.Raw)
	}
	expectSilence(t, c, 100*time.Millisecond)
}

// The first update the hub sees for a job (e.g. after a restart) is not held
// back, and removal forgets the job.
func TestFirstUpdateImmediate(t *testing.T) {
	h := newHarness(t, emptySnapshot(func(o *Options) { o.ProgressInterval = time.Hour })...)
	c := h.dial()
	read(t, c)
	j := newJob(jobs.StatusDownloading, 5)
	h.bus.Publish(updated(j))
	if m := read(t, c); percentOf(m) != 5 {
		t.Errorf("got %s", m.Raw)
	}
	h.bus.Publish(service.Change{Kind: service.ChangeRemoved, JobIDs: []string{j.ID}})
	read(t, c)
	h.bus.Publish(updated(withProgress(j, 6))) // unknown again: immediate
	if m := read(t, c); percentOf(m) != 6 {
		t.Errorf("got %s", m.Raw)
	}
}

// A client that stops reading is dropped when its buffer is full; healthy
// clients are unaffected.
func TestSlowClientDropped(t *testing.T) {
	h := newHarness(t, emptySnapshot(func(o *Options) {
		o.SendBuffer = 4
		o.WriteTimeout = time.Minute // the buffer, not the write timeout, must trigger the drop
	})...)
	h.dialSilent() // never reads
	fast := h.dial()
	read(t, fast)
	h.waitClients(2)

	big := strings.Repeat("w", 256<<10)
	deadline := time.Now().Add(20 * time.Second)
	for h.hub.Clients() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("slow client not dropped; %d clients", h.hub.Clients())
		}
		j := newJob(jobs.StatusPending, 0)
		j.Warnings = []string{big}
		h.bus.Publish(added(j))
		read(t, fast) // lockstep: the healthy client keeps up, the silent one cannot
	}
	expectOpen(t, fast)
}

// A client that does not answer pings is closed after PongTimeout; one that
// does stays connected well past it.
func TestPingPongTimeout(t *testing.T) {
	h := newHarness(t, emptySnapshot(func(o *Options) {
		o.PingInterval = 40 * time.Millisecond
		o.PongTimeout = 150 * time.Millisecond
	})...)
	h.dialSilent() // never reads, so the library never answers pings
	h.waitClients(1)
	healthy := h.dial() // its reader answers pings
	h.waitClients(2)
	h.waitClients(1)                   // the silent one went away
	time.Sleep(500 * time.Millisecond) // > 3x PongTimeout
	if h.hub.Clients() != 1 {
		t.Errorf("healthy client was dropped; %d clients", h.hub.Clients())
	}
	expectOpen(t, healthy)
}

func TestClientDisconnectCleansUp(t *testing.T) {
	h := newHarness(t, emptySnapshot()...)
	c := h.dial()
	read(t, c)
	h.waitClients(1)
	_ = c.Close(websocket.StatusNormalClosure, "bye")
	h.waitClients(0)
	// A second one drops the TCP connection abruptly.
	c2 := h.dial()
	read(t, c2)
	h.waitClients(1)
	_ = c2.CloseNow()
	h.waitClients(0)
}

// Close disconnects clients with 1001, returns, is idempotent and makes later
// upgrades fail.
func TestCloseDisconnectsClients(t *testing.T) {
	h := newHarness(t, emptySnapshot()...)
	c1, c2 := h.dial(), h.dial()
	read(t, c1)
	read(t, c2)
	h.waitClients(2)

	done := make(chan struct{})
	go func() { h.hub.Close(); h.hub.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return")
	}
	for i, c := range []*wsc{c1, c2} {
		err := waitClosed(t, c)
		if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
			t.Errorf("client %d: close status = %v (%v), want going away", i, got, err)
		}
	}
	if h.hub.Clients() != 0 {
		t.Errorf("clients = %d after Close", h.hub.Clients())
	}
	if h.bus.Subscribers() != 0 {
		t.Errorf("hub left %d bus subscriptions", h.bus.Subscribers())
	}
	ctx, cancel := timeoutCtx(t)
	defer cancel()
	c, resp, err := websocket.Dial(ctx, h.wsURL(), &websocket.DialOptions{HTTPHeader: authHeader(testToken)})
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("dial after Close succeeded")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("dial after Close: %v, want 503", resp)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
}

// If the hub falls behind the bus it loses events; it must disconnect its
// clients (they resync via a fresh snapshot) and keep working.
func TestBusOverflowForcesResync(t *testing.T) {
	h := newHarness(t, emptySnapshot(func(o *Options) { o.BusBuffer = 2 })...)
	c := h.dial()
	read(t, c)
	h.waitClients(1)

	// Stall the dispatch loop inside broadcast, then overflow its queue.
	h.hub.mu.Lock()
	for range 10 {
		h.bus.Publish(added(newJob(jobs.StatusPending, 0)))
	}
	h.hub.mu.Unlock()

	err := waitClosed(t, c) // events queued before the drop may arrive first
	if got := websocket.CloseStatus(err); got != websocket.StatusTryAgainLater {
		t.Errorf("close status = %v (%v), want try again later", got, err)
	}
	h.waitClients(0)

	// The hub resubscribed and works for new clients.
	deadline := time.Now().Add(readTimeout)
	for h.bus.Subscribers() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("hub did not resubscribe (%d subscribers)", h.bus.Subscribers())
		}
		time.Sleep(5 * time.Millisecond)
	}
	c2 := h.dial()
	read(t, c2)
	j := newJob(jobs.StatusPending, 0)
	h.bus.Publish(added(j))
	if m := read(t, c2); m.job()["id"] != j.ID {
		t.Errorf("got %s", m.Raw)
	}
}

// The connection must outlive http.Server's Read/WriteTimeout: Hijack clears
// the connection deadlines.
func TestSurvivesServerTimeouts(t *testing.T) {
	h := newHarness(t, emptySnapshot()...)
	srv := httptest.NewUnstartedServer(h.srv.Config.Handler)
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Config.WriteTimeout = 200 * time.Millisecond
	srv.Start()
	defer srv.Close()
	c := h.dialURL("ws" + strings.TrimPrefix(srv.URL, "http") + "/api/ws")
	read(t, c)
	time.Sleep(700 * time.Millisecond)
	h.bus.Publish(added(newJob(jobs.StatusPending, 0)))
	if m := read(t, c); m.Type != "job_added" {
		t.Errorf("got %s", m.Raw)
	}
}

// expectOpen asserts that the connection has not ended.
func expectOpen(t *testing.T, c *wsc) {
	t.Helper()
	select {
	case err := <-c.errc:
		t.Errorf("connection ended: %v", err)
	default:
	}
}
