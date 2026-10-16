package service

import "github.com/istarvin/fetcharr/server/internal/store"

// ChangeKind is the type of a Change. The string values are the WebSocket
// message types of spec §7.
type ChangeKind string

// Change kinds.
const (
	ChangeAdded   ChangeKind = "job_added"
	ChangeUpdated ChangeKind = "job_updated"
	ChangeRemoved ChangeKind = "job_removed"
)

// Change describes one job change for subscribers (the WebSocket hub).
//
// For ChangeAdded and ChangeUpdated, Job is the full stored row. It contains
// request headers, so a subscriber MUST convert it to a wire DTO (api.NewJobDTO)
// before serialising it. For ChangeRemoved, JobIDs lists the removed ids and
// Job is nil.
type Change struct {
	Kind   ChangeKind
	Job    *store.Job
	JobIDs []string
}

// Publisher receives every job change made through the Service, after it was
// committed. Publish must not block and must not call back into the Service.
type Publisher interface {
	Publish(Change)
}

// StopReason says why Controller.Stop was called.
type StopReason string

// Stop reasons.
const (
	// StopPause: the user paused the job. Kill the process group and keep the
	// work directory (spec §4.4).
	StopPause StopReason = "pause"
	// StopCancel: the user cancelled the job. Kill the process group and delete
	// the work directory (spec §4.4).
	StopCancel StopReason = "cancel"
)

// Controller is the hook into the download runner (dispatcher plus workers).
//
//   - Wake is called after a job became pending: created, resumed or retried.
//   - Stop is called after a pause or cancel was committed to the store.
//     For pause it is called only when the job was downloading. For cancel it
//     is called for every previous status (pending, downloading, processing,
//     paused), because the work directory of a paused or processing job must
//     be deleted too; the runner decides whether a process is running.
//
// Implementations must not block and must not call back into the Service.
// Lifecycle actions are serialized through their hooks, so a subsequent
// resume/retry cannot commit pending until Stop returns. Stop must register
// cancellation and protect any asynchronous cleanup before returning.
type Controller interface {
	Wake()
	Stop(jobID string, reason StopReason)
}

type nopPublisher struct{}

func (nopPublisher) Publish(Change) {}

type nopController struct{}

func (nopController) Wake()                   {}
func (nopController) Stop(string, StopReason) {}
