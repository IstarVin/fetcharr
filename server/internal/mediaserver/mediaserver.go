// Package mediaserver tells Jellyfin and Plex to rescan a library folder
// after a job completed (spec §4.5).
//
// Completions are queued per folder and debounced: a folder is refreshed once
// the debounce window (30 s by default) passed without another completion in
// it, so a batch of episodes finishing close together triggers one scan. The
// window restarts on every repeat, but a folder is refreshed at the latest
// MaxWait after its first queued completion, so a steady stream of
// completions cannot postpone the scan forever. Failures are logged and
// otherwise ignored. API keys and tokens are only sent in request headers and
// never logged.
package mediaserver

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/logsafe"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// Defaults for Options.
const (
	DefaultDebounce = 30 * time.Second
	DefaultMaxWait  = 5 * time.Minute
	DefaultTimeout  = 30 * time.Second
)

// Options configures New.
type Options struct {
	// Servers are the media_servers config entries. Entries of an unknown
	// type are skipped with a warning.
	Servers []config.MediaServer
	Logger  *slog.Logger
	// Debounce is the per-folder quiet period (default DefaultDebounce).
	Debounce time.Duration
	// MaxWait caps how long repeated completions can postpone a folder's
	// refresh (default DefaultMaxWait; never less than Debounce).
	MaxWait time.Duration
	// Timeout bounds one media server interaction (default DefaultTimeout).
	Timeout time.Duration
	// HTTPClient sends the requests (default: a client with no own timeout;
	// Timeout is applied through the request context).
	HTTPClient *http.Client
}

// client is one configured media server.
type client interface {
	// name is the server type, for logs.
	name() string
	// refresh asks the server to rescan folder (already path-mapped).
	refresh(ctx context.Context, folder string) error
	// mapPath rewrites a local folder into the server's view.
	mapPath(folder string) string
}

// Refresher debounces folder refreshes and sends them to every configured
// media server. Create it with New; it is safe for concurrent use.
type Refresher struct {
	log      *slog.Logger
	clients  []client
	debounce time.Duration
	maxWait  time.Duration
	timeout  time.Duration

	baseCtx context.Context
	cancel  context.CancelFunc

	mu      sync.Mutex
	pending map[string]*entry
	closed  bool
	wg      sync.WaitGroup // in-flight refreshes
}

type entry struct {
	timer *time.Timer
	first time.Time
}

// New returns a Refresher. With no usable servers it still works but sends
// nothing.
func New(o Options) *Refresher {
	r := &Refresher{
		log: o.Logger, debounce: o.Debounce, maxWait: o.MaxWait, timeout: o.Timeout,
		pending: make(map[string]*entry),
	}
	if r.log == nil {
		r.log = slog.Default()
	}
	if r.debounce <= 0 {
		r.debounce = DefaultDebounce
	}
	if r.maxWait <= 0 {
		r.maxWait = DefaultMaxWait
	}
	r.maxWait = max(r.maxWait, r.debounce)
	if r.timeout <= 0 {
		r.timeout = DefaultTimeout
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	for _, s := range o.Servers {
		base := strings.TrimRight(s.URL, "/")
		switch s.Type {
		case config.MediaServerJellyfin:
			r.clients = append(r.clients, &jellyfin{base: base, key: s.APIKey, paths: newPathMap(s.PathMap), hc: hc})
		case config.MediaServerPlex:
			r.clients = append(r.clients, &plex{base: base, token: s.Token, paths: newPathMap(s.PathMap), hc: hc})
		default:
			r.log.Warn("ignoring media server of unknown type", "type", s.Type)
		}
	}
	r.baseCtx, r.cancel = context.WithCancel(context.Background())
	return r
}

// Enabled reports whether at least one media server is configured.
func (r *Refresher) Enabled() bool { return len(r.clients) > 0 }

// OnCompleted has the signature of process.Options.OnCompleted. It queues
// the folder; the job is not used (and never logged).
func (r *Refresher) OnCompleted(_ *store.Job, folder string) { r.Queue(folder) }

// Queue schedules a refresh of folder (a local path). Repeated calls for the
// same folder within the debounce window coalesce into one refresh; different
// folders are independent. It never blocks, and is a no-op after Close.
func (r *Refresher) Queue(folder string) {
	if folder == "" || len(r.clients) == 0 {
		return
	}
	folder = filepath.Clean(folder)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	now := time.Now()
	if e := r.pending[folder]; e != nil {
		if delay := min(r.debounce, e.first.Add(r.maxWait).Sub(now)); delay > 0 {
			e.timer.Reset(delay)
		}
		return
	}
	e := &entry{first: now}
	e.timer = time.AfterFunc(r.debounce, func() { r.fire(folder, e) })
	r.pending[folder] = e
}

// fire runs when a folder's timer expired.
func (r *Refresher) fire(folder string, e *entry) {
	r.mu.Lock()
	if r.closed || r.pending[folder] != e {
		r.mu.Unlock()
		return // Close flushed (or will flush) it
	}
	delete(r.pending, folder)
	r.wg.Add(1)
	r.mu.Unlock()
	defer r.wg.Done()
	r.send(folder)
}

// send refreshes folder on every server, in parallel, and logs failures.
func (r *Refresher) send(folder string) {
	var wg sync.WaitGroup
	for _, c := range r.clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if p := recover(); p != nil {
					r.log.Error("media server refresh panicked", "server", c.name())
				}
			}()
			ctx, cancel := context.WithTimeout(r.baseCtx, r.timeout)
			defer cancel()
			if err := c.refresh(ctx, c.mapPath(folder)); err != nil {
				r.log.Warn("media server refresh failed", "server", c.name(), "folder", folder, "err", logsafe.Err(err))
				return
			}
			r.log.Info("media server refresh requested", "server", c.name(), "folder", folder)
		}()
	}
	wg.Wait()
}

// Close stops the debouncing. Refreshes still waiting for their window are
// sent immediately (their files are already in the library, and dropping
// them would leave the media server unaware of finished downloads until its
// next scan). Close waits for all in-flight refreshes until ctx is done; then
// it aborts them. Queue is a no-op afterwards. Close is idempotent.
func (r *Refresher) Close(ctx context.Context) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
	} else {
		r.closed = true
		flush := make([]string, 0, len(r.pending))
		for f, e := range r.pending {
			e.timer.Stop()
			flush = append(flush, f)
		}
		clear(r.pending)
		sort.Strings(flush)
		r.wg.Add(len(flush))
		r.mu.Unlock()
		for _, f := range flush {
			go func() {
				defer r.wg.Done()
				r.send(f)
			}()
		}
	}
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		r.cancel()
		<-done
	}
	r.cancel()
}
