package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// errStopped is returned by runGroup when ctx ended the process.
var errStopped = errors.New("process: stopped")

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

// lastLine returns the last non-empty line.
func (t *tailBuffer) lastLine() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// runGroup runs binary in its own process group and waits for it. When ctx
// ends, the whole group gets SIGTERM, then SIGKILL after grace, and runGroup
// returns an error wrapping errStopped once the process is gone. On a failed
// run the error carries the last line of stderr.
func runGroup(ctx context.Context, binary string, args []string, grace time.Duration) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %w", errStopped, context.Cause(ctx))
	}
	// WithoutCancel: the group kill below is ours; exec must not signal only
	// the leader.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second // a straggler holding the pipes cannot hang Wait
	stderr := &tailBuffer{max: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		// exec errors name only the binary, never the arguments.
		return fmt.Errorf("start %s: %w", binary, err)
	}
	pid := cmd.Process.Pid

	done := make(chan struct{})
	watcherDone := make(chan struct{})
	killed := false
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			killed = true
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			t := time.NewTimer(grace)
			defer t.Stop()
			select {
			case <-done:
			case <-t.C:
			}
			// Also reaps children that outlived the leader.
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		case <-done:
		}
	}()
	waitErr := cmd.Wait()
	close(done)
	<-watcherDone // orders the write of killed

	if killed {
		return fmt.Errorf("%w: %w", errStopped, context.Cause(ctx))
	}
	if waitErr == nil {
		return nil
	}
	if msg := stderr.lastLine(); msg != "" {
		return fmt.Errorf("%s: %s", binary, msg)
	}
	return fmt.Errorf("%s: %w", binary, waitErr)
}
