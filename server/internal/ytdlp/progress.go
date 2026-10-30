package ytdlp

import (
	"encoding/json"
	"math"
	"regexp"
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
// a progress update (noise such as "[download] Destination: ...", the final
// file path, or malformed JSON), in which case it must be skipped.
//
// Two shapes are understood: yt-dlp's JSON progress template, and aria2c's
// own summary line. The latter matters because yt-dlp hands direct downloads
// (mp4, mkv, ...) to aria2c, which never invokes the progress template until
// the file is finished; the only live progress is aria2c's stdout.
func ParseProgressLine(line string) (p Progress, ok bool) {
	line = strings.TrimSpace(line)
	if p, ok := parseAria2cLine(line); ok {
		return p, true
	}
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

var (
	// aria2cHead matches "[#2089b0 3.0MiB/28MiB(10%) CN:1 DL:1.5MiB ETA:16s]"
	// up to the end of the size/percent part. The total and percentage are
	// absent (or "0B/0B") while the file size is still unknown.
	aria2cHead  = regexp.MustCompile(`^\[#[0-9a-fA-F]+ ([0-9.]+[KMGT]?i?B)(?:/([0-9.]+[KMGT]?i?B)(?:\((\d+)%\))?)?[ \]]`)
	aria2cSpeed = regexp.MustCompile(`\bDL:([0-9.]+[KMGT]?i?B)`)
	aria2cETA   = regexp.MustCompile(`\bETA:(?:(\d+)d)?(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?[ \]]`)
)

// parseAria2cLine parses an aria2c console summary line. Sizes are printed
// rounded to a binary unit, so byte counts are approximate; the percentage is
// aria2c's own and is preferred over one derived from them.
func parseAria2cLine(line string) (Progress, bool) {
	m := aria2cHead.FindStringSubmatch(line)
	if m == nil {
		return Progress{}, false
	}
	var p Progress
	p.DownloadedBytes = parseAria2cSize(m[1])
	if t := parseAria2cSize(m[2]); t != nil && *t > 0 {
		p.TotalBytes = t
	}
	if m[3] != "" {
		if pct, err := strconv.ParseFloat(m[3], 64); err == nil {
			p.Percent = &pct
		}
	}
	if sm := aria2cSpeed.FindStringSubmatch(line); sm != nil {
		p.SpeedBps = parseAria2cSize(sm[1])
	}
	if em := aria2cETA.FindStringSubmatch(line); em != nil {
		var secs int64
		found := false
		for i, mult := range []int64{86400, 3600, 60, 1} {
			if em[i+1] != "" {
				n, _ := strconv.ParseInt(em[i+1], 10, 64)
				secs += n * mult
				found = true
			}
		}
		if found {
			p.ETASeconds = &secs
		}
	}
	return p, true
}

// parseAria2cSize converts "28MiB", "16KiB" or "0B" to bytes; "" is absent.
func parseAria2cSize(s string) *int64 {
	if s == "" {
		return nil
	}
	mult := float64(1)
	for _, u := range []struct {
		suffix string
		mult   float64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSuffix(s, u.suffix), u.mult
			break
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return intField(f * mult)
}
