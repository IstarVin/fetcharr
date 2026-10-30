package archive

import (
	"archive/tar"
	"archive/zip"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type entry struct {
	name string
	body string
}

var pack = []entry{
	{"Show.S01.1080p/Show.S01E02.Second.1080p.mkv", strings.Repeat("b", 2000)},
	{"Show.S01.1080p/Show.S01E01.Pilot.1080p.mkv", strings.Repeat("a", 2000)},
	{"Show.S01.1080p/Show.S01E10.Tenth.1080p.mkv", strings.Repeat("c", 2000)},
	{"Show.S01.1080p/sample.mkv", "s"},
	{"Show.S01.1080p/info.nfo", "nfo"},
}

func writeZip(t *testing.T, entries []entry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pack.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for _, e := range entries {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return p
}

func writeTar(t *testing.T, entries []entry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pack.tar")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	w := tar.NewWriter(f)
	for _, e := range entries {
		if err := w.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.WriteHeader(&tar.Header{Name: "link.mkv", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return p
}

func isVideo(ext string) bool { return strings.EqualFold(ext, ".mkv") }

func checkPack(t *testing.T, dst string) {
	t.Helper()
	files, err := VideoFiles(dst, isVideo)
	if err != nil {
		t.Fatal(err)
	}
	eps, err := MapEpisodes(files, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range eps {
		got = append(got, filepath.Base(e.Path)+"="+strconv.Itoa(e.Episode))
	}
	want := []string{"Show.S01E01.Pilot.1080p.mkv=1", "Show.S01E02.Second.1080p.mkv=2", "Show.S01E10.Tenth.1080p.mkv=10"}
	if !slices.Equal(got, want) {
		t.Fatalf("episodes = %v", got)
	}
	if _, err := os.Lstat(filepath.Join(dst, "link.mkv")); err == nil {
		t.Error("a symlink was extracted")
	}
}

func TestExtractZip(t *testing.T) {
	src := writeZip(t, pack)
	if f, _ := Detect(src); f != Zip {
		t.Fatalf("Detect = %q", f)
	}
	dst := t.TempDir()
	var last int64
	if err := Extract(context.Background(), src, dst, func(done, _ int64) { last = done }); err != nil {
		t.Fatal(err)
	}
	if last != 6000+1+3 {
		t.Errorf("progress = %d", last)
	}
	checkPack(t, dst)
	if n, _ := UncompressedSize(src); n != 6004 {
		t.Errorf("UncompressedSize = %d", n)
	}
}

func TestExtractTar(t *testing.T) {
	src := writeTar(t, pack)
	if f, _ := Detect(src); f != Tar {
		t.Fatalf("Detect = %q", f)
	}
	dst := t.TempDir()
	if err := Extract(context.Background(), src, dst, nil); err != nil {
		t.Fatal(err)
	}
	checkPack(t, dst)
}

func TestExtract7z(t *testing.T) {
	if SevenZipBinary() == "" {
		t.Skip("7-Zip not installed")
	}
	srcDir := t.TempDir()
	for _, e := range pack {
		p := filepath.Join(srcDir, filepath.FromSlash(e.name))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(e.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src := filepath.Join(t.TempDir(), "pack.7z")
	cmd := exec.CommandContext(context.Background(), SevenZipBinary(), "a", "-mx=0", src, ".")
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("7z a: %v\n%s", err, out)
	}
	if f, _ := Detect(src); f != SevenZip {
		t.Fatalf("Detect = %q", f)
	}
	dst := t.TempDir()
	if err := Extract(context.Background(), src, dst, nil); err != nil {
		t.Fatal(err)
	}
	checkPack(t, dst)
	if !slices.Contains(Formats(), "7z") {
		t.Error("Formats lacks 7z")
	}
}

func TestExtractRejectsZipSlip(t *testing.T) {
	src := writeZip(t, []entry{{"../../evil.mkv", "x"}})
	err := Extract(context.Background(), src, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtractCancel(t *testing.T) {
	src := writeZip(t, pack)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Extract(ctx, src, t.TempDir(), nil); err == nil {
		t.Fatal("extracted with a cancelled context")
	}
}

func TestDetectUnsupported(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.mkv")
	_ = os.WriteFile(p, []byte("\x1a\x45\xdf\xa3"), 0o644)
	if _, err := Detect(p); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestEpisodeNumber(t *testing.T) {
	for name, want := range map[string]int{
		"Dead-End.Job.S01E03.Title.1080p.NF.WEB-DL.H.264-4kHdHub.Com.mkv":                  3,
		"Mobland S01 E07 1080p BluRay x264.mkv":                                            7,
		"Show 1x05 Title.mkv":                                                              5,
		"Attack on Titan - Episode 12 (1080p).mkv":                                         12,
		"[Group] Frieren - 07 (1080p) [ABCD1234].mkv":                                      7,
		"Lupin.S04E08.2160p.NF.WEB-DL.MULTi.DDP5.1.Atmos.DV.HDR.H.265-4kHdHub.Com.mkv":     8,
		"Marvels The Punisher S02E13 2160p NF WEB-DL DoVi HDR 10bit HEVC x265 (SiC).mkv":   13,
		"How.to.Ruin.Diwali.S01E02.Sparks.Fly.1080p.NF.WEB-DL.Hindi.DDP5.1.H.264-4kHd.mkv": 2,
	} {
		if got, ok := EpisodeNumber(name); !ok || got != want {
			t.Errorf("EpisodeNumber(%q) = %d, %v; want %d", name, got, ok, want)
		}
	}
	if _, ok := EpisodeNumber("Show.Finale.1080p.WEB-DL.H.264.mkv"); ok {
		t.Error("found a number in a name without one")
	}
}

func TestMapEpisodesFallbackAndOffset(t *testing.T) {
	// No numbers: natural order, then the offset.
	eps, err := MapEpisodes([]string{"/x/Part 10.mkv", "/x/Part 2.mkv", "/x/Part 1.mkv"}, 59)
	if err != nil {
		t.Fatal(err)
	}
	if eps[0].Episode != 60 || filepath.Base(eps[0].Path) != "Part 1.mkv" || eps[2].Episode != 62 || filepath.Base(eps[2].Path) != "Part 10.mkv" {
		t.Fatalf("eps = %+v", eps)
	}
	// Repeated numbers (two releases of E01) also fall back.
	eps, _ = MapEpisodes([]string{"/x/S01E01.a.mkv", "/x/S01E01.b.mkv"}, 0)
	if eps[0].Episode != 1 || eps[1].Episode != 2 {
		t.Fatalf("eps = %+v", eps)
	}
	if _, err := MapEpisodes([]string{"/x/S01E01.mkv"}, -5); err == nil {
		t.Error("accepted a negative episode")
	}
	if _, err := MapEpisodes(nil, 0); !errors.Is(err, ErrNoEpisodes) {
		t.Errorf("err = %v", err)
	}
}
