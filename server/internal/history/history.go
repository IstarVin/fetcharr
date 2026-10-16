// Package history prunes old finished jobs (spec §4.6). It removes database
// records only, never files.
package history

import (
	"context"
	"log/slog"
	"time"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// DefaultInterval is how often Run prunes after the startup pass.
const DefaultInterval = time.Hour

// Options configures New. Store is required.
type Options struct {
	Store *store.Store
	// Publisher receives a job_removed change for every pruning that deleted
	// rows (default: none).
	Publisher service.Publisher
	Logger    *slog.Logger
	// RetentionDays is history.retention_days; 0 disables pruning.
	RetentionDays        int
	CurrentRetentionDays func() int
	// Interval is the period of Run (default DefaultInterval).
	Interval time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// Pruner deletes completed, failed and cancelled jobs whose finished_at is
// older than the retention.
type Pruner struct {
	st          *store.Store
	pub         service.Publisher
	log         *slog.Logger
	days        int
	currentDays func() int
	wake        chan struct{}
	interval    time.Duration
	now         func() time.Time
}

// New returns a Pruner.
func New(o Options) *Pruner {
	p := &Pruner{
		st: o.Store, pub: o.Publisher, log: o.Logger, days: o.RetentionDays,
		interval: o.Interval, now: o.Now, currentDays: o.CurrentRetentionDays, wake: make(chan struct{}, 1),
	}
	if p.log == nil {
		p.log = slog.Default()
	}
	if p.interval <= 0 {
		p.interval = DefaultInterval
	}
	if p.now == nil {
		p.now = time.Now
	}
	return p
}

func (p *Pruner) retentionDays() int {
	if p.currentDays != nil {
		return p.currentDays()
	}
	return p.days
}

// Enabled reports whether retention is configured.
func (p *Pruner) Enabled() bool { return p.retentionDays() > 0 }

// Prune deletes the expired jobs, publishes job_removed with their ids and
// returns how many were deleted. It does nothing when retention is 0.
func (p *Pruner) Prune(ctx context.Context) (int, error) {
	days := p.retentionDays()
	if days <= 0 {
		return 0, nil
	}
	cutoff := p.now().Add(-time.Duration(days) * 24 * time.Hour)
	ids, err := p.st.DeleteMany(ctx, jobs.FinishedStatuses(), &cutoff)
	if err != nil {
		return 0, err
	}
	if len(ids) > 0 {
		if p.pub != nil {
			p.pub.Publish(service.Change{Kind: service.ChangeRemoved, JobIDs: ids})
		}
		p.log.Info("pruned history", "jobs", len(ids), "retention_days", days)
	}
	return len(ids), nil
}

// Run prunes once at startup and then every Interval until ctx is done.
// Errors are logged. It returns at once when retention is 0.
func (p *Pruner) Run(ctx context.Context) {
	if !p.Enabled() && p.currentDays == nil {
		return
	}
	p.pruneLogged(ctx)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-p.wake:
			p.pruneLogged(ctx)
		case <-t.C:
			p.pruneLogged(ctx)
		}
	}
}

func (p *Pruner) pruneLogged(ctx context.Context) {
	if _, err := p.Prune(ctx); err != nil && ctx.Err() == nil {
		p.log.Error("history pruning failed", "err", err)
	}
}

// Wake requests an immediate pruning pass after a settings change.
func (p *Pruner) Wake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}
