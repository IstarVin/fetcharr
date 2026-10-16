package process

import "context"

type targetLock struct {
	busy chan struct{}
	refs int
}

// lockTarget serializes placements of one library item, including all video
// extensions and sidecars. Waiters remain cancellable before SetPlacing.
func (p *Processor) lockTarget(ctx context.Context, key string) (func(), error) {
	p.targetMu.Lock()
	if p.targets == nil {
		p.targets = make(map[string]*targetLock)
	}
	l := p.targets[key]
	if l == nil {
		l = &targetLock{busy: make(chan struct{}, 1)}
		p.targets[key] = l
	}
	l.refs++
	p.targetMu.Unlock()

	drop := func() {
		p.targetMu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(p.targets, key)
		}
		p.targetMu.Unlock()
	}
	select {
	case l.busy <- struct{}{}:
		return func() { <-l.busy; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, context.Cause(ctx)
	}
}
