package runner

import (
	"fmt"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/library"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// precheck runs the checks of spec §4.1. It returns an empty code when the
// job may start.
func (r *Runner) precheck(j *store.Job, cat config.Category) (code, msg string) {
	return r.precheckWithConfig(j, cat, r.cfg.Current())
}

func (r *Runner) precheckWithConfig(j *store.Job, cat config.Category, cfg *config.Config) (code, msg string) {
	if need := int64(cfg.Downloads.MinFreeSpace); need > 0 {
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

// existingMedia returns the path of a video file in the job's target folder
// whose name, without extension, is the target base name; "" if there is none.
func existingMedia(cat config.Category, j *store.Job) (string, error) {
	t, err := library.TargetOf(cat, j)
	if err != nil {
		return "", err
	}
	return library.ExistingMedia(t)
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
