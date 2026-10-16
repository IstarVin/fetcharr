package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func TestOverwriteFailureRestoresOriginalFiles(t *testing.T) {
	for _, tc := range []struct {
		name, failAt string
		copy         bool
	}{
		{name: "rename media failure", failAt: "media"},
		{name: "rename subtitle failure", failAt: "subtitle"},
		{name: "copy media failure", failAt: "media", copy: true},
		{name: "copy final rename failure", failAt: "partial", copy: true},
		{name: "cancelled copy", failAt: "cancel", copy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, nil)
			_, media := f.job()
			target, err := f.proc.targetFor("movies", "Dune", 2024)
			if err != nil {
				t.Fatal(err)
			}
			originals := map[string]string{
				target.Base + ".mp4":    "original media",
				target.Base + ".mkv":    "stale media",
				target.Base + ".en.srt": "original subtitle",
				target.Base + ".fr.srt": "stale subtitle",
			}
			for name, data := range originals {
				writeFile(t, filepath.Join(target.Dir, name), data)
			}
			en, es := filepath.Join(filepath.Dir(media), "en.srt"), filepath.Join(filepath.Dir(media), "es.srt")
			writeFile(t, en, "replacement subtitle")
			writeFile(t, es, "new subtitle")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("injected placement failure")
			mediaDst := filepath.Join(target.Dir, target.Base+".mp4")
			f.proc.rename = func(src, dst string) error {
				if (tc.failAt == "media" && src == media) ||
					(tc.failAt == "subtitle" && src == es) ||
					(tc.failAt == "partial" && src == mediaDst+".partial") {
					return failure
				}
				if tc.copy && !strings.HasSuffix(src, ".partial") {
					return &os.LinkError{Op: "rename", Old: src, New: dst, Err: syscall.EXDEV}
				}
				if tc.failAt == "cancel" && dst == filepath.Join(target.Dir, target.Base+".es.srt") {
					cancel()
				}
				return os.Rename(src, dst)
			}
			err = f.proc.place(ctx, target, media, []subtitle{{path: en, lang: "en"}, {path: es, lang: "es"}}, true)
			wantErr := failure
			if tc.failAt == "cancel" {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("placement error = %v, want %v", err, wantErr)
			}
			files := listFiles(t, target.Dir)
			if len(files) != len(originals) {
				t.Errorf("files after rollback = %v", files)
			}
			for name, data := range originals {
				if got := readFile(t, filepath.Join(target.Dir, name)); got != data {
					t.Errorf("%s = %q, want %q", name, got, data)
				}
			}
			if !exists(media) {
				t.Error("media removed from work directory after failure")
			}
		})
	}
}

func TestOverwriteAcrossFilesystemsRemovesBackupsAndStaleFiles(t *testing.T) {
	f := newFixture(t, func(o *Options) {
		o.Rename = func(src, dst string) error {
			if strings.HasSuffix(src, ".partial") {
				return os.Rename(src, dst)
			}
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: syscall.EXDEV}
		}
	})
	_, media := f.job()
	target, err := f.proc.targetFor("movies", "Dune", 2024)
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{".mp4", ".mkv", ".en.srt", ".fr.srt"} {
		writeFile(t, filepath.Join(target.Dir, target.Base+ext), "old")
	}
	sub := filepath.Join(filepath.Dir(media), "en.srt")
	writeFile(t, sub, "replacement subtitle")
	if err := f.proc.place(t.Context(), target, media, []subtitle{{path: sub, lang: "en"}}, true); err != nil {
		t.Fatal(err)
	}
	files := listFiles(t, target.Dir)
	slices.Sort(files)
	if want := []string{target.Base + ".en.srt", target.Base + ".mp4"}; !slices.Equal(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
	if got := readFile(t, filepath.Join(target.Dir, target.Base+".en.srt")); got != "replacement subtitle" {
		t.Errorf("subtitle = %q", got)
	}
	if got := readFile(t, filepath.Join(target.Dir, target.Base+".mp4")); got != "video-bytes" {
		t.Errorf("media = %q", got)
	}
}

func TestOverwriteRestoreFailureRetainsBackupAndReportsPath(t *testing.T) {
	f := newFixture(t, nil)
	_, media := f.job()
	target, err := f.proc.targetFor("movies", "Dune", 2024)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(target.Dir, target.Base+".en.srt")
	writeFile(t, dst, "original subtitle")
	sub := filepath.Join(filepath.Dir(media), "en.srt")
	writeFile(t, sub, "replacement subtitle")
	failure := errors.New("injected media failure")
	f.proc.rename = func(src, dstPath string) error {
		if src == media {
			// Simulate another writer obstructing restoration after a
			// replacement was placed successfully.
			if err := os.Remove(dst); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(dst, 0o755); err != nil {
				t.Fatal(err)
			}
			return failure
		}
		return os.Rename(src, dstPath)
	}
	err = f.proc.place(t.Context(), target, media, []subtitle{{path: sub, lang: "en"}}, true)
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "restore "+dst+" from ") {
		t.Fatalf("restore error not reported: %v", err)
	}
	backups, globErr := filepath.Glob(filepath.Join(target.Dir, ".fetcharr-overwrite-*", filepath.Base(dst)))
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("retained backups = %v, error %v", backups, globErr)
	}
	if got := readFile(t, backups[0]); got != "original subtitle" {
		t.Errorf("retained backup = %q", got)
	}
	if !strings.Contains(err.Error(), backups[0]) {
		t.Errorf("error does not identify recovery file: %v", err)
	}
}
