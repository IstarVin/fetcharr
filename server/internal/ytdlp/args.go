package ytdlp

import (
	"path/filepath"
	"sort"
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
	ExtraArgs   []string // downloads.extra_args, appended before "--"
}

// BuildArgs returns the yt-dlp argument vector for spec (spec §4.2):
//
//	--newline --progress --no-playlist
//	-f bv*+ba/b --merge-output-format <merge_format>
//	--progress-template download:%(progress)j
//	--print after_move:filepath
//	-o <work_dir>/media.%(ext)s
//	[--add-header "Name: value" ...]   (sorted by header name)
//	<extra_args...> -- <url>
//
// "--progress" is required in addition to "--newline": --print implies
// --quiet in yt-dlp, which would otherwise suppress all progress output.
// "--no-part" is intentionally absent so partial files allow resuming.
func BuildArgs(s Spec) []string {
	args := []string{
		"--newline", "--progress", "--no-playlist",
		"-f", formatSelector,
		"--merge-output-format", s.MergeFormat,
		"--progress-template", ProgressTemplate,
		"--print", "after_move:filepath",
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
