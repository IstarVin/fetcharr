// Package service is the job service: the business rules behind the HTTP API
// (validation, lifecycle actions, batches, deletion) on top of the store and
// the pure state machine in package jobs.
//
// It lives in its own package, not in package jobs, because store imports jobs
// (for jobs.Status); a jobs.Service wrapping the store would be an import cycle.
//
// Two small interfaces let later phases plug in without touching the HTTP
// layer: Publisher (the S2 event bus) and Controller (the S3 dispatcher and
// workers). Both default to no-ops.
package service
