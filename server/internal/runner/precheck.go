package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// videoExts are the extensions that count as "a video file" for the
// already_exists check (spec §4.1), lower case with the dot.
var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".wmv": true, ".flv": true, ".webm": true, ".ts": true, ".m2ts": true,
	".mts": true, ".mpg": true, ".mpeg": true, ".ogv": true, ".3gp": true,
	".divx": true, ".vob": true,
}

// precheck runs the checks of spec §4.1. It returns an empty code when the
// job may start.
func (r *Runner) precheck(j *store.Job, cat config.Category) (code, msg string) {
	if need := int64(r.cfg.Downloads.MinFreeSpace); need > 0 {
		free, err := r.freeBytes(cat.Path)
		switch {
		case err != nil:
			r.log.Warn("cannot determine free disk space; skipping the check", "category", cat.ID, "err", err)
		case free < uint64(need):
			return CodeLowDiskSpace, fmt.Sprintf("only %s free in category '%s', min_free_space is %s",
				humanBytes(free), cat.ID, humanBytes(uint64(need)))
		}
	}
	if j.Overwrite {
		return "", ""
	}
	existing, err := existingMedia(cat, j)
	if err != nil {
		r.log.Warn("cannot check for an existing file", "job", j, "err", err)
		return "", ""
	}
	if existing != "" {
		return CodeAlreadyExists, "a file already exists: " + existing
	}
	return "", ""
}

// naming metadata of a job.
func metaOf(j *store.Job) naming.Meta {
	m := naming.Meta{Season: j.Season, Episode: j.Episode, Year: j.Year}
	if j.Title != nil {
		m.Title = *j.Title
	}
	if j.ShowName != nil {
		m.ShowName = *j.ShowName
	}
	return m
}

// existingMedia returns the path of a video file in the job's target folder
// whose name, without extension, is the target base name; "" if there is none.
func existingMedia(cat config.Category, j *store.Job) (string, error) {
	t, err := naming.Resolve(cat.Path, cat.Layout, metaOf(j))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(t.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if videoExts[strings.ToLower(ext)] && strings.TrimSuffix(e.Name(), ext) == t.Base {
			return filepath.Join(t.Dir, e.Name()), nil
		}
	}
	return "", nil
}

func humanBytes(n uint64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	f, suffix := float64(n), []string{"kB", "MB", "GB", "TB", "PB", "EB"}
	i := -1
	for f >= unit && i < len(suffix)-1 {
		f /= unit
		i++
	}
	return fmt.Sprintf("%.1f %s", f, suffix[i])
}
