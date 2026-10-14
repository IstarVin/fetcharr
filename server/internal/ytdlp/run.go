package ytdlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// DefaultKillGrace is how long a stopped process group gets between SIGTERM
// and SIGKILL when Options.KillGrace is zero.
const DefaultKillGrace = 5 * time.Second

const (
	maxLineBytes  = 64 << 10 // longer lines are truncated, bounding memory
	maxErrorChars = 2000     // cap on Error.Message
)

// ErrStopped is returned by Run when the context was cancelled and the
// process group was killed. The error also wraps context.Cause(ctx), so a
// caller can tell pause from cancel or shutdown by using
// context.WithCancelCause and errors.Is on its own sentinel.
var ErrStopped = errors.New("ytdlp: stopped")

// Error is a yt-dlp failure: the process ran and exited non-zero (or was
// killed by a signal it did not get from us).
type Error struct {
	// Message is the last "ERROR:" line of stderr without the prefix, with
	// header values and URL query strings redacted. It is a generic
	// "exited with code N" text when stderr had no such line.
	Message  string
	ExitCode int
}

func (e *Error) Error() string { return "yt-dlp failed: " + e.Message }

// Options configures Run. The zero value is valid.
type Options struct {
	// Dir is the working directory of the process ("" = inherit).
	Dir string
	// Env are extra KEY=VALUE variables appended to the inherited environment.
	Env []string
	// Secrets are substrings (header values) removed from Error.Message.
	Secrets []string
	// KillGrace is the SIGTERM-to-SIGKILL delay (default DefaultKillGrace).
	KillGrace time.Duration
	// OnProgress is called for every parsed progress line, sequentially from
	// a single goroutine, before Run returns. It may be nil.
	OnProgress func(Progress)
}

// Result is the outcome of a successful Run.
type Result struct {
	// FilePath is the file printed by --print after_move:filepath, or ""
	// when nothing usable was printed (callers then use FallbackFile).
	FilePath string
}

// Run starts binary with args in its own process group and blocks until it
// exits. Cancelling ctx sends SIGTERM to the whole group, SIGKILL after
// Options.KillGrace, waits for the process, and returns an error satisfying
// errors.Is(err, ErrStopped). A non-zero exit returns *Error. Run never
// leaves the process, or any child in its group, running when it returns
// after a stop.
func Run(ctx context.Context, binary string, args []string, opt Options) (Result, error) {
	if ctx.Err() != nil {
		return Result{}, stoppedErr(ctx)
	}
	grace := opt.KillGrace
	if grace <= 0 {
		grace = DefaultKillGrace
	}

	// WithoutCancel: the group kill below is ours; exec must not signal only the leader.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), binary, args...)
	cmd.Dir = opt.Dir
	if len(opt.Env) > 0 {
		cmd.Env = append(os.Environ(), opt.Env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second // a straggler holding the pipes cannot hang Wait

	out := &stdoutParser{onProgress: opt.OnProgress}
	errs := &stderrCapture{}
	outW := &lineWriter{handle: out.line}
	errW := &lineWriter{handle: errs.line}
	cmd.Stdout, cmd.Stderr = outW, errW

	if err := cmd.Start(); err != nil {
		// exec errors name only the binary, never the arguments (headers).
		return Result{}, fmt.Errorf("start yt-dlp: %w", err)
	}
	pid := cmd.Process.Pid

	var killed atomic.Bool
	done := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			killed.Store(true)
			killGroup(pid, grace, done)
		case <-done:
		}
	}()

	waitErr := cmd.Wait()
	close(done)
	<-watcherDone
	outW.flush()
	errW.flush()

	if waitErr == nil {
		return Result{FilePath: out.finalPath(opt.Dir)}, nil
	}
	if killed.Load() {
		return Result{}, stoppedErr(ctx)
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		code := ee.ExitCode()
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			code = 128 + int(ws.Signal())
		}
		msg := redact(errs.last, opt.Secrets)
		msg = strings.TrimSpace(strings.TrimPrefix(msg, "ERROR:"))
		if msg == "" {
			msg = fmt.Sprintf("exited with code %d", code)
		}
		if r := []rune(msg); len(r) > maxErrorChars {
			msg = string(r[:maxErrorChars]) + "..."
		}
		return Result{}, &Error{Message: msg, ExitCode: code}
	}
	return Result{}, fmt.Errorf("run yt-dlp: %w", waitErr)
}

