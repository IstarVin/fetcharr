package jobs

import (
	"errors"
	"testing"
)

// want maps (status, event) to the expected resulting status when allowed.
// Every pair not listed must be refused with ErrInvalidState.
var want = map[Status]map[Event]Status{
	StatusPending: {
		EventPause: StatusPaused, EventCancel: StatusCancelled, EventStart: StatusDownloading, EventFail: StatusFailed,
	},
	StatusDownloading: {
		EventPause: StatusPaused, EventCancel: StatusCancelled, EventDownloadDone: StatusProcessing,
		EventFail: StatusFailed, EventShutdown: StatusPending, EventRecover: StatusPending,
	},
	StatusProcessing: {
		EventCancel: StatusCancelled, EventProcessingDone: StatusCompleted,
		EventFail: StatusFailed, EventShutdown: StatusPending, EventRecover: StatusPending,
	},
	StatusPaused: {
		EventResume: StatusPending, EventCancel: StatusCancelled,
	},
	StatusCompleted: {EventDelete: StatusRemoved},
	StatusFailed:    {EventRetry: StatusPending, EventDelete: StatusRemoved},
	StatusCancelled: {EventRetry: StatusPending, EventDelete: StatusRemoved},
}

// TestTransitionTable checks every (status, event, placing) combination.
func TestTransitionTable(t *testing.T) {
	for _, from := range AllStatuses() {
		for _, ev := range AllEvents() {
			for _, placing := range []bool{false, true} {
				name := string(from) + "/" + string(ev)
				if placing {
					name += "/placing"
				}
				t.Run(name, func(t *testing.T) {
					exp, allowed := want[from][ev]
					// D7: cancel of a processing job is refused once placement started.
					if from == StatusProcessing && ev == EventCancel && placing {
						allowed = false
					}
					got, err := Transition(from, ev, placing)
					if allowed {
						if err != nil || got != exp {
							t.Fatalf("got (%q, %v), want (%q, nil)", got, err, exp)
						}
						return
					}
					if !errors.Is(err, ErrInvalidState) {
						t.Fatalf("err = %v, want ErrInvalidState", err)
					}
					if got != from {
						t.Fatalf("status changed on refusal: %q", got)
					}
				})
			}
		}
	}
}

// TestSpecActionTable restates the §2.4 user-action table literally.
func TestSpecActionTable(t *testing.T) {
	type row struct {
		ev   Event
		from []Status
		to   Status
	}
	rows := []row{
		{EventPause, []Status{StatusPending, StatusDownloading}, StatusPaused},
		{EventResume, []Status{StatusPaused}, StatusPending},
		{EventCancel, []Status{StatusPending, StatusDownloading, StatusProcessing, StatusPaused}, StatusCancelled},
		{EventRetry, []Status{StatusFailed, StatusCancelled}, StatusPending},
		{EventDelete, []Status{StatusCompleted, StatusFailed, StatusCancelled}, StatusRemoved},
	}
	for _, r := range rows {
		allowed := map[Status]bool{}
		for _, f := range r.from {
			allowed[f] = true
			if got, err := Transition(f, r.ev, false); err != nil || got != r.to {
				t.Errorf("%s from %s: (%q, %v), want %q", r.ev, f, got, err, r.to)
			}
		}
		for _, f := range AllStatuses() {
			if !allowed[f] {
				if _, err := Transition(f, r.ev, false); !errors.Is(err, ErrInvalidState) {
					t.Errorf("%s from %s should be invalid_state, got %v", r.ev, f, err)
				}
			}
		}
	}
}

func TestCancelWhilePlacing(t *testing.T) {
	if got, err := Transition(StatusProcessing, EventCancel, false); err != nil || got != StatusCancelled {
		t.Fatalf("cancel before placing: (%q, %v)", got, err)
	}
	if _, err := Transition(StatusProcessing, EventCancel, true); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("cancel while placing: %v", err)
	}
	// The job still runs to completion or failure while placing.
	if got, err := Transition(StatusProcessing, EventProcessingDone, true); err != nil || got != StatusCompleted {
		t.Fatalf("done while placing: (%q, %v)", got, err)
	}
	if got, err := Transition(StatusProcessing, EventFail, true); err != nil || got != StatusFailed {
		t.Fatalf("fail while placing: (%q, %v)", got, err)
	}
	// A stale placing flag must not affect other statuses.
	if got, err := Transition(StatusPending, EventCancel, true); err != nil || got != StatusCancelled {
		t.Fatalf("cancel pending: (%q, %v)", got, err)
	}
}

func TestUnknownInputs(t *testing.T) {
	if _, err := Transition("bogus", EventPause, false); !errors.Is(err, ErrInvalidState) {
		t.Errorf("unknown status: %v", err)
	}
	if _, err := Transition(StatusPending, "bogus", false); !errors.Is(err, ErrInvalidState) {
		t.Errorf("unknown event: %v", err)
	}
	if _, err := Transition(StatusRemoved, EventDelete, false); !errors.Is(err, ErrInvalidState) {
		t.Errorf("removed pseudo-status: %v", err)
	}
}

func TestNormalPath(t *testing.T) {
	s := StatusPending
	for _, ev := range []Event{EventStart, EventDownloadDone, EventProcessingDone} {
		var err error
		if s, err = Transition(s, ev, false); err != nil {
			t.Fatal(err)
		}
	}
	if s != StatusCompleted {
		t.Fatalf("ended in %q", s)
	}
}

func TestActiveSets(t *testing.T) {
	active := map[Status]bool{StatusPending: true, StatusDownloading: true, StatusProcessing: true, StatusPaused: true}
	for _, s := range AllStatuses() {
		if IsActive(s) != active[s] {
			t.Errorf("IsActive(%s) = %v", s, IsActive(s))
		}
		if IsFinished(s) == active[s] {
			t.Errorf("IsFinished(%s) = %v", s, IsFinished(s))
		}
		if !IsValid(s) {
			t.Errorf("IsValid(%s) = false", s)
		}
	}
	if IsValid(StatusRemoved) || IsValid("") || IsActive(StatusRemoved) {
		t.Error("pseudo/empty status must be invalid")
	}
	if len(ActiveStatuses()) != 4 || len(FinishedStatuses()) != 3 {
		t.Error("set sizes")
	}
	for _, s := range ActiveStatuses() {
		if !active[s] {
			t.Errorf("ActiveStatuses contains %s", s)
		}
	}
}
