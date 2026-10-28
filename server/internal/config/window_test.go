package config

import (
	"testing"
	"time"
)

func at(h, m int) time.Time { return time.Date(2026, 10, 28, h, m, 0, 0, time.UTC) }

func TestWindowOpen(t *testing.T) {
	night := Downloads{WindowStart: "23:00", WindowEnd: "07:00"}
	day := Downloads{WindowStart: "09:30", WindowEnd: "17:00"}
	cases := []struct {
		d    Downloads
		t    time.Time
		want bool
	}{
		{Downloads{}, at(12, 0), true},
		{night, at(23, 0), true},
		{night, at(2, 0), true},
		{night, at(6, 59), true},
		{night, at(7, 0), false},
		{night, at(12, 0), false},
		{day, at(9, 29), false},
		{day, at(9, 30), true},
		{day, at(16, 59), true},
		{day, at(17, 0), false},
	}
	for _, c := range cases {
		if got := c.d.WindowOpen(c.t); got != c.want {
			t.Errorf("%s-%s at %s: open=%v, want %v", c.d.WindowStart, c.d.WindowEnd, c.t.Format("15:04"), got, c.want)
		}
	}
}

func TestNextWindowOpen(t *testing.T) {
	night := Downloads{WindowStart: "23:00", WindowEnd: "07:00"}
	if got := night.NextWindowOpen(at(12, 0)); !got.Equal(at(23, 0)) {
		t.Errorf("same day: got %v", got)
	}
	if got := night.NextWindowOpen(at(2, 0)); !got.Equal(at(2, 0)) {
		t.Errorf("already open: got %v", got)
	}
	early := Downloads{WindowStart: "01:00", WindowEnd: "05:00"}
	want := time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC)
	if got := early.NextWindowOpen(at(6, 0)); !got.Equal(want) {
		t.Errorf("next day: got %v, want %v", got, want)
	}
}

func TestValidateDownloadControls(t *testing.T) {
	bad := []Downloads{
		{WindowStart: "01:00"},
		{WindowStart: "25:00", WindowEnd: "02:00"},
		{WindowStart: "01:00", WindowEnd: "01:00"},
		{AutoRetries: -1},
		{AutoRetries: MaxAutoRetries + 1},
		{RateLimit: -1},
	}
	for _, d := range bad {
		c := Default()
		d.Concurrency, d.MergeFormat = 1, "mp4"
		c.Downloads = d
		if c.Validate() == nil {
			t.Errorf("%+v: expected a validation error", d)
		}
	}
	c := Default()
	c.Downloads.WindowStart, c.Downloads.WindowEnd, c.Downloads.RateLimit = "22:00", "06:00", 5_000_000
	if err := c.Validate(); err != nil {
		t.Fatalf("valid window rejected: %v", err)
	}
}

func TestValidateNotifications(t *testing.T) {
	c := Default()
	c.Notifications = []Notification{{Type: "email", URL: "https://x"}, {Type: NotificationNtfy, URL: "ftp://x"}}
	if c.Validate() == nil {
		t.Fatal("expected errors for an unknown type and a non-http URL")
	}
	c.Notifications = []Notification{{Type: NotificationNtfy, URL: "https://ntfy.sh/topic"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
