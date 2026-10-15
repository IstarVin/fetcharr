// Package ws serves the WebSocket endpoint GET /api/ws (spec §7).
//
// A Hub subscribes once to the events.Bus, turns service.Changes into wire
// messages, throttles progress-only updates and fans the resulting messages
// out to every connected client. Each client gets a snapshot first, then the
// live messages.
//
// # Progress throttling
//
// Throttling is decided here, not by the publisher. The hub remembers, per
// job, the last job object it broadcast without its progress field ("base").
// A job_updated whose base equals the remembered one changed progress only:
// it is broadcast at most once per ProgressInterval per job, and while it is
// held back only the newest one is kept and flushed when the interval has
// elapsed (trailing flush), so the last progress value is always delivered.
// Anything else (a status change, an error, warnings, file_path, ...,
// job_added, job_removed) is broadcast at once, supersedes a held-back
// progress update of that job, and restarts the job's interval. The first
// update the hub sees for a job is never held back. Publishers therefore
// need no flag: they publish every update as service.ChangeUpdated.
package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/istarvin/fetcharr/server/internal/api"
	"github.com/istarvin/fetcharr/server/internal/events"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// Defaults for Options.
const (
	DefaultProgressInterval = time.Second
	DefaultPingInterval     = 30 * time.Second
	DefaultPongTimeout      = 60 * time.Second
	DefaultWriteTimeout     = 10 * time.Second
	DefaultSendBuffer       = 64
	DefaultBusBuffer        = 1024

	// readLimit caps the size of a message a client may send; messages are
	// ignored (spec §7), a larger one ends the connection.
	readLimit = 4096
)

// SnapshotFunc returns every job that is not completed, failed or cancelled,
// newest created_at first (contract/README.md).
type SnapshotFunc func(ctx context.Context) ([]*store.Job, error)

// Options configures New. Bus and Snapshot are required; every other zero
// value selects the default.
type Options struct {
	Bus      *events.Bus
	Snapshot SnapshotFunc
	Logger   *slog.Logger

	// ProgressInterval is the minimum time between two progress-only updates
	// of one job (default 1 s).
	ProgressInterval time.Duration
	// PingInterval is the time between pings (default 30 s).
	PingInterval time.Duration
	// PongTimeout is how long a ping may stay unanswered before the
	// connection is closed (default 60 s).
	PongTimeout time.Duration
	// WriteTimeout bounds one message write (default 10 s).
	WriteTimeout time.Duration
	// SendBuffer is the number of messages queued per client; a client that
	// falls further behind is disconnected (default 64).
	SendBuffer int
	// BusBuffer is the hub's own subscription queue on the bus (default 1024).
	// If it ever overflows, all clients are disconnected so that they resync.
	BusBuffer int
}

// Hub is the http.Handler of GET /api/ws.
type Hub struct {
	opts Options
	log  *slog.Logger

	mu      sync.Mutex
	clients map[*client]struct{}
	closed  bool

	quit      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup // run loop and every ServeHTTP
}

// New creates a Hub and starts its dispatch goroutine; stop it with Close.
func New(opts Options) *Hub {
	if opts.ProgressInterval <= 0 {
		opts.ProgressInterval = DefaultProgressInterval
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = DefaultPingInterval
	}
	if opts.PongTimeout <= 0 {
		opts.PongTimeout = DefaultPongTimeout
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = DefaultWriteTimeout
	}
	if opts.SendBuffer <= 0 {
		opts.SendBuffer = DefaultSendBuffer
	}
	if opts.BusBuffer <= 0 {
		opts.BusBuffer = DefaultBusBuffer
	}
	h := &Hub{opts: opts, log: opts.Logger, clients: make(map[*client]struct{}), quit: make(chan struct{})}
	if h.log == nil {
		h.log = slog.Default()
	}
	// Subscribe before returning so that nothing published after New is missed.
	sub := opts.Bus.Subscribe(opts.BusBuffer)
	h.wg.Add(1)
	go h.run(sub)
	return h
}

// Clients returns the number of connected clients.
func (h *Hub) Clients() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Close disconnects every client (close code 1001), stops the dispatch
// goroutine and waits for all of them. It is idempotent. Hijacked WebSocket
// connections are not tracked by http.Server, so call Close before
// http.Server.Shutdown.
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		for c := range h.clients {
			c.fail(websocket.StatusGoingAway, "server shutting down", nil)
		}
		h.mu.Unlock()
		close(h.quit)
	})
	h.wg.Wait()
}

