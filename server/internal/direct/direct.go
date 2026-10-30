// Package direct downloads one file over HTTP(S) into a job's work
// directory. It is used for links the resolve package turns into plain file
// URLs: yt-dlp's generic extractor rejects archives and extension-less
// octet-stream objects, which is what the HubCloud mirrors serve.
//
// A partial download is kept as download.part and resumed with a Range
// request on the next attempt when the mirror supports it. The finished file
// is named media.<ext>, the extension coming from Content-Disposition, else
// the file's magic bytes, else the URL path.
package direct

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/istarvin/fetcharr/server/internal/ytdlp"
)

// PartName is the partial file inside the work directory.
const PartName = "download.part"

// Defaults of Spec.
const (
	DefaultStallTimeout  = 90 * time.Second
	DefaultProgressEvery = 250 * time.Millisecond
)

// Spec describes one download.
type Spec struct {
	URL     string
	Headers map[string]string
	Dir     string
	// Resumable allows a Range request when a partial file exists.
	Resumable bool
	// RateLimit caps the speed in bytes per second (0 = unlimited).
	RateLimit int64
	// OnProgress receives updates at most every ProgressEvery, plus a final one.
	OnProgress    func(ytdlp.Progress)
	ProgressEvery time.Duration
	// StallTimeout fails the attempt when no byte arrives for that long.
	StallTimeout time.Duration
}

// Error is a download failure worth retrying. Its message never contains
// the URL.
type Error struct{ Message string }

func (e *Error) Error() string { return "download failed: " + e.Message }

func fail(format string, args ...any) error { return &Error{Message: fmt.Sprintf(format, args...)} }

// IsRetryable reports whether err is a download failure worth retrying.
func IsRetryable(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

var errStalled = errors.New("direct: stalled")

// Download fetches s.URL and returns the path of the finished file.
func Download(ctx context.Context, client *http.Client, s Spec) (string, error) {
	if s.StallTimeout <= 0 {
		s.StallTimeout = DefaultStallTimeout
	}
	if s.ProgressEvery <= 0 {
		s.ProgressEvery = DefaultProgressEvery
	}
	part := filepath.Join(s.Dir, PartName)
	var offset int64
	if s.Resumable {
		if st, err := os.Stat(part); err == nil {
			offset = st.Size()
		}
	}

	resp, offset, err := open(ctx, client, s, offset)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); ct == "text/html" {
		return "", fail("the server returned a web page instead of the file")
	}

	var total int64 = -1
	if resp.StatusCode == http.StatusPartialContent {
		if _, _, t, ok := contentRange(resp.Header.Get("Content-Range")); ok {
			total = t
		}
	} else if resp.ContentLength >= 0 {
		total = resp.ContentLength
	}

	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	f, err := os.OpenFile(part, flags, 0o640)
	if err != nil {
		return "", fmt.Errorf("direct: open partial file: %w", err)
	}
	written, copyErr := copyBody(ctx, f, resp.Body, s, offset, total)
	if err := f.Close(); err != nil && copyErr == nil {
		copyErr = fmt.Errorf("direct: close partial file: %w", err)
	}
	if copyErr != nil {
		return "", copyErr
	}
	if total >= 0 && written != total {
		return "", fail("the connection closed early (%d of %d bytes)", written, total)
	}

	ext := dispositionExt(resp.Header.Get("Content-Disposition"))
	if ext == "" {
		ext = Sniff(part)
	}
	if ext == "" {
		ext = cleanExt(path.Ext(resp.Request.URL.Path))
	}
	if ext == "" {
		ext = ".bin"
	}
	final := filepath.Join(s.Dir, "media"+ext)
	if err := os.Rename(part, final); err != nil {
		return "", fmt.Errorf("direct: rename finished file: %w", err)
	}
	return final, nil
}

// open sends the request, resuming at offset when possible. It returns the
// response and the offset the body starts at (0 when the server ignored the
// Range).
func open(ctx context.Context, client *http.Client, s Spec, offset int64) (*http.Response, int64, error) {
	for range 2 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
		if err != nil {
			return nil, 0, fail("invalid file URL")
		}
		for k, v := range s.Headers {
			req.Header.Set(k, v)
		}
		if offset > 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, 0, context.Cause(ctx)
			}
			return nil, 0, fail("%s unreachable", req.URL.Hostname())
		}
		switch {
		case resp.StatusCode == http.StatusOK:
			return resp, 0, nil
		case resp.StatusCode == http.StatusPartialContent:
			if start, _, _, ok := contentRange(resp.Header.Get("Content-Range")); ok && start == offset {
				return resp, offset, nil
			}
			_ = resp.Body.Close()
			offset = 0 // a range we did not ask for: start over
		case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0:
			_ = resp.Body.Close()
			offset = 0 // the partial file does not fit this object
		default:
			_ = resp.Body.Close()
			return nil, 0, fail("%s answered HTTP %d", req.URL.Hostname(), resp.StatusCode)
		}
	}
	return nil, 0, fail("the server does not answer range requests consistently")
}

