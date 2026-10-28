package config

import (
	"errors"
	"fmt"
	"time"
)

// MaxAutoRetries bounds downloads.auto_retries.
const MaxAutoRetries = 5

// parseClock parses "HH:MM" (24-hour) into minutes after midnight.
func parseClock(s string) (int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a time in HH:MM format", s)
	}
	return t.Hour()*60 + t.Minute(), nil
}

func (d Downloads) validateWindow() error {
	if d.WindowStart == "" && d.WindowEnd == "" {
		return nil
	}
	if d.WindowStart == "" || d.WindowEnd == "" {
		return errors.New("window_start and window_end must both be set or both be empty")
	}
	start, err := parseClock(d.WindowStart)
	if err != nil {
		return fmt.Errorf("window_start: %w", err)
	}
	end, err := parseClock(d.WindowEnd)
	if err != nil {
		return fmt.Errorf("window_end: %w", err)
	}
	if start == end {
		return errors.New("window_start and window_end must differ; leave both empty to allow any time")
	}
	return nil
}

// HasWindow reports whether new downloads are restricted to a daily window.
func (d Downloads) HasWindow() bool { return d.WindowStart != "" && d.WindowEnd != "" }

// window returns the window bounds in minutes after midnight; ok is false
// when there is no (valid) window.
func (d Downloads) window() (start, end int, ok bool) {
	if !d.HasWindow() {
		return 0, 0, false
	}
	start, err1 := parseClock(d.WindowStart)
	end, err2 := parseClock(d.WindowEnd)
	if err1 != nil || err2 != nil || start == end {
		return 0, 0, false
	}
	return start, end, true
}

// WindowOpen reports whether a new download may start at t, read in t's
// location. A window whose end is before its start spans midnight. Without
// a window it is always open.
func (d Downloads) WindowOpen(t time.Time) bool {
	start, end, ok := d.window()
	if !ok {
		return true
	}
	m := t.Hour()*60 + t.Minute()
	if start < end {
		return m >= start && m < end
	}
	return m >= start || m < end
}

// NextWindowOpen returns the next time at or after t when the window opens,
// in t's location. It returns t itself when the window is already open.
func (d Downloads) NextWindowOpen(t time.Time) time.Time {
	start, _, ok := d.window()
	if !ok || d.WindowOpen(t) {
		return t
	}
	y, mo, day := t.Date()
	next := time.Date(y, mo, day, start/60, start%60, 0, 0, t.Location())
	if !next.After(t) {
		next = time.Date(y, mo, day+1, start/60, start%60, 0, 0, t.Location())
	}
	return next
}
