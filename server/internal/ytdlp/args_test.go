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
