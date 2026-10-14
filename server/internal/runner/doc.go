// Package runner is the download dispatcher and its workers (spec §4).
//
// A Runner pulls pending jobs in queued_at order (spec §2.5), claims each with
// a compare-and-set pending -> downloading, runs the pre-checks (§4.1) and
// yt-dlp (§4.2) in a worker goroutine and then hands the downloaded file to a
// Processor (§4.3, implemented in phase S4). It implements
// service.Controller, so the service wakes it on create, resume and retry and
// stops jobs on pause and cancel.
//
// # Concurrency model
//
// Every status change the runner makes is a compare-and-set (store.UpdateIf)
// whose target status comes from the pure state machine (jobs.Transition). A
// concurrent pause or cancel through the API therefore wins cleanly: the
// runner's write finds another status, does nothing and publishes nothing.
//
// A worker holds its slot (downloads.concurrency) from the claim until the
// Processor returns. The job id stays "busy" until the worker, including the
// deletion of the work directory after a cancel, is completely done, so a job
// that is paused and quickly resumed is never started twice.
//
// # Hold switch
//
// Hold stops the dispatcher from starting jobs; Idle/WaitIdle signal that no
// job is in the pre-check or download phase any more. Jobs in processing do
// not count. The yt-dlp update drain (decision D1) uses both.
package runner
