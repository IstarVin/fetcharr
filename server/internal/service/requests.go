package service

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// SubtitleInput is one requested sidecar subtitle.
type SubtitleInput struct {
	URL  string `json:"url"`
	Lang string `json:"lang"`
}

// CreateRequest is the body of POST /api/jobs (spec §6.3).
type CreateRequest struct {
	URL       string            `json:"url"`
	Category  string            `json:"category"`
	Title     string            `json:"title"`
	ShowName  string            `json:"show_name"`
	Season    *int              `json:"season"`
	Episode   *int              `json:"episode"`
	Year      *int              `json:"year"`
	Headers   map[string]string `json:"headers"`
	Subtitles []SubtitleInput   `json:"subtitles"`
	// Overwrite replaces an existing library file instead of rejecting the
	// request with already_exists.
	Overwrite bool `json:"overwrite"`
	// Archive makes the job a season pack: the download is a zip, 7z or tar
	// archive whose episode files are placed into the season. It needs a
	// tv category, and episode must be omitted.
	Archive *ArchiveInput `json:"archive"`
}

// ArchiveInput is the episode mapping of a season-pack job.
type ArchiveInput struct {
	// EpisodeOffset is added to the episode number read from each file name.
	EpisodeOffset int `json:"episode_offset"`
	// EpisodeTitles maps final episode numbers ("1", "2", …) to titles.
	EpisodeTitles map[string]string `json:"episode_titles"`
}

// maxArchiveTitles bounds ArchiveInput.EpisodeTitles.
const maxArchiveTitles = 2000

func (a *ArchiveInput) validate() error {
	if a.EpisodeOffset < -999 || a.EpisodeOffset > 9999 {
		return invalid("archive.episode_offset must be between -999 and 9999")
	}
	if len(a.EpisodeTitles) > maxArchiveTitles {
		return invalid("archive.episode_titles has more than %d entries", maxArchiveTitles)
	}
	for k := range a.EpisodeTitles {
		if n, err := strconv.Atoi(k); err != nil || n < 0 || strconv.Itoa(n) != k {
			return invalid("archive.episode_titles keys must be episode numbers, got %q", k)
		}
	}
	return nil
}

// BatchEpisode is one episode of a BatchRequest.
type BatchEpisode struct {
	Episode   *int              `json:"episode"`
	Title     string            `json:"title"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Subtitles []SubtitleInput   `json:"subtitles"`
}

// BatchRequest is the body of POST /api/jobs/batch (spec §6.3).
type BatchRequest struct {
	Category string            `json:"category"`
	ShowName string            `json:"show_name"`
	Season   *int              `json:"season"`
	Year     *int              `json:"year"`
	Headers  map[string]string `json:"headers"`
	Episodes []BatchEpisode    `json:"episodes"`
	// Overwrite replaces existing library files instead of skipping those
	// episodes as already_exists.
	Overwrite bool `json:"overwrite"`
}

// SkippedEpisode reports an episode that was not created.
type SkippedEpisode struct {
	Episode int    `json:"episode"`
	Reason  string `json:"reason"`
}

// Skip reasons of a batch episode.
const (
	// SkipReasonDuplicate: the URL is already active.
	SkipReasonDuplicate = "duplicate"
	// SkipReasonAlreadyExists: the episode is already in the library.
	SkipReasonAlreadyExists = "already_exists"
)

// BatchResult is the outcome of CreateBatch. Jobs and Skipped are in
// ascending episode order.
type BatchResult struct {
	BatchID string
	Jobs    []*store.Job
	Skipped []SkippedEpisode
}

// ReplacementSource is a complete new source for a single retry. Headers stay private.
type ReplacementSource struct {
	URL         string            `json:"url"`
	HeadersMode string            `json:"headers_mode"`
	Headers     map[string]string `json:"headers,omitempty"`
	Subtitles   []SubtitleInput   `json:"subtitles"`
}

func (s *ReplacementSource) validate() error {
	if err := validateHTTPURL("source.url", s.URL); err != nil {
		return err
	}
	if s.Subtitles == nil {
		return invalid("source.subtitles must be an array")
	}
	switch s.HeadersMode {
	case "clear", "keep":
		if s.Headers != nil {
			return invalid("source.headers is only accepted in replace mode")
		}
	case "replace":
		if s.Headers == nil {
			return invalid("source.headers must be an object in replace mode")
		}
		if err := validateHeaders("source.headers", s.Headers); err != nil {
			return err
		}
	default:
		return invalid("source.headers_mode must be clear, keep or replace")
	}
	_, err := normalizeSubtitles("source.subtitles", s.Subtitles)
	return err
}

// RetryOptions are the optional parameters of retry.
type RetryOptions struct {
	Source *ReplacementSource
	// Overwrite replaces an existing library file (spec §6.3 retry).
	Overwrite bool
}

// DeleteOptions are the optional parameters of Delete.
type DeleteOptions struct {
	// DeleteFile asks to also delete the media file and sidecars of a
	// completed job, then the folders that leaves empty up to the category
	// root (spec §6.3). It is ignored for failed and cancelled jobs.
	DeleteFile bool
}

// validateHTTPURL checks that raw is an absolute http or https URL.
func validateHTTPURL(field, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return invalid("%s is required", field)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return invalid("%s is not a valid URL", field)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return invalid("%s must be an http or https URL", field)
	}
	if u.Hostname() == "" {
		return invalid("%s must include a host", field)
	}
	return nil
}

// validateHeaders rejects names and values that could not be passed to
// yt-dlp's `--add-header "Name: value"` safely.
func validateHeaders(field string, h map[string]string) error {
	for name, val := range h {
		if name == "" || strings.ContainsFunc(name, func(r rune) bool {
			return r == ':' || unicode.IsSpace(r) || unicode.IsControl(r)
		}) {
			return invalid("%s: invalid header name %q", field, name)
		}
		if strings.ContainsFunc(val, unicode.IsControl) {
			return invalid("%s: header %q has an invalid value", field, name)
		}
	}
	return nil
}

// normalizeSubtitles validates subtitles and maps a missing lang to "und".
func normalizeSubtitles(field string, in []SubtitleInput) ([]store.Subtitle, error) {
	out := make([]store.Subtitle, 0, len(in))
	for i, s := range in {
		if err := validateHTTPURL(fmt.Sprintf("%s[%d].url", field, i), s.URL); err != nil {
			return nil, err
		}
		out = append(out, store.Subtitle{URL: s.URL, Lang: naming.NormalizeLang(s.Lang)})
	}
	return out, nil
}

// mergeHeaders overlays over onto base. Header names are case-insensitive, so
// an overriding name replaces a base name that differs only in case.
func mergeHeaders(base, over map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		for existing := range out {
			if strings.EqualFold(existing, k) {
				delete(out, existing)
			}
		}
		out[k] = v
	}
	return out
}

// optString returns nil for a blank string.
func optString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}
