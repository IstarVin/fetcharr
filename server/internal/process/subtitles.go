package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/store"
	"github.com/istarvin/fetcharr/server/internal/ytdlp"
)

// sidecarMode is the mode of subtitle files (the library must be readable by
// the media server).
const sidecarMode = 0o644

// subtitle is a downloaded (and, if needed, converted) SRT file in the work
// directory.
type subtitle struct {
	path string
	lang string // normalized
}

// srtTiming matches an SRT cue timing line.
var srtTiming = regexp.MustCompile(`(?m)^\s*\d{1,3}:\d{2}:\d{2}[,.]\d{3}\s*-->\s*\d{1,3}:\d{2}:\d{2}[,.]\d{3}`)

type subFormat int

const (
	formatOther subFormat = iota
	formatSRT
	formatVTT
	formatASS
)

// sniff detects the subtitle format from its content.
func sniff(data []byte) subFormat {
	head := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(head) > 8192 {
		head = head[:8192]
	}
	trimmed := bytes.TrimLeft(head, " \t\r\n")
	switch {
	case bytes.HasPrefix(trimmed, []byte("WEBVTT")):
		return formatVTT
	case bytes.Contains(bytes.ToLower(head), []byte("[script info]")):
		return formatASS
	case srtTiming.Match(head) && !bytes.Contains(head, []byte("<tt")):
		return formatSRT
	}
	return formatOther
}

// inputFormat returns the extension of the file handed to ffmpeg and the
// demuxer to force with -f. Forcing the demuxer (never letting ffmpeg probe)
// keeps hostile content from selecting a playlist or concat demuxer that
// reads other files or URLs. ok is false for a format that is not on the
// allow list.
func inputFormat(f subFormat, rawURL string) (ext, demuxer string, ok bool) {
	switch f {
	case formatVTT:
		return ".vtt", "webvtt", true
	case formatASS:
		return ".ass", "ass", true
	case formatSRT:
		return ".srt", "srt", true
	}
	if u, err := url.Parse(rawURL); err == nil {
		switch e := strings.ToLower(path.Ext(u.Path)); e {
		case ".vtt":
			return e, "webvtt", true
		case ".ass", ".ssa":
			return e, "ass", true
		case ".srt":
			return e, "srt", true
		case ".smi", ".sami":
			return e, "sami", true
		case ".lrc":
			return e, "lrc", true
		case ".sub":
			return e, "microdvd", true
		}
	}
	return "", "", false
}

// safeURL is raw without userinfo, query string and fragment (spec §9), for
// warnings and logs.
func safeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "<invalid url>"
	}
	u.User, u.RawQuery, u.Fragment, u.ForceQuery = nil, "", "", false
	return u.String()
}

// subtitles downloads and converts the job's subtitles into workDir. A
// subtitle that fails produces a warning and is skipped (spec §4.3 step 1);
// only ctx ending returns an error.
func (p *Processor) subtitles(ctx context.Context, job *store.Job, workDir string) ([]subtitle, []string, error) {
	var (
		out      []subtitle
		warnings []string
		secrets  = ytdlp.HeaderValues(job.Headers)
	)
	for i, s := range job.Subtitles {
		lang := naming.NormalizeLang(s.Lang)
		sub, reason, err := p.oneSubtitle(ctx, job, workDir, i, s, lang)
		if err != nil {
			return nil, nil, err
		}
		if reason == "" {
			out = append(out, sub)
		} else {
			// Header values and URL query strings never reach a warning.
			warnings = append(warnings, ytdlp.Redact(
				fmt.Sprintf("subtitle %s (%s): %s", lang, safeURL(s.URL), reason), secrets))
		}
	}
	return out, warnings, nil
}

