package process

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// archiveJob inserts a processing season-pack job whose work directory
// holds a zip of entries (name -> content) and returns the job and the zip.
func (f *fixture) archiveJob(entries map[string]string, mods ...func(*store.Job)) (*store.Job, string) {
	f.t.Helper()
	j, media := f.job(append([]func(*store.Job){func(j *store.Job) {
		j.Category = "tv"
		j.Title, j.Year = nil, store.Ptr(2026)
		j.ShowName, j.Season = store.Ptr("Dead-End Job"), store.Ptr(1)
		j.Archive = &store.Archive{EpisodeTitles: map[string]string{"1": "Convenience Store Rules"}}
	}}, mods...)...)
	_ = os.Remove(media)
	p := filepath.Join(filepath.Dir(media), "media.zip")
	out, err := os.Create(p)
	if err != nil {
		f.t.Fatal(err)
	}
	w := zip.NewWriter(out)
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Store})
		if err != nil {
			f.t.Fatal(err)
		}
		_, _ = fw.Write([]byte(entries[n]))
	}
	if err := w.Close(); err != nil {
		f.t.Fatal(err)
	}
	_ = out.Close()
	return j, p
}

var deadEndPack = map[string]string{
	"Dead-End.Job.S01.1080p/Dead-End.Job.S01E01.Convenience.Store.Rules.1080p.NF.WEB-DL.mkv": "episode-one-bytes",
	"Dead-End.Job.S01.1080p/Dead-End.Job.S01E02.A.Journey.1080p.NF.WEB-DL.mkv":               "episode-two-bytes",
	"Dead-End.Job.S01.1080p/Dead-End.Job.S01E03.Third.1080p.NF.WEB-DL.mkv":                   "episode-three-bytes",
	"Dead-End.Job.S01.1080p/readme.txt":                                                      "x",
}

func TestArchivePlacesEpisodes(t *testing.T) {
	f := newFixture(t, nil)
	season := filepath.Join(f.root("tv"), "Dead-End Job (2026)", "Season 01")
	// Episode 3 is already in the library: skipped with a warning.
	writeFile(t, filepath.Join(season, "Dead-End Job - S01E03.mkv"), "old")
	j, zipPath := f.archiveJob(deadEndPack)
	if err := f.process(j, zipPath); err != nil {
		t.Fatal(err)
	}
	got := f.get(j.ID)
	if got.Status != jobs.StatusCompleted || got.Placing {
		t.Fatalf("status = %s placing=%v", got.Status, got.Placing)
	}
	want := []string{
		filepath.Join(season, "Dead-End Job - S01E01 - Convenience Store Rules.mkv"),
		filepath.Join(season, "Dead-End Job - S01E02.mkv"),
	}
	if !slices.Equal(got.Files, want) {
		t.Fatalf("files = %v", got.Files)
	}
	if got.FilePath == nil || *got.FilePath != season {
		t.Errorf("file_path = %v", got.FilePath)
	}
	if readFile(t, want[0]) != "episode-one-bytes" || readFile(t, filepath.Join(season, "Dead-End Job - S01E03.mkv")) != "old" {
		t.Error("wrong contents")
	}
	if !slices.Equal(got.Warnings, []string{"episode 3 skipped: already in the library"}) {
		t.Errorf("warnings = %v", got.Warnings)
	}
	if exists(f.cfg.JobWorkDir(j.ID)) {
		t.Error("work directory kept")
	}
}

func TestArchiveOffsetAndOverwrite(t *testing.T) {
	f := newFixture(t, nil)
	season := filepath.Join(f.root("tv"), "Dead-End Job (2026)", "Season 01")
	writeFile(t, filepath.Join(season, "Dead-End Job - S01E11.mkv"), "old")
	j, zipPath := f.archiveJob(deadEndPack, func(j *store.Job) {
		j.Archive.EpisodeOffset = 10
		j.Overwrite = true
	})
	if err := f.process(j, zipPath); err != nil {
		t.Fatal(err)
	}
	got := f.get(j.ID)
	if len(got.Files) != 3 || filepath.Base(got.Files[0]) != "Dead-End Job - S01E11.mkv" || filepath.Base(got.Files[2]) != "Dead-End Job - S01E13.mkv" {
		t.Fatalf("files = %v", got.Files)
	}
	if readFile(t, got.Files[0]) != "episode-one-bytes" {
		t.Error("existing episode not overwritten")
	}
}

func TestArchiveAllEpisodesExist(t *testing.T) {
	f := newFixture(t, nil)
	season := filepath.Join(f.root("tv"), "Dead-End Job (2026)", "Season 01")
	for _, n := range []string{"S01E01 - Convenience Store Rules", "S01E02", "S01E03"} {
		writeFile(t, filepath.Join(season, "Dead-End Job - "+n+".mkv"), "old")
	}
	j, zipPath := f.archiveJob(deadEndPack)
	err := f.process(j, zipPath)
	var fail *runner.Failure
	if !errors.As(err, &fail) || fail.Code != runner.CodeAlreadyExists {
		t.Fatalf("err = %v", err)
	}
}

func TestArchiveNotAnArchive(t *testing.T) {
	f := newFixture(t, nil)
	j, zipPath := f.archiveJob(deadEndPack)
	if err := os.WriteFile(zipPath, []byte("\x1a\x45\xdf\xa3 a single episode"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := f.process(j, zipPath)
	var fail *runner.Failure
	if !errors.As(err, &fail) || fail.Code != runner.CodeProcessingFailed {
		t.Fatalf("err = %v", err)
	}
}

func TestArchiveLowDiskSpace(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.FreeBytes = func(string) (uint64, error) { return 10, nil } })
	j, zipPath := f.archiveJob(deadEndPack)
	err := f.process(j, zipPath)
	var fail *runner.Failure
	if !errors.As(err, &fail) || fail.Code != runner.CodeLowDiskSpace {
		t.Fatalf("err = %v", err)
	}
}

func TestArchiveRollsBackOnFailedPlacement(t *testing.T) {
	calls := 0
	f := newFixture(t, func(o *Options) {
		o.Rename = func(from, to string) error {
			calls++
			if calls == 2 {
				return errors.New("disk on fire")
			}
			return os.Rename(from, to)
		}
	})
	j, zipPath := f.archiveJob(deadEndPack)
	if err := f.process(j, zipPath); err == nil {
		t.Fatal("placement failure not reported")
	}
	season := filepath.Join(f.root("tv"), "Dead-End Job (2026)", "Season 01")
	if exists(filepath.Join(season, "Dead-End Job - S01E01 - Convenience Store Rules.mkv")) {
		t.Error("episode 1 left in the library after rollback")
	}
	if f.get(j.ID).Placing {
		t.Error("placing flag left set")
	}
}
