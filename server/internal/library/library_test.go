package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/naming"
)

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func TestIsSidecar(t *testing.T) {
	for name, want := range map[string]bool{
		"Foo.en.srt":      true,
		"Foo.en.2.srt":    true,
		"Foo.pt-BR.srt":   true,
		"Foo.srt":         false,
		"Foo.en.srt.bak":  false,
		"Foo.en.x.srt":    false,
		"Foo.en.2.3.srt":  false,
		"Foo - x.en.srt":  false,
		"Foo.mp4":         false,
		"Foo..srt":        false,
		"Foo.en..srt":     false,
		"Foo2.en.srt":     false,
		"Other.en.srt":    false,
		"Foo.en.2.srt.en": false,
	} {
		if got := IsSidecar(name, "Foo"); got != want {
			t.Errorf("IsSidecar(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestExistingMedia(t *testing.T) {
	root := t.TempDir()
	tgt := naming.Target{Root: root, Dir: filepath.Join(root, "A"), Base: "A"}
	if got, err := ExistingMedia(tgt); err != nil || got != "" {
		t.Fatalf("missing folder: %q %v", got, err)
	}
	write(t, filepath.Join(root, "A", "A.en.srt"))
	write(t, filepath.Join(root, "A", "A B.mkv"))
	if got, _ := ExistingMedia(tgt); got != "" {
		t.Fatalf("non-matching files: %q", got)
	}
	write(t, filepath.Join(root, "A", "A.MKV"))
	if got, _ := ExistingMedia(tgt); got != filepath.Join(root, "A", "A.MKV") {
		t.Fatalf("got %q", got)
	}
}

func TestResolveDir(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{root, outside, filepath.Join(root, "real")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustLink := func(target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	mustLink(outside, filepath.Join(root, "escape"))
	mustLink("real", filepath.Join(root, "inside"))
	rootLink := filepath.Join(base, "rootlink")
	mustLink(root, rootLink)

	cases := []struct {
		name, root, dir string
		ok              bool
	}{
		{"root itself", root, root, true},
		{"new subfolder", root, filepath.Join(root, "a", "b"), true},
		{"existing", root, filepath.Join(root, "real"), true},
		{"symlink inside", root, filepath.Join(root, "inside", "x"), true},
		{"symlinked root", rootLink, filepath.Join(rootLink, "a"), true},
		{"symlink escape", root, filepath.Join(root, "escape"), false},
		{"symlink escape, missing tail", root, filepath.Join(root, "escape", "new", "dir"), false},
		{"dotdot", root, filepath.Join(root, "..", "outside"), false},
		{"sibling prefix", root, root + "2", false},
	}
	for _, c := range cases {
		_, _, err := ResolveDir(c.root, c.dir)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
		if err != nil && !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("%s: err %v does not wrap ErrOutsideRoot", c.name, err)
		}
	}
}

func TestRemoveFiles(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "Show (2020)")
	s1 := filepath.Join(show, "Season 01")
	s2 := filepath.Join(show, "Season 02")
	ep := filepath.Join(s1, "Show - S01E01")
	write(t, ep+".mp4")
	write(t, ep+".en.srt")
	write(t, ep+".en.2.srt")
	write(t, ep+" - Other.en.srt") // another episode's sidecar: stays
	write(t, filepath.Join(s2, "Show - S02E01.mp4"))

	n, err := RemoveFiles(root, ep+".mp4")
	if err != nil || n != 3 {
		t.Fatalf("RemoveFiles = %d, %v", n, err)
	}
	if exists(ep+".mp4") || exists(ep+".en.srt") || exists(ep+".en.2.srt") {
		t.Error("media or sidecar still there")
	}
	if !exists(ep + " - Other.en.srt") {
		t.Error("other sidecar deleted")
	}
	if !exists(s1) || !exists(show) {
		t.Error("non-empty folders removed")
	}

	// Last file of Season 01 goes: Season 01 is removed; Show stays (Season 02).
	if err := os.Remove(ep + " - Other.en.srt"); err != nil {
		t.Fatal(err)
	}
	write(t, ep+".mkv")
	if _, err := RemoveFiles(root, ep+".mkv"); err != nil {
		t.Fatal(err)
	}
	if exists(s1) {
		t.Error("empty Season 01 kept")
	}
	if !exists(show) {
		t.Error("Show removed although Season 02 is not empty")
	}

	// Missing file: not an error. Then the last file empties everything up to
	// (not including) the root.
	if n, err := RemoveFiles(root, ep+".mkv"); err != nil || n != 0 {
		t.Errorf("missing file: %d, %v", n, err)
	}
	if _, err := RemoveFiles(root, filepath.Join(s2, "Show - S02E01.mp4")); err != nil {
		t.Fatal(err)
	}
	if exists(show) {
		t.Error("empty show folder kept")
	}
	if !exists(root) {
		t.Fatal("category root removed")
	}
}

func TestRemoveFilesFlatKeepsRoot(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "Talk.webm"))
	write(t, filepath.Join(root, "Talk.und.srt"))
	if n, err := RemoveFiles(root, filepath.Join(root, "Talk.webm")); err != nil || n != 2 {
		t.Fatalf("RemoveFiles = %d, %v", n, err)
	}
	if !exists(root) {
		t.Fatal("root removed")
	}
}

func TestRemoveFilesOutsideRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	outside := filepath.Join(base, "outside")
	write(t, filepath.Join(outside, "Film", "Film.mp4"))
	write(t, filepath.Join(root, "keep"))
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(outside, "Film", "Film.mp4"),
		filepath.Join(root, "link", "Film", "Film.mp4"),
		filepath.Join(root, "..", "outside", "Film", "Film.mp4"),
	} {
		_, err := RemoveFiles(root, p)
		if !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("RemoveFiles(%s) = %v, want ErrOutsideRoot", p, err)
		}
	}
	if !exists(filepath.Join(outside, "Film", "Film.mp4")) {
		t.Fatal("file outside the root deleted")
	}
}

func TestRemoveFilesSymlinkedMediaIsNotFollowed(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	target := filepath.Join(base, "precious.mp4")
	write(t, target)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "Talk.mp4")); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveFiles(root, filepath.Join(root, "Talk.mp4")); err != nil {
		t.Fatal(err)
	}
	if !exists(target) {
		t.Fatal("symlink target deleted")
	}
	if exists(filepath.Join(root, "Talk.mp4")) {
		t.Fatal("link kept")
	}
}
