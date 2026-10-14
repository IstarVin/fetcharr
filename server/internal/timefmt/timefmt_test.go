package timefmt

import (
	"encoding/json"
	"regexp"
	"testing"
	"time"
)

func TestFormat(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"whole second", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "2026-01-02T03:04:05.000Z"},
		{"millis", time.Date(2026, 10, 14, 14, 30, 0, 123_000_000, time.UTC), "2026-10-14T14:30:00.123Z"},
		{"truncates not rounds", time.Date(2026, 1, 2, 3, 4, 5, 999_999_999, time.UTC), "2026-01-02T03:04:05.999Z"},
		{"1ms", time.Date(2026, 1, 2, 3, 4, 5, 1_000_000, time.UTC), "2026-01-02T03:04:05.001Z"},
		{"converts to UTC", time.Date(2026, 1, 2, 5, 4, 5, 7_000_000, time.FixedZone("x", 2*3600)), "2026-01-02T03:04:05.007Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Format(tc.in); got != tc.want {
				t.Errorf("Format = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"2026-01-02T03:04:05.000Z", "2026-01-02T03:04:05.000Z", false},
		{"2026-01-02T03:04:05Z", "2026-01-02T03:04:05.000Z", false},
		{"2026-01-02T03:04:05.1234567Z", "2026-01-02T03:04:05.123Z", false},
		{"2026-01-02T05:04:05.5+02:00", "2026-01-02T03:04:05.500Z", false},
		{"", "", true},
		{"yesterday", "", true},
		{"2026-01-02", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := Parse(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && Format(got) != tc.want {
				t.Errorf("got %q, want %q", Format(got), tc.want)
			}
		})
	}
}

func TestNow(t *testing.T) {
	re := regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)
	for i := 0; i < 50; i++ {
		n := Now()
		if n.Nanosecond()%int(time.Millisecond) != 0 {
			t.Fatalf("Now not truncated: %v", n)
		}
		if n.Location() != time.UTC {
			t.Fatalf("Now not UTC")
		}
		if !re.MatchString(Format(n)) {
			t.Fatalf("bad format %q", Format(n))
		}
	}
}

func TestRoundTrip(t *testing.T) {
	n := Now()
	p, err := Parse(Format(n))
	if err != nil || !p.Equal(n) {
		t.Fatalf("round trip: %v %v err=%v", p, n, err)
	}
}

func TestTimeJSON(t *testing.T) {
	type dto struct {
		A Time  `json:"a"`
		B Time  `json:"b"`
		C *Time `json:"c"`
	}
	set := New(time.Date(2026, 1, 2, 3, 4, 5, 6_000_000, time.UTC))
	b, err := json.Marshal(dto{A: set})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":"2026-01-02T03:04:05.006Z","b":null,"c":null}`
	if string(b) != want {
		t.Fatalf("got %s want %s", b, want)
	}
	var back dto
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.A.Equal(set.Time) || back.B.IsSet() || back.C != nil {
		t.Fatalf("round trip mismatch: %+v", back)
	}
	if err := json.Unmarshal([]byte(`{"a":"nope"}`), &back); err == nil {
		t.Fatal("expected error for bad timestamp")
	}
	if err := json.Unmarshal([]byte(`{"a":5}`), &back); err == nil {
		t.Fatal("expected error for non-string")
	}
	p := &Time{}
	if s := p.String(); s != "" {
		t.Fatalf("unset String = %q", s)
	}
	if s := set.String(); s != "2026-01-02T03:04:05.006Z" {
		t.Fatalf("String = %q", s)
	}
}
