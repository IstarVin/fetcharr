package ytdlp

import (
	"math"
	"strconv"
	"testing"
)

func i64(v int64) *int64 { return &v }

func TestParseProgressLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		ok      bool
		pct     *float64
		dl, tot *int64
		speed   *int64
		eta     *int64
	}{
		{"full", `{"status":"downloading","downloaded_bytes":500,"total_bytes":2000,"speed":123.6,"eta":7}`,
			true, ptrF(25), i64(500), i64(2000), i64(124), i64(7)},
		{"estimate fallback", `{"status":"downloading","downloaded_bytes":50,"total_bytes":null,"total_bytes_estimate":200.4,"speed":null,"eta":null}`,
			true, ptrF(25), i64(50), i64(200), nil, nil},
		{"total wins over estimate", `{"status":"downloading","downloaded_bytes":1,"total_bytes":10,"total_bytes_estimate":999}`,
			true, ptrF(10), i64(1), i64(10), nil, nil},
		{"string None/NA", `{"status":"downloading","downloaded_bytes":"None","total_bytes":"NA","speed":"None","eta":"NA"}`,
			true, nil, nil, nil, nil, nil},
		{"no total", `{"status":"downloading","downloaded_bytes":1024}`, true, nil, i64(1024), nil, nil, nil},
		{"zero total", `{"status":"downloading","downloaded_bytes":0,"total_bytes":0}`, true, nil, i64(0), i64(0), nil, nil},
		{"over 100 clamped", `{"status":"finished","downloaded_bytes":300,"total_bytes":200}`, true, ptrF(100), i64(300), i64(200), nil, nil},
		{"negative eta dropped", `{"status":"downloading","downloaded_bytes":1,"total_bytes":2,"eta":-1}`, true, ptrF(50), i64(1), i64(2), nil, nil},
		{"extra fields ignored", `{"status":"downloading","downloaded_bytes":2,"total_bytes":4,"filename":"x","_percent_str":" 50%"}`, true, ptrF(50), i64(2), i64(4), nil, nil},
		{"whitespace", "  {\"status\":\"downloading\",\"downloaded_bytes\":2,\"total_bytes\":4}\r", true, ptrF(50), i64(2), i64(4), nil, nil},
		{"aria2c full", `[#f19d6d 3.0MiB/28MiB(10%) CN:1 DL:1.5MiB ETA:16s]`,
			true, ptrF(10), i64(3 << 20), i64(28 << 20), i64(3 << 19), i64(16)},
		{"aria2c compound eta", `[#f19d6d 16KiB/28MiB(0%) CN:1 DL:16KiB ETA:1h2m3s]`,
			true, ptrF(0), i64(16 << 10), i64(28 << 20), i64(16 << 10), i64(3723)},
		{"aria2c no eta", `[#f19d6d 27MiB/28MiB(94%) CN:1 DL:2.6MiB]`,
			true, ptrF(94), i64(27 << 20), i64(28 << 20), i64(2726298), nil},
		{"aria2c unknown size", `[#f19d6d 512KiB/0B CN:1 DL:256KiB]`,
			true, nil, i64(512 << 10), nil, i64(256 << 10), nil},
		{"aria2c multiple connections", `[#f19d6d 1.2GiB/4.0GiB(30%) CN:16 SD:2 DL:20MiB ETA:2m20s]`,
			true, ptrF(30), i64(1288490189), i64(4 << 30), i64(20 << 20), i64(140)},
		{"junk text", `[download] Destination: /x/media.mp4`, false, nil, nil, nil, nil, nil},
		{"path", `/data/work/j/media.mp4`, false, nil, nil, nil, nil, nil},
		{"empty", ``, false, nil, nil, nil, nil, nil},
		{"bad json NA", `{"status":"downloading","downloaded_bytes":1,"total_bytes":NA}`, false, nil, nil, nil, nil, nil},
		{"json without progress keys", `{"id":"abc","title":"t"}`, false, nil, nil, nil, nil, nil},
		{"json array", `[1,2]`, false, nil, nil, nil, nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := ParseProgressLine(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if !eqF(p.Percent, tc.pct) || !eqI(p.DownloadedBytes, tc.dl) || !eqI(p.TotalBytes, tc.tot) ||
				!eqI(p.SpeedBps, tc.speed) || !eqI(p.ETASeconds, tc.eta) {
				t.Fatalf("got %s, want pct=%s dl=%s tot=%s speed=%s eta=%s", show(p), fs(tc.pct), is(tc.dl), is(tc.tot), is(tc.speed), is(tc.eta))
			}
		})
	}
}

func ptrF(v float64) *float64 { return &v }

func eqF(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return math.Abs(*a-*b) < 1e-9
}

func eqI(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func fs(p *float64) string {
	if p == nil {
		return "nil"
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

func is(p *int64) string {
	if p == nil {
		return "nil"
	}
	return strconv.FormatInt(*p, 10)
}

func show(p Progress) string {
	return fs(p.Percent) + "," + is(p.DownloadedBytes) + "," + is(p.TotalBytes) + "," + is(p.SpeedBps) + "," + is(p.ETASeconds)
}