// ServeHTTP upgrades the request and serves one client until it disconnects,
// is dropped, or the hub closes. Authentication is the caller's business (the
// api package mounts the hub behind its bearer-token middleware, so it runs
// on the upgrade request).
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.isClosed() {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the HTTP error response
	}
	conn.SetReadLimit(readLimit)
	c := newClient(h, conn, r.RemoteAddr)

	// Register before reading the snapshot: every change committed from now
	// on is queued for this client, so none can be lost. A change that is
	// also reflected in the snapshot is delivered again afterwards; clients
	// apply job_added / job_updated as upserts, so that is harmless.
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
		return
	}
	h.clients[c] = struct{}{}
	h.wg.Add(1)
	h.mu.Unlock()
	defer h.wg.Done()
	h.log.Info("websocket connected", "remote", c.remote)

	c.serve()

	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
	h.log.Info("websocket disconnected", "remote", c.remote, "reason", c.reason())
}

// ---- messages ----------------------------------------------------------

type snapshotMsg struct {
	Type string       `json:"type"`
	Jobs []api.JobDTO `json:"jobs"`
}

type jobMsg struct {
	Type string     `json:"type"`
	Job  api.JobDTO `json:"job"`
}

type removedMsg struct {
	Type   string   `json:"type"`
	JobIDs []string `json:"job_ids"`
}

func encode(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // the message types above always marshal
	}
	return b
}

// ---- dispatch loop & throttle -----------------------------------------

type jobState struct {
	base     []byte      // last broadcast job object without its progress
	lastSent time.Time   // when anything was last broadcast for the job
	pending  *api.JobDTO // newest held-back progress-only update
}

func (h *Hub) run(sub *events.Subscription) {
	defer h.wg.Done()
	defer func() { sub.Close() }() // sub is replaced after a resync
	states := make(map[string]*jobState)
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()

	for {
		select {
		case <-h.quit:
			return
		case ch, ok := <-sub.C:
			if !ok {
				if h.isClosed() {
					return
				}
				// The bus dropped us (queue overflow): events were lost, so
				// make every client reconnect and take a fresh snapshot.
				h.log.Warn("websocket hub fell behind the event bus; disconnecting clients", "err", sub.Err())
				h.dropAll("resync required")
				clear(states)
				sub = h.opts.Bus.Subscribe(h.opts.BusBuffer)
				continue
			}
			h.handle(states, ch, time.Now())
		case <-timer.C:
			h.flush(states, time.Now())
		}
		h.arm(timer, states)
	}
}

func (h *Hub) isClosed() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

func (h *Hub) handle(states map[string]*jobState, ch service.Change, now time.Time) {
	switch ch.Kind {
	case service.ChangeRemoved:
		for _, id := range ch.JobIDs {
			delete(states, id)
		}
		h.broadcast(encode(removedMsg{Type: string(service.ChangeRemoved), JobIDs: append([]string{}, ch.JobIDs...)}))
	case service.ChangeAdded, service.ChangeUpdated:
		if ch.Job == nil {
			return
		}
		dto := api.NewJobDTO(ch.Job)
		base := baseOf(dto)
		st := states[dto.ID]
		if ch.Kind == service.ChangeUpdated && st != nil && bytes.Equal(st.base, base) {
			// Progress-only update.
			if now.Sub(st.lastSent) >= h.opts.ProgressInterval {
				h.send(st, service.ChangeUpdated, dto, now)
			} else {
				st.pending = &dto
			}
			return
		}
		if st == nil {
			st = &jobState{}
			states[dto.ID] = st
		}
		st.base, st.pending = base, nil
		h.send(st, ch.Kind, dto, now)
		if jobs.IsFinished(jobs.Status(dto.Status)) {
			delete(states, dto.ID) // no progress updates follow
		}
	}
}

func (h *Hub) send(st *jobState, kind service.ChangeKind, dto api.JobDTO, now time.Time) {
	st.lastSent, st.pending = now, nil
	h.broadcast(encode(jobMsg{Type: string(kind), Job: dto}))
}

// flush sends every held-back progress update whose interval has elapsed.
func (h *Hub) flush(states map[string]*jobState, now time.Time) {
	for _, st := range states {
		if st.pending != nil && now.Sub(st.lastSent) >= h.opts.ProgressInterval {
			h.send(st, service.ChangeUpdated, *st.pending, now)
		}
	}
}

// arm schedules the timer for the earliest held-back update, or stops it.
func (h *Hub) arm(timer *time.Timer, states map[string]*jobState) {
	var next time.Time
	for _, st := range states {
		if st.pending == nil {
			continue
		}
		if due := st.lastSent.Add(h.opts.ProgressInterval); next.IsZero() || due.Before(next) {
			next = due
		}
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	if !next.IsZero() {
		timer.Reset(max(time.Until(next), 0))
	}
}

// baseOf serialises dto without its progress.
func baseOf(dto api.JobDTO) []byte {
	dto.Progress = nil
	return encode(dto)
}

// broadcast queues msg for every client; a client with a full queue is
// dropped.
func (h *Hub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- msg:
		default:
			// No close handshake: the writer is most likely stuck in a write.
			c.fail(0, "send buffer full", errSlowClient)
		}
	}
}

func (h *Hub) dropAll(reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.fail(websocket.StatusTryAgainLater, reason, nil)
	}
}

var errSlowClient = errors.New("send buffer full")
