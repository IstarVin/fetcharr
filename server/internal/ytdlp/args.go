package ytdlp

import (
	"encoding/base64"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// OutputName is the basename template yt-dlp writes inside the work dir.
const OutputName = "media.%(ext)s"

// ProgressTemplate makes yt-dlp print one JSON object per progress update.
// "%(progress)j" (rather than per-field templates) is deliberate: a per-field
// template such as %(progress.total_bytes)s renders missing values as the
// bare word NA, which is not valid JSON, whereas %(progress)j renders null.
const ProgressTemplate = "download:%(progress)j"

// formatSelector is "best video plus best audio, falling back to the best
// single format" (spec §4.2).
const formatSelector = "bv*+ba/b"

// Spec describes one yt-dlp download invocation.
type Spec struct {
	URL         string
	Headers     map[string]string
	WorkDir     string   // output goes to <WorkDir>/media.%(ext)s
	MergeFormat string   // container used when separate streams are merged
	RateLimit   int64    // bytes per second; 0 = unlimited
	ExtraArgs   []string // downloads.extra_args, appended before "--"
}

// BuildArgs returns the yt-dlp argument vector for spec (spec §4.2):
//
//	--newline --progress --no-playlist
//	--downloader aria2c --downloader dash,m3u8:native
//	-f bv*+ba/b --merge-output-format <merge_format>
//	--progress-template download:%(progress)j
//	--print after_move:filepath
//	--replace-in-metadata video:ext (?i)^matroska$ mkv
//	-o <work_dir>/media.%(ext)s
//	[--add-header "Name: value" ...]   (sorted by header name)
//	[--limit-rate <bytes>]
//	<extra_args...> -- <url>
//
// "--progress" is required in addition to "--newline": --print implies
// --quiet in yt-dlp, which would otherwise suppress all progress output.
// "--no-part" is intentionally absent so partial files allow resuming.
// Normalize the Matroska container name some sources use as an extension.
// The video stage runs after format selection and before filename creation;
// the default pre_process stage can be overwritten by the selected format.
// This preserves yt-dlp's extension safety checks for all other extensions.
// yt-dlp passes --limit-rate on to aria2c as --max-overall-download-limit, so
// one flag covers both downloaders; extra_args come later and can override it.
func BuildArgs(s Spec) []string {
	args := []string{
		"--newline", "--progress", "--no-playlist",
		"--downloader", "aria2c", "--downloader", "dash,m3u8:native",
		"-f", formatSelector,
		"--merge-output-format", s.MergeFormat,
		"--progress-template", ProgressTemplate,
		"--print", "after_move:filepath",
		"--replace-in-metadata", "video:ext", "(?i)^matroska$", "mkv",
		"-o", filepath.Join(escapeTemplate(s.WorkDir), OutputName),
	}
	names := make([]string, 0, len(s.Headers))
	for k := range s.Headers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		args = append(args, "--add-header", k+": "+s.Headers[k])
	}
	if s.RateLimit > 0 {
		args = append(args, "--limit-rate", strconv.FormatInt(s.RateLimit, 10))
	}
	args = append(args, s.ExtraArgs...)
	return append(args, "--", s.URL)
}

// escapeTemplate escapes '%' so that a work directory containing it is not
// read as an output template field.
func escapeTemplate(p string) string { return strings.ReplaceAll(p, "%", "%%") }

// HeaderValues returns the non-empty header values of h, for Options.Secrets.
func HeaderValues(h map[string]string) []string {
	out := make([]string, 0, len(h))
	for _, v := range h {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// IsPixeldrain reports whether rawURL points at pixeldrain.com or a subdomain.
func IsPixeldrain(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "pixeldrain.com" || strings.HasSuffix(h, ".pixeldrain.com")
}

// WithPixeldrainAuth returns headers plus Pixeldrain's HTTP Basic credentials
// (empty username, API key as password) when rawURL is a Pixeldrain link and
// apiKey is set. An Authorization header already on the job wins. The input
// map is never modified.
func WithPixeldrainAuth(rawURL string, headers map[string]string, apiKey string) map[string]string {
	if apiKey == "" || !IsPixeldrain(rawURL) {
		return headers
	}
	for k := range headers {
		if strings.EqualFold(k, "Authorization") {
			return headers
		}
	}
	out := make(map[string]string, len(headers)+1)
	for k, v := range headers {
		out[k] = v
	}
	out["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+apiKey))
	return out
}
