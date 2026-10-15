package store

import (
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/istarvin/fetcharr/server/internal/jobs"
)

// NewID returns a new random job/batch id (UUID v4, as in the contract fixtures).
func NewID() string { return uuid.NewString() }

// Subtitle is one sidecar subtitle to fetch. Lang is "" when the client gave
// none; naming maps that to "und" at placement time. The JSON tags match the
// wire format of spec §6.2.
type Subtitle struct {
	URL  string `json:"url"`
	Lang string `json:"lang"`
}

// Progress is the live progress of a job. Any field may be nil when yt-dlp
// does not report it. The JSON tags are the wire names of spec §6.2.
type Progress struct {
	Percent         *float64 `json:"percent"`
	DownloadedBytes *int64   `json:"downloaded_bytes"`
	TotalBytes      *int64   `json:"total_bytes"`
	SpeedBps        *int64   `json:"speed_bps"`
	ETASeconds      *int64   `json:"eta_seconds"`
}

// Job is the stored job row: every field of the API job object (spec §6.2)
// plus server-internal state.
//
// SECURITY: Headers holds request headers (possibly cookies/tokens). They
// must never be returned by the API, sent over WebSocket or logged. The API
// layer must therefore map Job to a separate DTO type that has no headers
// field instead of marshalling Job directly; the `json:"-"` tag on Headers is
// only a safety net.
type Job struct {
	ID      string
	BatchID *string // nil outside batches

	Category string
	URL      string

	// Headers are the request headers for yt-dlp and subtitle downloads.
	// NEVER expose them (see type doc).
	Headers map[string]string `json:"-"`

	// Naming metadata. Nil when not applicable to the category's layout.
	Title    *string
	ShowName *string
	Season   *int
	Episode  *int
	Year     *int

	Subtitles []Subtitle

	Status   jobs.Status
	Progress *Progress // nil until the first update

	FilePath     *string
	ErrorCode    *string // low_disk_space | already_exists | download_failed | processing_failed
	ErrorMessage *string
	Warnings     []string

	// Overwrite replaces an existing library file (retry with overwrite:true).
	Overwrite bool
	// Placing is true once file placement started; cancel is refused (D7).
	Placing bool
	// WorkDir is <data_dir>/work/<id>/, "" until assigned.
	WorkDir string

	CreatedAt  time.Time
	QueuedAt   time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// LogValue implements slog.LogValuer so that logging a *Job can never leak
// headers or full URLs.
func (j *Job) LogValue() slog.Value {
	if j == nil {
		return slog.StringValue("<nil>")
	}
	return slog.GroupValue(
		slog.String("id", j.ID),
		slog.String("category", j.Category),
		slog.String("status", string(j.Status)),
	)
}

// Ptr returns a pointer to v; convenient for the optional fields.
func Ptr[T any](v T) *T { return &v }
