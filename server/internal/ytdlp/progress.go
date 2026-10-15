package ytdlp

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Progress is one parsed progress update. Every numeric field is nil when
// yt-dlp did not report it (null, "None", "NA", missing). The field set
// matches store.Progress, so store.Progress(p) converts directly.
type Progress struct {
	Percent         *float64
	DownloadedBytes *int64
	TotalBytes      *int64 // total_bytes, else total_bytes_estimate
	SpeedBps        *int64
	ETASeconds      *int64
}

// ParseProgressLine parses one stdout line. ok is false when the line is not
// a JSON progress object (noise such as "[download] Destination: ...", the
// final file path, or malformed JSON), in which case it must be skipped.
func ParseProgressLine(line string) (p Progress, ok bool) {
	line = strings.TrimSpace(line)
	if len(line) < 2 || line[0] != '{' {
		return Progress{}, false
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return Progress{}, false
	}
	if _, hasStatus := m["status"]; !hasStatus {
		if _, hasBytes := m["downloaded_bytes"]; !hasBytes {
			return Progress{}, false
		}
	}
	p.DownloadedBytes = intField(m["downloaded_bytes"])
	p.TotalBytes = intField(m["total_bytes"])
	if p.TotalBytes == nil {
		p.TotalBytes = intField(m["total_bytes_estimate"])
	}
	p.SpeedBps = intField(m["speed"])
	p.ETASeconds = intField(m["eta"])
	if p.DownloadedBytes != nil && p.TotalBytes != nil && *p.TotalBytes > 0 {
		pct := float64(*p.DownloadedBytes) / float64(*p.TotalBytes) * 100
		pct = math.Min(100, math.Max(0, pct))
		p.Percent = &pct
	}
	return p, true
}

// numField converts a decoded JSON value to a float, treating null, the
// strings None/NA/N/A/"" and anything non-numeric as absent.
func numField(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return 0, false
		}
		return t, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// intField is numField rounded to int64; negative values are treated as absent.
func intField(v any) *int64 {
	f, ok := numField(v)
	if !ok || f < 0 || f > math.MaxInt64/2 {
		return nil
	}
	n := int64(math.Round(f))
	return &n
}
