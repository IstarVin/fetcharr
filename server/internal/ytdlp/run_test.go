package ytdlp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func fakeSpec(dir, url string, headers map[string]string) Spec {
	return Spec{URL: url, Headers: headers, WorkDir: dir, MergeFormat: "mp4"}
}

func TestRunSuccess(t *testing.T) {
	bin := fakeBinary(t)
	dir := t.TempDir()
	var got []Progress
	res, err := Run(t.Context(), bin, BuildArgs(fakeSpec(dir, "https://example.com/v", nil)), Options{
		Env:        []string{"FAKE_YTDLP_DURATION=0.4"},
		OnProgress: func(p Progress) { got = append(got, p) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "media.mp4"); res.FilePath != want {
		t.Fatalf("FilePath = %q, want %q", res.FilePath, want)
	}
	if len(got) < 5 {
		t.Fatalf("only %d progress callbacks", len(got))
	}
	var prev int64
	for _, p := range got {
		if p.DownloadedBytes == nil || *p.DownloadedBytes < prev {
			t.Fatalf("downloaded_bytes not monotonic: %+v", p)
		}
		prev = *p.DownloadedBytes
	}
	last := got[len(got)-1]
	if last.Percent == nil || *last.Percent != 100 || last.TotalBytes == nil || last.SpeedBps != nil {
		t.Fatalf("last progress = %s", show(last))
	}
}

func TestRunFailureTypedError(t *testing.T) {
	bin := fakeBinary(t)
	_, err := Run(t.Context(), bin, BuildArgs(fakeSpec(t.TempDir(), "https://example.com/fail", nil)), Options{})
	var ye *Error
	if !errors.As(err, &ye) {
		t.Fatalf("err = %v, want *Error", err)
	}
	if ye.ExitCode != 1 || ye.Message != "[generic] simulated failure" {
		t.Fatalf("got %+v", ye)
	}
	if errors.Is(err, ErrStopped) {
		t.Fatal("failure must not look like a stop")
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stub.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil { //nolint:gosec // test script must be executable
		t.Fatal(err)
	}
	return p
}

func TestRunRedactsHeaderValuesAndQuery(t *testing.T) {
	stub := writeScript(t, `echo "WARNING: noise" >&2
echo "ERROR: [generic] 403 for https://cdn.example/v.m3u8?token=zzz with Cookie sid=SECRET123 and Bearer tok-abc" >&2
echo "trailing noise" >&2
exit 3
`)
	headers := map[string]string{"Cookie": "sid=SECRET123", "Authorization": "Bearer tok-abc"}
	_, err := Run(t.Context(), stub, nil, Options{Secrets: HeaderValues(headers)})
	var ye *Error
	if !errors.As(err, &ye) || ye.ExitCode != 3 {
		t.Fatalf("err = %v", err)
	}
	for _, leak := range []string{"SECRET123", "tok-abc", "token=zzz", "?"} {
		if strings.Contains(ye.Message, leak) || strings.Contains(err.Error(), leak) {
			t.Fatalf("message leaks %q: %q", leak, ye.Message)
		}
	}
	if !strings.Contains(ye.Message, "https://cdn.example/v.m3u8") || strings.HasPrefix(ye.Message, "ERROR:") {
		t.Fatalf("message = %q", ye.Message)
	}
}

func TestRunKeepsLastErrorLineOnly(t *testing.T) {
	stub := writeScript(t, `echo "ERROR: first" >&2
echo "ERROR: second" >&2
exit 1
`)
	_, err := Run(t.Context(), stub, nil, Options{})
	var ye *Error
	if !errors.As(err, &ye) || ye.Message != "second" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunNoErrorLineFallsBack(t *testing.T) {
	_, err := Run(t.Context(), writeScript(t, "exit 7\n"), nil, Options{})
	var ye *Error
	if !errors.As(err, &ye) || ye.ExitCode != 7 || !strings.Contains(ye.Message, "7") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunStartFailureIsNotTyped(t *testing.T) {
	_, err := Run(t.Context(), "/nonexistent/yt-dlp", []string{"--add-header", "X: SECRET"}, Options{})
	var ye *Error
	if err == nil || errors.As(err, &ye) || errors.Is(err, ErrStopped) || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v", err)
	}
}

func TestRunAlreadyCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Run(ctx, fakeBinary(t), []string{"--version"}, Options{})
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("err = %v", err)
	}
}

var errPause = errors.New("pause")

func TestRunStopThenResume(t *testing.T) {
	bin := fakeBinary(t)
	dir := t.TempDir()
	args := BuildArgs(fakeSpec(dir, "https://example.com/v", nil))

	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	started := make(chan struct{})
	var once sync.Once
	var lastDL int64
	go func() {
		<-started
		cancel(errPause)
	}()
	t0 := time.Now()
	_, err := Run(ctx, bin, args, Options{
		Env:       []string{"FAKE_YTDLP_DURATION=8"},
		KillGrace: 3 * time.Second,
		OnProgress: func(p Progress) {
			if p.DownloadedBytes != nil {
				lastDL = *p.DownloadedBytes
			}
			once.Do(func() { close(started) })
		},
	})
	if !errors.Is(err, ErrStopped) || !errors.Is(err, errPause) {
		t.Fatalf("err = %v, want ErrStopped wrapping the cancel cause", err)
	}
	if d := time.Since(t0); d > 4*time.Second {
		t.Fatalf("stop took %v", d)
	}
	st, statErr := os.Stat(filepath.Join(dir, "media.mp4.part"))
	if statErr != nil || st.Size() == 0 {
		t.Fatalf(".part missing after stop: %v", statErr)
	}
	partial := st.Size()

	var first *Progress
	res, err := Run(t.Context(), bin, args, Options{
		Env: []string{"FAKE_YTDLP_DURATION=0.2"},
		OnProgress: func(p Progress) {
			if first == nil {
				first = &p
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first == nil || first.DownloadedBytes == nil || *first.DownloadedBytes <= partial-1 || *first.DownloadedBytes < lastDL {
		t.Fatalf("resume did not continue from .part (%d): %v", partial, first)
	}
	if res.FilePath != filepath.Join(dir, "media.mp4") {
		t.Fatalf("FilePath = %q", res.FilePath)
	}
}

// processGone reports whether pid no longer exists (zombies count as gone).
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); err != nil {
		return true
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}
	s := string(b)
	return strings.HasPrefix(s[strings.LastIndexByte(s, ')')+2:], "Z")
}

func TestRunStopKillsWholeProcessGroup(t *testing.T) {
	helper, err := filepath.Abs(filepath.Join("..", "..", "testdata", "spawn-child.sh"))
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var childPID int
	go func() {
		for range 200 {
			if b, err := os.ReadFile(pidFile); err == nil {
				if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
					childPID = n
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		cancel()
	}()
	// The helper ignores SIGTERM, so only the SIGKILL escalation can end it.
	_, err = Run(ctx, helper, []string{pidFile}, Options{KillGrace: 300 * time.Millisecond})
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("err = %v", err)
	}
	if childPID == 0 {
		t.Fatal("grandchild pid never appeared")
	}
	deadline := time.Now().Add(2 * time.Second)
	for !processGone(childPID) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d still running after Run returned", childPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunFinalPathIgnoresMissingFile(t *testing.T) {
	stub := writeScript(t, "echo /nonexistent/media.mp4\n")
	res, err := Run(t.Context(), stub, nil, Options{})
	if err != nil || res.FilePath != "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestVersion(t *testing.T) {
	v, err := Version(t.Context(), fakeBinary(t))
	if err != nil || v != "2099.01.01" {
		t.Fatalf("v=%q err=%v", v, err)
	}
	if _, err := Version(t.Context(), "/nonexistent/yt-dlp"); err == nil {
		t.Fatal("want error")
	}
}

func TestFallbackFile(t *testing.T) {
	dir := t.TempDir()
	if FallbackFile(dir) != "" {
		t.Fatal("empty dir")
	}
	write := func(name string, n int) {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("media.mkv.part", 9000)
	write("media.en.vtt", 8000)
	write("small.mp4", 10)
	write("big.mkv", 100)
	if got := FallbackFile(dir); got != filepath.Join(dir, "big.mkv") {
		t.Fatalf("got %q", got)
	}
}

func TestLineWriterBoundsAndFlushes(t *testing.T) {
	var lines []string
	w := &lineWriter{handle: func(s string) { lines = append(lines, s) }}
	_, _ = w.Write([]byte("a\r\nb"))
	_, _ = w.Write([]byte("c\n"))
	_, _ = w.Write([]byte(strings.Repeat("x", maxLineBytes*3) + "\nlast"))
	w.flush()
	if len(lines) != 4 || lines[0] != "a" || lines[1] != "bc" || len(lines[2]) != maxLineBytes || lines[3] != "last" {
		t.Fatalf("lines = %d: %q", len(lines), lines[:2])
	}
}

func TestRedact(t *testing.T) {
	got := redact("a SECRET b SECRETlong https://h/p?x=1&y=2 and http://h2/q#f", []string{"SECRET", "SECRETlong", ""})
	want := "a [redacted] b [redacted] https://h/p and http://h2/q#f"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
