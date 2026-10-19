package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const progressTemplate = `download:{"status":"%(progress.status)s","downloaded_bytes":%(progress.downloaded_bytes)s,"total_bytes":%(progress.total_bytes)s,"total_bytes_estimate":%(progress.total_bytes_estimate)j,"speed":%(progress.speed)j,"eta":%(progress.eta)j}`

func fakeBinary(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "testdata", "fake-ytdlp"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	return p
}

func fakeArgs(dir, url string) []string {
	return []string{
		"--newline", "--no-playlist",
		"--progress-template", progressTemplate,
		"--print", "after_move:filepath",
		"-o", filepath.Join(dir, "media.%(ext)s"),
		"--add-header", "Referer: https://example.com",
		"--", url,
	}
}

func TestFakeVersionAndUpdate(t *testing.T) {
	bin := fakeBinary(t)
	for _, flag := range []string{"--version", "-U"} {
		out, err := exec.CommandContext(t.Context(), bin, flag).Output()
		if err != nil || len(bytes.TrimSpace(out)) == 0 {
			t.Fatalf("%s: err=%v out=%q", flag, err, out)
		}
	}
}

func TestFakeDownload(t *testing.T) {
	bin := fakeBinary(t)
	dir := t.TempDir()
	cmd := exec.CommandContext(t.Context(), bin, fakeArgs(dir, "https://example.com/v")...)
	cmd.Env = append(os.Environ(), "FAKE_YTDLP_DURATION=0.4")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	var progress int
	var last string
	var lastBytes float64
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		last = line
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("progress line not JSON: %q: %v", line, err)
		}
		b, _ := m["downloaded_bytes"].(float64)
		if b < lastBytes {
			t.Fatalf("downloaded_bytes went backwards: %v < %v", b, lastBytes)
		}
		lastBytes = b
		progress++
	}
	if progress < 5 {
		t.Fatalf("only %d progress lines", progress)
	}
	want := filepath.Join(dir, "media.mp4")
	if last != want {
		t.Fatalf("last line = %q, want %q", last, want)
	}
	if st, err := os.Stat(want); err != nil || st.Size() == 0 {
		t.Fatalf("output missing: %v", err)
	}
	if _, err := os.Stat(want + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".part should be gone: %v", err)
	}
}

func TestFakeFailure(t *testing.T) {
	bin := fakeBinary(t)
	cmd := exec.CommandContext(t.Context(), bin, fakeArgs(t.TempDir(), "https://example.com/fail")...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(stderr.String(), "ERROR: [generic] simulated failure") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestFakeSigtermLeavesPartAndResumes(t *testing.T) {
	bin := fakeBinary(t)
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, fakeArgs(dir, "https://example.com/v")...)
	cmd.Env = append(os.Environ(), "FAKE_YTDLP_DURATION=6")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "{") {
			break
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	started := time.Now()
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
	if d := time.Since(started); d > 2*time.Second {
		t.Fatalf("SIGTERM took %v", d)
	}
	part := filepath.Join(dir, "media.mp4.part")
	st, err := os.Stat(part)
	if err != nil || st.Size() == 0 {
		t.Fatalf("expected partial file: %v", err)
	}
	partial := st.Size()

	cmd = exec.CommandContext(ctx, bin, fakeArgs(dir, "https://example.com/v")...)
	cmd.Env = append(os.Environ(), "FAKE_YTDLP_DURATION=0.2")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var first map[string]any
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "{") {
			if err := json.Unmarshal([]byte(l), &first); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if b, _ := first["downloaded_bytes"].(float64); int64(b) <= partial {
		t.Fatalf("resume did not start from .part size (%d): first=%v", partial, first)
	}
}
