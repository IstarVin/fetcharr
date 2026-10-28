// Package notify pushes finished-job notifications to ntfy topics and
// generic webhooks, so users hear about results without an open client.
//
// It subscribes to the event bus and reacts to jobs that reach completed or
// failed. A batch is announced once, as a summary, when none of its episodes
// is active any more; an episode retried after that summary gets its own
// notification, like the Android client. Delivery is best effort: failures
// are logged without the target URL or token, and never affect jobs.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/events"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// Defaults for Options.
const (
	DefaultTimeout = 15 * time.Second
	// rememberBatches bounds the summaries kept to tell a late retry from an
	// episode the summary already covered.
	rememberBatches = 500
	rememberJobs    = 1000
	busBuffer       = 256
)

// Event names in webhook payloads.
const (
	EventCompleted     = "completed"
	EventFailed        = "failed"
	EventBatchFinished = "batch_finished"
	EventTest          = "test"
)

// Message is one notification.
type Message struct {
	Event string
	Title string // short heading, e.g. "Downloaded"
	Body  string // e.g. "Dune Part Two (2024)"
	Job   *store.Job
	// completed and failed decide which targets receive a batch summary.
	completed, failed bool
}

// Text is the one-line form used by chat webhooks.
func (m Message) Text() string { return m.Title + " · " + m.Body }

// Options configures New.
type Options struct {
	Bus     *events.Bus
	Store   *store.Store
	Targets func() []config.Notification
	Logger  *slog.Logger
	// HTTPClient sends the requests (default: a plain client; Timeout is
	// applied through the request context).
	HTTPClient *http.Client
	Timeout    time.Duration
}

// Notifier delivers notifications. Create it with New and call Run.
type Notifier struct {
	bus     *events.Bus
	st      *store.Store
	targets func() []config.Notification
	log     *slog.Logger
	hc      *http.Client
	timeout time.Duration

	// covered maps a summarized batch to the finish times of the episodes
	// its summary counted (job id -> finished_at).
	covered map[string]map[string]time.Time
	order   []string
	// sent holds recently announced job results (id + finished_at), so a
	// repeated change event is not announced twice.
	sent      map[string]bool
	sentOrder []string
	sends     sync.WaitGroup
}

// New returns a Notifier.
func New(o Options) *Notifier {
	n := &Notifier{bus: o.Bus, st: o.Store, targets: o.Targets, log: o.Logger, hc: o.HTTPClient, timeout: o.Timeout,
		covered: map[string]map[string]time.Time{}, sent: map[string]bool{}}
	if n.log == nil {
		n.log = slog.Default()
	}
	if n.hc == nil {
		n.hc = &http.Client{}
	}
	if n.timeout <= 0 {
		n.timeout = DefaultTimeout
	}
	if n.targets == nil {
		n.targets = func() []config.Notification { return nil }
	}
	return n
}

// Run handles bus changes until ctx is done, then waits for sends in flight.
// A subscription dropped for overflow is renewed; the changes it missed are
// not notified.
func (n *Notifier) Run(ctx context.Context) {
	defer n.sends.Wait()
	for ctx.Err() == nil {
		sub := n.bus.Subscribe(busBuffer)
		n.consume(ctx, sub)
		sub.Close()
		if errors.Is(sub.Err(), events.ErrOverflow) {
			n.log.Warn("notifications fell behind; some results were not sent")
			continue
		}
		return // bus closed or ctx done
	}
}

func (n *Notifier) consume(ctx context.Context, sub *events.Subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-sub.C:
			if !ok {
				return
			}
			if c.Kind == service.ChangeUpdated && c.Job != nil {
				n.handle(ctx, c.Job)
			}
		}
	}
}

// handle runs in the subscription goroutine, so batch bookkeeping needs no lock.
func (n *Notifier) handle(ctx context.Context, j *store.Job) {
	if (j.Status != jobs.StatusCompleted && j.Status != jobs.StatusFailed) || j.FinishedAt == nil {
		return
	}
	if len(n.targets()) == 0 {
		return
	}
	key := j.ID + "@" + j.FinishedAt.UTC().Format(time.RFC3339Nano)
	if n.sent[key] {
		return
	}
	n.sent[key] = true
	n.sentOrder = append(n.sentOrder, key)
	if len(n.sentOrder) > rememberJobs {
		delete(n.sent, n.sentOrder[0])
		n.sentOrder = n.sentOrder[1:]
	}
	if j.BatchID == nil {
		n.dispatch(jobMessage(j))
		return
	}
	batchID := *j.BatchID
	if seen, ok := n.covered[batchID]; ok {
		if at, counted := seen[j.ID]; counted && at.Equal(*j.FinishedAt) {
			return // part of the summary already sent
		}
		n.dispatch(jobMessage(j)) // retried after the summary
		return
	}
	members, err := n.st.ListByBatch(ctx, batchID)
	if err != nil {
		n.log.Warn("notification: cannot read batch", "batch", batchID, "err", err)
		return
	}
	finished := map[string]time.Time{}
	completed, failed := 0, 0
	for _, m := range members {
		if jobs.IsActive(m.Status) {
			return // announced when the last episode finishes
		}
		if m.FinishedAt != nil {
			finished[m.ID] = *m.FinishedAt
		}
		switch m.Status {
		case jobs.StatusCompleted:
			completed++
		case jobs.StatusFailed:
			failed++
		}
	}
	n.remember(batchID, finished)
	if completed+failed > 0 {
		n.dispatch(batchMessage(j, completed, failed))
	}
}

