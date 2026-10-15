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
	RetentionDays int
	// Interval is the period of Run (default DefaultInterval).
	Interval time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// Pruner deletes completed, failed and cancelled jobs whose finished_at is
// older than the retention.
type Pruner struct {
	st       *store.Store
	pub      service.Publisher
	log      *slog.Logger
	days     int
	interval time.Duration
	now      func() time.Time
}

// New returns a Pruner.
func New(o Options) *Pruner {
	p := &Pruner{
		st: o.Store, pub: o.Publisher, log: o.Logger, days: o.RetentionDays,
		interval: o.Interval, now: o.Now,
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

// Enabled reports whether retention is configured.
func (p *Pruner) Enabled() bool { return p.days > 0 }

// Prune deletes the expired jobs, publishes job_removed with their ids and
// returns how many were deleted. It does nothing when retention is 0.
func (p *Pruner) Prune(ctx context.Context) (int, error) {
	if !p.Enabled() {
		return 0, nil
	}
	cutoff := p.now().Add(-time.Duration(p.days) * 24 * time.Hour)
	ids, err := p.st.DeleteMany(ctx, jobs.FinishedStatuses(), &cutoff)
	if err != nil {
		return 0, err
	}
	if len(ids) > 0 {
		if p.pub != nil {
			p.pub.Publish(service.Change{Kind: service.ChangeRemoved, JobIDs: ids})
		}
		p.log.Info("pruned history", "jobs", len(ids), "retention_days", p.days)
	}
	return len(ids), nil
}

// Run prunes once at startup and then every Interval until ctx is done.
// Errors are logged. It returns at once when retention is 0.
func (p *Pruner) Run(ctx context.Context) {
	if !p.Enabled() {
		return
	}
	p.pruneLogged(ctx)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
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
