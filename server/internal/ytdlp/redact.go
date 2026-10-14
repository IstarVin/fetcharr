package ytdlp

import (
	"regexp"
	"sort"
	"strings"
)

const redacted = "[redacted]"

// urlQuery matches the query string of an http(s) URL embedded in text.
var urlQuery = regexp.MustCompile(`(https?://[^\s?#"'<>]+)\?[^\s"'<>]*`)

// redact removes every secret substring and every URL query string from s
// (spec §9: header values are never exposed, URLs are logged without query).
func redact(s string, secrets []string) string {
	sorted := make([]string, 0, len(secrets))
	for _, v := range secrets {
		if v != "" {
			sorted = append(sorted, v)
		}
	}
	// Longest first so a value containing another is fully replaced.
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, v := range sorted {
		s = strings.ReplaceAll(s, v, redacted)
	}
	return urlQuery.ReplaceAllString(s, "$1")
}

// Redact removes every secret substring and every URL query string from s.
// Other subprocess wrappers (ffmpeg) use it for text that may reach a job's
// error message or warnings.
func Redact(s string, secrets []string) string { return redact(s, secrets) }