func (n *Notifier) remember(batchID string, finished map[string]time.Time) {
	n.covered[batchID] = finished
	n.order = append(n.order, batchID)
	if len(n.order) > rememberBatches {
		delete(n.covered, n.order[0])
		n.order = n.order[1:]
	}
}

// dispatch sends m to every subscribed target in the background.
func (n *Notifier) dispatch(m Message) {
	for i, t := range n.targets() {
		if !wants(t, m) {
			continue
		}
		n.sends.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), n.timeout)
			defer cancel()
			if err := n.Send(ctx, t, m); err != nil {
				n.log.Warn("notification failed", "target", i, "type", t.Type, "err", err)
			}
		})
	}
}

func wants(t config.Notification, m Message) bool {
	switch m.Event {
	case EventCompleted:
		return t.OnCompleted
	case EventFailed:
		return t.OnFailed
	case EventBatchFinished:
		return m.completed && t.OnCompleted || m.failed && t.OnFailed
	}
	return true
}

// Test sends a test message to t and returns the delivery error, if any.
func (n *Notifier) Test(ctx context.Context, t config.Notification) error {
	ctx, cancel := context.WithTimeout(ctx, n.timeout)
	defer cancel()
	return n.Send(ctx, t, Message{Event: EventTest, Title: "Fetcharr", Body: "Test notification — this target works."})
}

// Send delivers m to t. The returned error never contains the target URL.
func (n *Notifier) Send(ctx context.Context, t config.Notification, m Message) error {
	var req *http.Request
	var err error
	switch t.Type {
	case config.NotificationNtfy:
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewBufferString(m.Body))
		if err == nil {
			req.Header.Set("Content-Type", "text/plain; charset=utf-8")
			req.Header.Set("Title", mime.QEncoding.Encode("utf-8", m.Title))
			req.Header.Set("Tags", ntfyTag(m))
		}
	case config.NotificationWebhook:
		var body []byte
		body, err = json.Marshal(webhookPayload(m))
		if err == nil {
			req, err = http.NewRequestWithContext(ctx, http.MethodPost, t.URL, bytes.NewReader(body))
		}
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
		}
	default:
		return fmt.Errorf("unknown notification type %q", t.Type)
	}
	if err != nil {
		return stripURL(err)
	}
	req.Header.Set("User-Agent", "fetcharr")
	if t.Token != "" {
		req.Header.Set("Authorization", "Bearer "+t.Token)
	}
	resp, err := n.hc.Do(req)
	if err != nil {
		return stripURL(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the target answered %s", resp.Status)
	}
	return nil
}

// stripURL drops the request URL from transport errors: webhook URLs often
// embed their secret.
func stripURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func ntfyTag(m Message) string {
	switch {
	case m.Event == EventFailed, m.Event == EventBatchFinished && m.failed:
		return "warning"
	case m.Event == EventTest:
		return "bell"
	}
	return "white_check_mark"
}

// jobDTO is the job summary in webhook payloads. It omits the source URL and
// headers, which may carry credentials.
type jobDTO struct {
	ID        string  `json:"id"`
	BatchID   *string `json:"batch_id"`
	Category  string  `json:"category"`
	Status    string  `json:"status"`
	Title     *string `json:"title"`
	ShowName  *string `json:"show_name"`
	Season    *int    `json:"season"`
	Episode   *int    `json:"episode"`
	Year      *int    `json:"year"`
	FilePath  *string `json:"file_path"`
	ErrorCode *string `json:"error_code"`
}

// webhookPayload carries "text" and "content" as well, so Slack- and
// Discord-style incoming webhooks can display it unchanged.
func webhookPayload(m Message) map[string]any {
	p := map[string]any{"event": m.Event, "title": m.Title, "message": m.Body, "text": m.Text(), "content": m.Text()}
	if j := m.Job; j != nil {
		p["job"] = jobDTO{j.ID, j.BatchID, j.Category, string(j.Status), j.Title, j.ShowName, j.Season, j.Episode, j.Year, j.FilePath, j.ErrorCode}
	}
	return p
}