// oneSubtitle processes one subtitle. On failure reason says why (a warning
// text); err is non-nil only when ctx ended.
func (p *Processor) oneSubtitle(ctx context.Context, job *store.Job, workDir string, i int,
	s store.Subtitle, lang string,
) (sub subtitle, reason string, err error) {
	data, reason, err := p.fetchSubtitle(ctx, job, s.URL)
	if err != nil || reason != "" {
		return sub, reason, err
	}
	final := filepath.Join(workDir, fmt.Sprintf("subtitle-%d.srt", i))
	format := sniff(data)
	if format == formatSRT {
		if err := os.WriteFile(final, data, sidecarMode); err != nil {
			return sub, "download failed: " + redactPaths(err), nil
		}
		return subtitle{path: final, lang: lang}, "", nil
	}

	inExt, demuxer, ok := inputFormat(format, s.URL)
	if !ok {
		return sub, "conversion failed: unsupported subtitle format", nil
	}
	in := filepath.Join(workDir, fmt.Sprintf("subtitle-%d.src%s", i, inExt))
	if err := os.WriteFile(in, data, 0o600); err != nil {
		return sub, "download failed: " + redactPaths(err), nil
	}
	cctx, cancel := context.WithTimeout(ctx, p.convertTimeout)
	defer cancel()
	cerr := runGroup(cctx, p.ffmpeg, []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-protocol_whitelist", "file", "-f", demuxer, "-i", in, "-f", "srt", final,
	}, p.killGrace)
	_ = os.Remove(in)
	if cerr != nil {
		if ctx.Err() != nil {
			return sub, "", context.Cause(ctx) // cancelled or shutting down: not a warning
		}
		if errors.Is(cerr, errStopped) {
			return sub, "conversion failed: ffmpeg timed out", nil
		}
		return sub, "conversion failed: " + cerr.Error(), nil
	}
	if st, err := os.Stat(final); err != nil || st.Size() == 0 {
		return sub, "conversion failed: ffmpeg produced no subtitles", nil
	}
	_ = os.Chmod(final, sidecarMode) // ffmpeg creates it with the umask's mode
	return subtitle{path: final, lang: lang}, "", nil
}

// redactPaths is the message of err without the file path that os errors
// carry (work directory paths are no secret, but clutter a warning).
func redactPaths(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// fetchSubtitle downloads rawURL with the job's headers. reason is the
// warning text on failure; err is non-nil only when ctx ended.
func (p *Processor) fetchSubtitle(ctx context.Context, job *store.Job, rawURL string) (data []byte, reason string, err error) {
	rctx, cancel := context.WithTimeout(ctx, p.subtitleTimeout)
	defer cancel()
	req, rerr := http.NewRequestWithContext(rctx, http.MethodGet, rawURL, nil)
	if rerr != nil {
		return nil, "download failed: invalid URL", nil
	}
	for name, val := range job.Headers {
		switch {
		case strings.EqualFold(name, "host"):
			req.Host = val
		case strings.EqualFold(name, "accept-encoding"):
			// Setting it by hand turns off Go's transparent gzip decoding.
		default:
			req.Header.Set(name, val)
		}
	}
	client := *p.client // shallow copy: CheckRedirect is per job
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		if next.URL.Host != via[0].URL.Host {
			// The headers (cookies, tokens) belong to the original host.
			for name := range job.Headers {
				next.Header.Del(name)
			}
			next.Host = ""
		}
		return nil
	}
	resp, derr := client.Do(req)
	if derr != nil {
		if ctx.Err() != nil {
			return nil, "", context.Cause(ctx)
		}
		return nil, "download failed: " + netErrText(rctx, derr), nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Sprintf("download failed: HTTP %d", resp.StatusCode), nil
	}
	data, rerr = io.ReadAll(io.LimitReader(resp.Body, p.maxSubtitleBytes+1))
	switch {
	case ctx.Err() != nil:
		return nil, "", context.Cause(ctx)
	case rerr != nil:
		return nil, "download failed: " + netErrText(rctx, rerr), nil
	case int64(len(data)) > p.maxSubtitleBytes:
		return nil, fmt.Sprintf("download failed: larger than %d bytes", p.maxSubtitleBytes), nil
	case len(bytes.TrimSpace(data)) == 0:
		return nil, "download failed: empty file", nil
	}
	return data, "", nil
}

// netErrText is a short description of a transport error without the request
// URL that *url.Error embeds.
func netErrText(rctx context.Context, err error) string {
	if errors.Is(rctx.Err(), context.DeadlineExceeded) {
		return "timed out"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}
