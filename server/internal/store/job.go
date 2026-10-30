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

// Archive is the episode mapping of a season-pack job.
type Archive struct {
	// EpisodeOffset is added to the episode number read from each file name
	// (or to its position when the names carry none).
	EpisodeOffset int `json:"episode_offset"`
	// EpisodeTitles maps final episode numbers ("1", "2", …) to titles.
	EpisodeTitles map[string]string `json:"episode_titles,omitempty"`
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

	FilePath *string
	// Archive marks a season-pack job: one download holding several episode
	// files, placed one by one after extraction. Nil for ordinary jobs.
	Archive *Archive
	// Files are the library files an archive job placed (FilePath is then
	// the season folder). Empty for ordinary jobs.
	Files []string

	ErrorCode    *string // low_disk_space | already_exists | download_failed | processing_failed
	ErrorMessage *string
	Warnings     []string

	// Overwrite replaces an existing library file (retry with overwrite:true).
	ResetWorkDir bool // durable: remove old attempt files before using a replacement source
	Overwrite    bool
	// Placing is true once file placement started; cancel is refused (D7).
	Placing bool
	// WorkDir is <data_dir>/work/<id>/, "" until assigned.
	WorkDir         string
	DestinationRoot string // internal: immutable origin of library files

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