var contentRangeRe = regexp.MustCompile(`^bytes (\d+)-(\d+)/(\d+|\*)$`)

func contentRange(h string) (start, end, total int64, ok bool) {
	m := contentRangeRe.FindStringSubmatch(strings.TrimSpace(h))
	if m == nil {
		return 0, 0, 0, false
	}
	start, _ = strconv.ParseInt(m[1], 10, 64)
	end, _ = strconv.ParseInt(m[2], 10, 64)
	total = -1
	if m[3] != "*" {
		total, _ = strconv.ParseInt(m[3], 10, 64)
	}
	return start, end, total, true
}

// copyBody copies body to f, reporting progress, applying the rate limit
// and failing when the transfer stalls. It returns the file size reached.
func copyBody(ctx context.Context, f io.Writer, body io.Reader, s Spec, offset, total int64) (int64, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(s.StallTimeout, func() { cancel(errStalled) })
	defer stall.Stop()
	go func() {
		<-ctx.Done()
		if c, ok := body.(io.Closer); ok {
			_ = c.Close() // unblocks a Read waiting on a dead connection
		}
	}()

	buf := make([]byte, 1<<20)
	written := offset
	start := time.Now()
	var sinceStart int64
	lastReport := time.Time{}
	lastBytes, lastTime := written, start
	var speed float64

	report := func(now time.Time, final bool) {
		if s.OnProgress == nil {
			return
		}
		if dt := now.Sub(lastTime).Seconds(); dt > 0 {
			inst := float64(written-lastBytes) / dt
			if speed == 0 {
				speed = inst
			} else {
				speed = 0.7*speed + 0.3*inst
			}
		}
		lastBytes, lastTime = written, now
		p := ytdlp.Progress{DownloadedBytes: ptr(written)}
		if !final {
			p.SpeedBps = ptr(int64(speed))
		}
		if total > 0 {
			p.TotalBytes = ptr(total)
			pct := math.Min(100, float64(written)/float64(total)*100)
			p.Percent = &pct
			if speed > 0 && !final {
				p.ETASeconds = ptr(int64(float64(total-written) / speed))
			}
		}
		s.OnProgress(p)
	}

	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			stall.Reset(s.StallTimeout)
			if _, err := f.Write(buf[:n]); err != nil {
				return written, fmt.Errorf("direct: write: %w", err)
			}
			written += int64(n)
			sinceStart += int64(n)
			now := time.Now()
			if now.Sub(lastReport) >= s.ProgressEvery {
				lastReport = now
				report(now, false)
			}
			if s.RateLimit > 0 {
				due := start.Add(time.Duration(float64(sinceStart) / float64(s.RateLimit) * float64(time.Second)))
				if wait := time.Until(due); wait > 0 {
					stall.Reset(s.StallTimeout + wait)
					select {
					case <-ctx.Done():
					case <-time.After(wait):
					}
				}
			}
		}
		if rerr == io.EOF {
			report(time.Now(), true)
			return written, nil
		}
		if rerr != nil || ctx.Err() != nil {
			switch cause := context.Cause(ctx); {
			case errors.Is(cause, errStalled):
				return written, fail("no data received for %s", s.StallTimeout)
			case cause != nil:
				return written, cause
			}
			return written, fail("the connection broke after %d bytes", written)
		}
	}
}

func ptr[T any](v T) *T { return &v }

var extRe = regexp.MustCompile(`^\.[a-z0-9]{1,5}$`)

func cleanExt(ext string) string {
	ext = strings.ToLower(ext)
	if ext == ".part" || !extRe.MatchString(ext) {
		return ""
	}
	return ext
}

func dispositionExt(h string) string {
	if h == "" {
		return ""
	}
	_, params, err := mime.ParseMediaType(h)
	if err != nil {
		return ""
	}
	return cleanExt(path.Ext(params["filename"]))
}

// Sniff returns the extension matching the magic bytes of the file at p,
// or "" when unknown.
func Sniff(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case len(head) >= 4 && string(head[:4]) == "\x1a\x45\xdf\xa3":
		if strings.Contains(string(head[:min(len(head), 64)]), "webm") {
			return ".webm"
		}
		return ".mkv"
	case len(head) >= 8 && string(head[4:8]) == "ftyp":
		return ".mp4"
	case len(head) >= 4 && (string(head[:4]) == "PK\x03\x04" || string(head[:4]) == "PK\x05\x06"):
		return ".zip"
	case len(head) >= 6 && string(head[:6]) == "7z\xbc\xaf\x27\x1c":
		return ".7z"
	case len(head) >= 6 && string(head[:6]) == "Rar!\x1a\x07":
		return ".rar"
	case len(head) >= 262 && string(head[257:262]) == "ustar":
		return ".tar"
	case len(head) >= 1 && head[0] == 0x47 && len(head) >= 189 && head[188] == 0x47:
		return ".ts"
	}
	return ""
}
