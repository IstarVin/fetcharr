package events

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/service"
)

func ch(ids ...string) service.Change {
	return service.Change{Kind: service.ChangeRemoved, JobIDs: ids}
}

func recv(t *testing.T, s *Subscription) service.Change {
	t.Helper()
	select {
	case c, ok := <-s.C:
		if !ok {
			t.Fatal("subscription closed")
		}
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("no change delivered")
	}
	return service.Change{}
}

func TestFanOutInOrder(t *testing.T) {
	b := NewBus()
	s1, s2 := b.Subscribe(8), b.Subscribe(8)
	for _, id := range []string{"a", "b", "c"} {
		b.Publish(ch(id))
	}
	for _, s := range []*Subscription{s1, s2} {
		for _, want := range []string{"a", "b", "c"} {
			if got := recv(t, s).JobIDs[0]; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		}
	}
	if b.Subscribers() != 2 {
		t.Errorf("Subscribers = %d, want 2", b.Subscribers())
	}
}

func TestPublishNeverBlocksAndDropsSlowSubscriber(t *testing.T) {
	b := NewBus()
	slow, fast := b.Subscribe(2), b.Subscribe(100)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 50 {
			b.Publish(ch(string(rune('a' + i%26))))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
	// The slow subscriber got its two buffered changes, then the channel closed.
	n := 0
	for range slow.C {
		n++
	}
	if n != 2 {
		t.Errorf("slow subscriber received %d changes, want 2", n)
	}
	if err := slow.Err(); !errors.Is(err, ErrOverflow) {
		t.Errorf("slow.Err() = %v, want ErrOverflow", err)
	}
	if len(fast.C) != 50 {
		t.Errorf("fast subscriber has %d changes, want 50", len(fast.C))
	}
	if fast.Err() != nil {
		t.Errorf("fast.Err() = %v", fast.Err())
	}
	if b.Subscribers() != 1 {
		t.Errorf("Subscribers = %d, want 1", b.Subscribers())
	}
}

func TestCloseSubscriptionAndBus(t *testing.T) {
	b := NewBus()
	s1, s2 := b.Subscribe(1), b.Subscribe(1)
	s1.Close()
	s1.Close() // idempotent
	if _, ok := <-s1.C; ok {
		t.Error("s1.C not closed")
	}
	if s1.Err() != nil {
		t.Errorf("Err after Close = %v", s1.Err())
	}
	b.Publish(ch("x"))
	if got := recv(t, s2).JobIDs[0]; got != "x" {
		t.Errorf("got %q", got)
	}
	b.Close()
	if _, ok := <-s2.C; ok {
		t.Error("s2.C not closed by Bus.Close")
	}
	b.Publish(ch("y")) // no panic
	if _, ok := <-b.Subscribe(1).C; ok {
		t.Error("subscription on a closed bus must be closed")
	}
}

func TestConcurrentUse(*testing.T) {
	b := NewBus()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 200 {
				b.Publish(ch("x"))
			}
		}()
		go func() {
			defer wg.Done()
			for range 50 {
				s := b.Subscribe(4)
				<-time.After(time.Microsecond)
				s.Close()
			}
		}()
	}
	wg.Wait()
	b.Close()
}
