// Package timefmt is the single place where Fetcharr formats and parses
// timestamps: RFC 3339 in UTC with exactly millisecond precision, for example
// "2026-01-02T03:04:05.000Z" (spec §6.1, decision D5). Every API, WebSocket
// and database timestamp must go through this package so no code path falls
// back to whole seconds.
package timefmt

import (
	"encoding/json"
	"fmt"
	"time"
)

// Layout is the wire layout: always three fraction digits and a literal Z.
const Layout = "2006-01-02T15:04:05.000Z"

// Now returns the current time in UTC truncated to milliseconds.
func Now() time.Time { return Truncate(time.Now()) }

// Truncate converts t to UTC and truncates it to millisecond precision.
func Truncate(t time.Time) time.Time { return t.UTC().Truncate(time.Millisecond) }

// Format renders t as RFC 3339 UTC with millisecond precision. Sub-millisecond
// digits are dropped (not rounded).
func Format(t time.Time) string { return t.UTC().Truncate(time.Millisecond).Format(Layout) }

// Parse parses an RFC 3339 timestamp (any offset, any fraction length) and
// returns it in UTC truncated to milliseconds.
func Parse(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid RFC 3339 timestamp %q: %w", s, err)
	}
	return Truncate(t), nil
}

// Time is a nullable timestamp for DTOs. The zero value (and a nil *Time) is
// "unset" and marshals to JSON null; a set value marshals to Format(t).
// Use it as a plain value field: `FinishedAt timefmt.Time`.
type Time struct{ time.Time }

// New wraps t (truncated to ms, UTC).
func New(t time.Time) Time {
	if t.IsZero() {
		return Time{}
	}
	return Time{Truncate(t)}
}

// IsSet reports whether the value holds a timestamp.
func (t Time) IsSet() bool { return !t.IsZero() }

// MarshalJSON implements json.Marshaler.
func (t Time) MarshalJSON() ([]byte, error) {
	if !t.IsSet() {
		return []byte("null"), nil
	}
	return json.Marshal(Format(t.Time))
}

// UnmarshalJSON implements json.Unmarshaler. null yields the zero value.
func (t *Time) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*t = Time{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	p, err := Parse(s)
	if err != nil {
		return err
	}
	*t = Time{p}
	return nil
}

// String implements fmt.Stringer ("" when unset).
func (t Time) String() string {
	if !t.IsSet() {
		return ""
	}
	return Format(t.Time)
}
