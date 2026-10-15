package runner

import (
	"testing"

	"github.com/istarvin/fetcharr/server/internal/config"
)

func TestLiveConcurrencyAffectsReservationsWithoutStoppingWorkers(t *testing.T) {
	c := config.Default()
	runtime := c.EnableRuntime()
	r := New(Options{Config: c})
	_, first, result := r.reserve("first")
	if result != reserved {
		t.Fatal("first reservation failed")
	}
	_, second, result := r.reserve("second")
	if result != reserved {
		t.Fatal("second reservation failed")
	}
	next := c.Current().Clone()
	next.Downloads.Concurrency = 1
	runtime.Publish(next)
	if _, _, result := r.reserve("third"); result != noSlot {
		t.Fatal("lower limit did not stop new reservations")
	}
	if r.workers != 2 {
		t.Fatal("decreasing concurrency stopped existing work")
	}
	r.release("first", first)
	if _, _, result := r.reserve("third"); result != noSlot {
		t.Fatal("started a replacement while at the new limit")
	}
	next.Downloads.Concurrency = 3
	runtime.Publish(next)
	_, third, result := r.reserve("third")
	if result != reserved {
		t.Fatal("increased limit not applied")
	}
	r.release("second", second)
	r.release("third", third)
}
