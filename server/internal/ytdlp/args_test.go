package ytdlp

import (
	"reflect"
	"slices"
	"testing"
)

func TestBuildArgsFull(t *testing.T) {
	got := BuildArgs(Spec{
		URL:         "https://cdn.example/v.m3u8?sig=1",
		Headers:     map[string]string{"Referer": "https://example.com", "Cookie": "a=b"},
		WorkDir:     "/data/work/job1",
		MergeFormat: "mp4",
		ExtraArgs:   []string{"--limit-rate", "5M"},
	})
	want := []string{
		"--newline", "--progress", "--no-playlist",
		"--downloader", "aria2c", "--downloader", "dash,m3u8:native",
		"-f", "bv*+ba/b",
		"--merge-output-format", "mp4",
		"--progress-template", "download:%(progress)j",
		"--print", "after_move:filepath",
		"--replace-in-metadata", "video:ext", "(?i)^matroska$", "mkv",
		"-o", "/data/work/job1/media.%(ext)s",
		"--add-header", "Cookie: a=b",
		"--add-header", "Referer: https://example.com",
		"--limit-rate", "5M",
		"--", "https://cdn.example/v.m3u8?sig=1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args =\n%q\nwant\n%q", got, want)
	}
}

func TestBuildArgsMinimal(t *testing.T) {
	got := BuildArgs(Spec{URL: "-weird", WorkDir: "/w", MergeFormat: "mkv"})
	if n := len(got); n < 2 || got[n-2] != "--" || got[n-1] != "-weird" {
		t.Fatalf("URL must follow --: %q", got)
	}
	if slices.Contains(got, "--add-header") || slices.Contains(got, "--no-part") {
		t.Fatalf("unexpected flags: %q", got)
	}
	if i := slices.Index(got, "--merge-output-format"); got[i+1] != "mkv" {
		t.Fatalf("merge format: %q", got)
	}
}

func TestBuildArgsEscapesPercentInWorkDir(t *testing.T) {
	got := BuildArgs(Spec{URL: "https://x/v", WorkDir: "/data/100%/work(1)", MergeFormat: "mp4"})
	i := slices.Index(got, "-o")
	if i < 0 || got[i+1] != "/data/100%%/work(1)/media.%(ext)s" {
		t.Fatalf("-o = %q", got[i+1])
	}
}

func TestWithPixeldrainAuth(t *testing.T) {
	const want = "Basic " + "OmtleTEyMw==" // base64(":key123")
	got := WithPixeldrainAuth("https://pixeldrain.com/u/abc", map[string]string{"Referer": "x"}, "key123")
	if got["Authorization"] != want || got["Referer"] != "x" {
		t.Fatalf("got %v", got)
	}
	if h := WithPixeldrainAuth("https://example.com/u/abc", nil, "key123"); h != nil {
		t.Fatalf("non-pixeldrain URL got headers %v", h)
	}
	if h := WithPixeldrainAuth("https://notpixeldrain.com/u/abc", nil, "key123"); h != nil {
		t.Fatalf("lookalike host got headers %v", h)
	}
	if h := WithPixeldrainAuth("https://pixeldrain.com/u/abc", nil, ""); h != nil {
		t.Fatalf("empty key got headers %v", h)
	}
	own := map[string]string{"authorization": "Bearer z"}
	if h := WithPixeldrainAuth("https://pixeldrain.com/u/abc", own, "key123"); len(h) != 1 || h["authorization"] != "Bearer z" {
		t.Fatalf("job Authorization overridden: %v", h)
	}
}

func TestBuildArgsRateLimit(t *testing.T) {
	got := BuildArgs(Spec{URL: "https://u", WorkDir: "/w", MergeFormat: "mp4", RateLimit: 2_500_000, ExtraArgs: []string{"--limit-rate", "1M"}})
	i := slices.Index(got, "--limit-rate")
	if i < 0 || got[i+1] != "2500000" {
		t.Fatalf("rate limit missing: %q", got)
	}
	if j := slices.Index(got[i+1:], "--limit-rate"); j < 0 {
		t.Fatalf("extra_args must come after the setting so they can override it: %q", got)
	}
	if slices.Contains(BuildArgs(Spec{URL: "https://u", WorkDir: "/w", MergeFormat: "mp4"}), "--limit-rate") {
		t.Fatal("no limit flag expected when unlimited")
	}
}