func stoppedErr(ctx context.Context) error {
	return fmt.Errorf("%w: %w", ErrStopped, context.Cause(ctx))
}

// killGroup sends SIGTERM to the process group, escalates to SIGKILL once
// grace has elapsed or the leader has exited (done), and always finishes with
// a SIGKILL so children that outlived the leader are not orphaned.
func killGroup(pid int, grace time.Duration, done <-chan struct{}) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

// stdoutParser feeds progress lines to the callback and remembers the last
// path-like non-progress line.
type stdoutParser struct {
	onProgress func(Progress)
	last       string
}

func (s *stdoutParser) line(l string) {
	if p, ok := ParseProgressLine(l); ok {
		if s.onProgress != nil {
			s.onProgress(p)
		}
		return
	}
	l = strings.TrimSpace(l)
	if l == "" || strings.HasPrefix(l, "[") || strings.HasPrefix(l, "{") ||
		strings.HasPrefix(l, "WARNING:") || strings.HasPrefix(l, "ERROR:") {
		return
	}
	s.last = l
}

// finalPath returns the last printed path if it is an existing regular file.
func (s *stdoutParser) finalPath(dir string) string {
	if s.last == "" {
		return ""
	}
	p := s.last
	if !filepath.IsAbs(p) && dir != "" {
		p = filepath.Join(dir, p)
	}
	if st, err := os.Stat(p); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	return p
}

// stderrCapture keeps only the last "ERROR:" line (raw; redacted on use).
type stderrCapture struct{ last string }

func (s *stderrCapture) line(l string) {
	if l = strings.TrimSpace(l); strings.HasPrefix(l, "ERROR:") {
		s.last = l
	}
}

// lineWriter is an io.Writer that calls handle once per "\n"-terminated line.
// Lines longer than maxLineBytes are truncated, so memory stays bounded.
type lineWriter struct {
	handle func(string)
	buf    []byte
	skip   bool // discarding the rest of an over-long line
	mu     sync.Mutex
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		if !w.skip {
			room := maxLineBytes - len(w.buf)
			if len(chunk) > room {
				chunk, w.skip = chunk[:room], true
			}
			w.buf = append(w.buf, chunk...)
		}
		if i < 0 {
			break
		}
		w.emit()
		p = p[i+1:]
	}
	return n, nil
}

func (w *lineWriter) emit() {
	w.handle(strings.TrimSuffix(string(w.buf), "\r"))
	w.buf, w.skip = w.buf[:0], false
}

// flush emits a trailing unterminated line.
func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit()
	}
}

// Version returns `binary --version`, trimmed.
func Version(ctx context.Context, binary string) (string, error) {
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("yt-dlp --version: %w", err)
	}
	v := strings.TrimSpace(string(out))
	if v == "" {
		return "", errors.New("yt-dlp --version: empty output")
	}
	return v, nil
}

// FallbackFile returns the largest media file directly inside dir, ignoring
// partial (.part/.ytdl/.temp) files and subtitle/metadata sidecars. It
// implements the spec §4.2 fallback for when no path was printed. It returns
// "" when there is none.
func FallbackFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestSize int64 = -1
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".part", ".ytdl", ".temp", ".tmp", ".srt", ".vtt", ".ass", ".json", ".jpg", ".jpeg", ".png", ".webp", ".txt", ".description":
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() > bestSize {
			best, bestSize = filepath.Join(dir, e.Name()), info.Size()
		}
	}
	return best
}
