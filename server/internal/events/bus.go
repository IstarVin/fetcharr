// Package events is the in-process event bus between the job service and its
// observers (the WebSocket hub today; later phases may add more).
//
// The Bus implements service.Publisher. Publish never blocks: every subscriber
// owns a bounded queue, and a subscriber that cannot keep up is disconnected
// (its channel is closed and Err reports ErrOverflow) instead of slowing the
// publisher down or growing memory without bound. Such a subscriber must
// resynchronise from the store before it subscribes again.
package events

import (
	"errors"
	"sync"

	"github.com/istarvin/fetcharr/server/internal/service"
)

// ErrOverflow is the Subscription.Err of a subscriber that was dropped
// because its queue was full.
var ErrOverflow = errors.New("events: subscriber queue overflow")

// Bus fans every published Change out to all subscribers. The zero value is
// not usable; use NewBus.
type Bus struct {
	mu     sync.Mutex
	subs   map[*Subscription]struct{}
	closed bool
}

// NewBus returns an empty bus.
func NewBus() *Bus {
	return &Bus{subs: make(map[*Subscription]struct{})}
}

var _ service.Publisher = (*Bus)(nil)

// Subscription is one subscriber's view of the bus.
type Subscription struct {
	// C delivers changes in publish order. It is closed when the subscription
	// ends: after Close, after Bus.Close, or after an overflow (see Err).
	C <-chan service.Change

	bus *Bus
	ch  chan service.Change
	err error // guarded by bus.mu; set before ch is closed
}

// Publish delivers c to every subscriber and never blocks. The Job inside c is
// shared between subscribers and must be treated as read-only. A subscriber
// whose queue is full is dropped. Publishing on a closed bus is a no-op.
func (b *Bus) Publish(c service.Change) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for s := range b.subs {
		select {
		case s.ch <- c:
		default:
			s.err = ErrOverflow
			b.removeLocked(s)
		}
	}
}

// Subscribe registers a subscriber with a queue of buffer changes (minimum 1).
// On a closed bus it returns an already-closed subscription.
func (b *Bus) Subscribe(buffer int) *Subscription {
	ch := make(chan service.Change, max(buffer, 1))
	s := &Subscription{C: ch, bus: b, ch: ch}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		close(ch)
		return s
	}
	b.subs[s] = struct{}{}
	return s
}

// Close ends the subscription and closes C. It is idempotent.
func (s *Subscription) Close() {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	s.bus.removeLocked(s)
}

// Err reports why the subscription ended: ErrOverflow after a drop, nil
// after Close or Bus.Close (and while it is still active).
func (s *Subscription) Err() error {
	s.bus.mu.Lock()
	defer s.bus.mu.Unlock()
	return s.err
}

func (b *Bus) removeLocked(s *Subscription) {
	if _, ok := b.subs[s]; !ok {
		return
	}
	delete(b.subs, s)
	close(s.ch)
}

// Close ends every subscription and turns later Publish calls into no-ops.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for s := range b.subs {
		b.removeLocked(s)
	}
}

// Subscribers returns the number of active subscriptions (for tests and
// diagnostics).
func (b *Bus) Subscribers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
