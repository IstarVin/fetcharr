// Package jobs defines the job lifecycle (spec §2.4) as a pure state machine.
// It has no I/O and no clock, so every rule can be tested as a table row. The
// jobs.Service that orchestrates workers and persistence is built on top of
// it in a later phase.
package jobs

import (
	"errors"
	"fmt"
)

// Status is a job status (spec §2.4). The string values are the wire and
// database values.
type Status string

// Job statuses.
const (
	StatusPending     Status = "pending"
	StatusDownloading Status = "downloading"
	StatusProcessing  Status = "processing"
	StatusPaused      Status = "paused"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"

	// StatusRemoved is the pseudo-status returned by Transition for
	// EventDelete: the record disappears. It is never stored or serialized.
	StatusRemoved Status = "removed"
)

// AllStatuses lists every real status, in lifecycle order.
func AllStatuses() []Status {
	return []Status{StatusPending, StatusDownloading, StatusProcessing, StatusPaused, StatusCompleted, StatusFailed, StatusCancelled}
}

// ActiveStatuses are the statuses that count as "active": a job in one of
// them blocks another job with the same URL (409 duplicate, spec §6.1), is
// included in the WebSocket snapshot (§7) and cannot be deleted (§6.3).
// The store builds its partial unique index from this list.
func ActiveStatuses() []Status {
	return []Status{StatusPending, StatusDownloading, StatusProcessing, StatusPaused}
}

// FinishedStatuses are the terminal statuses (finished_at is set; deletable).
func FinishedStatuses() []Status {
	return []Status{StatusCompleted, StatusFailed, StatusCancelled}
}

// IsValid reports whether s is one of the seven real statuses.
func IsValid(s Status) bool { return IsActive(s) || IsFinished(s) }

// IsActive reports whether s is pending, downloading, processing or paused.
func IsActive(s Status) bool {
	switch s {
	case StatusPending, StatusDownloading, StatusProcessing, StatusPaused:
		return true
	}
	return false
}

// IsFinished reports whether s is completed, failed or cancelled.
func IsFinished(s Status) bool {
	switch s {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	}
	return false
}

// Event is an input to the state machine: a user action or a pipeline signal.
type Event string

// Events. The first five are user actions (spec §2.4 table); the rest come
// from the dispatcher/pipeline (§4).
const (
	EventPause  Event = "pause"
	EventResume Event = "resume"
	EventCancel Event = "cancel"
	EventRetry  Event = "retry"
	EventDelete Event = "delete"

	// EventStart: a worker took the job (pending -> downloading).
	EventStart Event = "start"
	// EventDownloadDone: yt-dlp finished (downloading -> processing).
	EventDownloadDone Event = "download_done"
	// EventProcessingDone: file placed (processing -> completed).
	EventProcessingDone Event = "processing_done"
	// EventFail: the pipeline hit an error (pending/downloading/processing -> failed).
	EventFail Event = "fail"
	// EventShutdown: graceful shutdown interrupted the job (-> pending).
	EventShutdown Event = "shutdown"
	// EventRecover: startup recovery of a job left running by a crash (-> pending).
	EventRecover Event = "recover"
)

// AllEvents lists every event.
func AllEvents() []Event {
	return []Event{
		EventPause, EventResume, EventCancel, EventRetry, EventDelete,
		EventStart, EventDownloadDone, EventProcessingDone, EventFail, EventShutdown, EventRecover,
	}
}

// ErrInvalidState is returned (wrapped) when an event is not allowed in the
// current status. The API maps it to 409 invalid_state.
var ErrInvalidState = errors.New("invalid_state")

// transitions is the complete table of allowed (status, event) pairs for
// which `placing` is irrelevant. Cancel of a processing job is handled in
// Transition because it depends on the placing flag.
var transitions = map[Status]map[Event]Status{
	StatusPending: {
		EventPause:  StatusPaused,
		EventCancel: StatusCancelled,
		EventStart:  StatusDownloading,
		EventFail:   StatusFailed, // pre-check failure before the download starts
	},
	StatusDownloading: {
		EventPause:        StatusPaused,
		EventCancel:       StatusCancelled,
		EventDownloadDone: StatusProcessing,
		EventFail:         StatusFailed,
		EventShutdown:     StatusPending,
		EventRecover:      StatusPending,
	},
	StatusProcessing: {
		EventCancel:         StatusCancelled, // only while !placing, see Transition
		EventProcessingDone: StatusCompleted,
		EventFail:           StatusFailed,
		EventShutdown:       StatusPending,
		EventRecover:        StatusPending,
	},
	StatusPaused: {
		EventResume: StatusPending,
		EventCancel: StatusCancelled,
	},
	StatusCompleted: {
		EventDelete: StatusRemoved,
	},
	StatusFailed: {
		EventRetry:  StatusPending,
		EventDelete: StatusRemoved,
	},
	StatusCancelled: {
		EventRetry:  StatusPending,
		EventDelete: StatusRemoved,
	},
}

// Transition returns the status that results from applying ev to a job in
// status from. placing tells whether file placement has started (spec §4.3
// step 3, decision D7); it only matters for EventCancel on a processing job,
// which is refused once placement began. EventDelete yields StatusRemoved.
//
// Every refusal is an error wrapping ErrInvalidState.
func Transition(from Status, ev Event, placing bool) (Status, error) {
	if !IsValid(from) {
		return from, fmt.Errorf("%w: unknown status %q", ErrInvalidState, from)
	}
	known := false
	for _, e := range AllEvents() {
		if e == ev {
			known = true
			break
		}
	}
	if !known {
		return from, fmt.Errorf("%w: unknown event %q", ErrInvalidState, ev)
	}
	to, ok := transitions[from][ev]
	if !ok {
		return from, fmt.Errorf("%w: cannot %s a %s job", ErrInvalidState, ev, from)
	}
	if from == StatusProcessing && ev == EventCancel && placing {
		return from, fmt.Errorf("%w: cannot cancel a job while its files are being placed", ErrInvalidState)
	}
	return to, nil
}
