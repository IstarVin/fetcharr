package runner

import (
	"context"

	"github.com/istarvin/fetcharr/server/internal/store"
)

// Error codes of failed jobs (spec §6.2).
const (
	CodeLowDiskSpace     = "low_disk_space"
	CodeAlreadyExists    = "already_exists"
	CodeDownloadFailed   = "download_failed"
	CodeProcessingFailed = "processing_failed"
)

// Processor implements the processing phase (spec §4.3): subtitles, the
// second existence check, placing the files, cleaning up the work directory
// and completing the job.
//
// Process is called once per job, in the worker goroutine that downloaded it
// (so the worker slot stays occupied), after the job moved to processing.
// job is the stored row; mediaFile is the downloaded file inside the work
// directory.
//
// The implementation owns the processing -> completed transition (and
// store.SetPlacing, which closes the cancel window): it must use
// compare-and-set updates, publish its own changes and stop work when ctx is
// cancelled. ctx is cancelled when the user cancels the job (before
// placement) and on graceful shutdown; return promptly with any error then.
//
// Return values:
//   - nil: the Processor is done. The runner changes nothing, so a Processor
//     that leaves the job in processing (the S3 NopProcessor) keeps it there.
//   - a *Failure (or an error wrapping one): the runner fails the job, if it
//     is still processing, with that code and message.
//   - any other error while ctx is not cancelled: the job fails with
//     processing_failed and err.Error() as its message.
//   - any error while ctx is cancelled: the runner treats it as the stop. A
//     cancel needs nothing more (the service already set the status; the
//     work directory is deleted). A shutdown sets the job back to pending.
//
// A panic is recovered by the runner and fails the job with processing_failed.
type Processor interface {
	Process(ctx context.Context, job *store.Job, mediaFile string) error
}

// NopProcessor is the S3 default: it does nothing and leaves the job in
// processing.
type NopProcessor struct{}

// Process implements Processor.
func (NopProcessor) Process(context.Context, *store.Job, string) error { return nil }

// Failure is a Processor error that carries the job's error_code.
type Failure struct {
	Code    string // one of the Code* constants
	Message string
}

func (f *Failure) Error() string { return f.Message }
