package api

import (
	"github.com/istarvin/fetcharr/server/internal/store"
	"github.com/istarvin/fetcharr/server/internal/timefmt"
)

// SubtitleDTO is a subtitle in a job object.
type SubtitleDTO struct {
	URL  string `json:"url"`
	Lang string `json:"lang"`
}

// ProgressDTO is the progress object of a job; any field may be null.
type ProgressDTO struct {
	Percent         *float64 `json:"percent"`
	DownloadedBytes *int64   `json:"downloaded_bytes"`
	TotalBytes      *int64   `json:"total_bytes"`
	SpeedBps        *int64   `json:"speed_bps"`
	ETASeconds      *int64   `json:"eta_seconds"`
}

// JobDTO is the job object of spec §6.2. It deliberately has no field for
// request headers. All timestamps use package timefmt.
type JobDTO struct {
	ID           string        `json:"id"`
	BatchID      *string       `json:"batch_id"`
	Category     string        `json:"category"`
	URL          string        `json:"url"`
	Title        *string       `json:"title"`
	ShowName     *string       `json:"show_name"`
	Season       *int          `json:"season"`
	Episode      *int          `json:"episode"`
	Year         *int          `json:"year"`
	Subtitles    []SubtitleDTO `json:"subtitles"`
	Status       string        `json:"status"`
	Progress     *ProgressDTO  `json:"progress"`
	FilePath     *string       `json:"file_path"`
	ErrorCode    *string       `json:"error_code"`
	ErrorMessage *string       `json:"error_message"`
	Warnings     []string      `json:"warnings"`
	CreatedAt    timefmt.Time  `json:"created_at"`
	QueuedAt     timefmt.Time  `json:"queued_at"`
	StartedAt    timefmt.Time  `json:"started_at"`
	FinishedAt   timefmt.Time  `json:"finished_at"`
}

// NewJobDTO converts a stored job to its wire form. It is exported for the
// WebSocket hub (S2), which must use the same shape.
func NewJobDTO(j *store.Job) JobDTO {
	d := JobDTO{
		ID:           j.ID,
		BatchID:      j.BatchID,
		Category:     j.Category,
		URL:          j.URL,
		Title:        j.Title,
		ShowName:     j.ShowName,
		Season:       j.Season,
		Episode:      j.Episode,
		Year:         j.Year,
		Subtitles:    make([]SubtitleDTO, 0, len(j.Subtitles)),
		Status:       string(j.Status),
		FilePath:     j.FilePath,
		ErrorCode:    j.ErrorCode,
		ErrorMessage: j.ErrorMessage,
		Warnings:     append([]string{}, j.Warnings...),
		CreatedAt:    timefmt.New(j.CreatedAt),
		QueuedAt:     timefmt.New(j.QueuedAt),
	}
	for _, s := range j.Subtitles {
		d.Subtitles = append(d.Subtitles, SubtitleDTO{URL: s.URL, Lang: s.Lang})
	}
	if j.Progress != nil {
		p := ProgressDTO(*j.Progress)
		d.Progress = &p
	}
	if j.StartedAt != nil {
		d.StartedAt = timefmt.New(*j.StartedAt)
	}
	if j.FinishedAt != nil {
		d.FinishedAt = timefmt.New(*j.FinishedAt)
	}
	return d
}

// NewJobDTOs converts a slice; the result is never nil.
func NewJobDTOs(js []*store.Job) []JobDTO {
	out := make([]JobDTO, 0, len(js))
	for _, j := range js {
		out = append(out, NewJobDTO(j))
	}
	return out
}

// CategoryDTO is one entry of the info response.
type CategoryDTO struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Layout    string `json:"layout"`
	Path      string `json:"path"`
	FreeBytes int64  `json:"free_bytes"`
}

// InfoDTO is the body of GET /api/info.
type InfoDTO struct {
	SourceReplacementSupported bool          `json:"source_replacement_supported"`
	SettingsSupported          bool          `json:"settings_supported"`
	SetupRequired              bool          `json:"setup_required"`
	Version                    string        `json:"version"`
	YtdlpVersion               string        `json:"ytdlp_version"`
	YtdlpManaged               bool          `json:"ytdlp_managed"`
	Concurrency                int           `json:"concurrency"`
	AuthRequired               bool          `json:"auth_required"`
	Categories                 []CategoryDTO `json:"categories"`
}

// ListDTO is the body of GET /api/jobs.
type ListDTO struct {
	Jobs  []JobDTO `json:"jobs"`
	Total int      `json:"total"`
}

// BatchCreatedDTO is the body of POST /api/jobs/batch.
type BatchCreatedDTO struct {
	BatchID string           `json:"batch_id"`
	Jobs    []JobDTO         `json:"jobs"`
	Skipped []SkippedEpisode `json:"skipped"`
}

// SkippedEpisode is an episode of a batch request that was not created.
type SkippedEpisode struct {
	Episode int    `json:"episode"`
	Reason  string `json:"reason"`
}
