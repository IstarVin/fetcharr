package service

import (
	"fmt"
	"net/url"
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
}

// SkippedEpisode reports an episode that was not created.
type SkippedEpisode struct {
	Episode int    `json:"episode"`
	Reason  string `json:"reason"`
}

// SkipReasonDuplicate is the only skip reason: the URL is already active.
const SkipReasonDuplicate = "duplicate"

// BatchResult is the outcome of CreateBatch. Jobs and Skipped are in
// ascending episode order.
type BatchResult struct {
	BatchID string
	Jobs    []*store.Job
	Skipped []SkippedEpisode
}

// RetryOptions are the optional parameters of retry.
type RetryOptions struct {
	// Overwrite replaces an existing library file (spec §6.3 retry).
	Overwrite bool
}

// DeleteOptions are the optional parameters of Delete.
type DeleteOptions struct {
	// DeleteFile asks to also delete the media file and sidecars of a
	// completed job (spec §6.3). Not implemented before phase S4: it is
	// accepted and ignored, so files are never touched.
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
