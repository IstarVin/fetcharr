package ytdlp

import (
	"sort"
	"strings"

	"github.com/istarvin/fetcharr/server/internal/logsafe"
)

const redacted = "[redacted]"

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
	return logsafe.Text(s)
}

// Redact removes every secret substring and every URL query string from s.
// Other subprocess wrappers (ffmpeg) use it for text that may reach a job's
// error message or warnings.
func Redact(s string, secrets []string) string { return redact(s, secrets) }
