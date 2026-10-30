package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/istarvin/fetcharr/server/internal/archive"
	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/library"
	"github.com/istarvin/fetcharr/server/internal/naming"
	"github.com/istarvin/fetcharr/server/internal/runner"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/store"
)

// extractDir is the folder inside the work directory the archive is
// unpacked into.
const extractDir = "extract"

// processArchive is Process for a season-pack job: extract the archive,
// number its episode files and place each one into the season folder.
// Episodes already in the library are skipped with a warning unless the job
// overwrites. A failed placement removes the episodes this job added.
func (p *Processor) processArchive(ctx context.Context, job *store.Job, cat config.Category, workDir, archiveFile string) error {
	season, err := library.SeasonTarget(cat, job)
	if err != nil {
		return processingFailed("cannot resolve the library path: %v", err)
	}
	_, seasonDir, err := library.ResolveDir(season.Root, season.Dir)
	if err != nil {
		return processingFailed("%v", err)
	}

	// 1. Room for the extracted files, which sit next to the archive.
	need, err := archive.UncompressedSize(archiveFile)
	if errors.Is(err, archive.ErrUnsupported) {
		return processingFailed("the download is not a zip, tar or 7z archive")
	}
	if err != nil {
		return processingFailed("cannot read the archive: %v", err)
	}
	if free, err := p.freeBytes(workDir); err == nil && free < uint64(need) {
		return &runner.Failure{Code: runner.CodeLowDiskSpace,
			Message: fmt.Sprintf("extracting needs %d bytes but only %d are free", need, free)}
	}

	// 2. Extract (again, after an interrupted attempt).
	dst := filepath.Join(workDir, extractDir)
	if err := os.RemoveAll(dst); err != nil {
		return processingFailed("cannot reset the extraction folder: %v", err)
	}
	if err := archive.Extract(ctx, archiveFile, dst, p.extractProgress(job)); err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return processingFailed("cannot extract the archive: %v", err)
	}

	// 3. Number the episodes.
	files, err := archive.VideoFiles(dst, library.IsVideoExt)
	if err != nil {
		return processingFailed("cannot list the extracted files: %v", err)
	}
	eps, err := archive.MapEpisodes(files, job.Archive.EpisodeOffset)
	if err != nil {
		return processingFailed("%v", err)
	}
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}

	// 4. Place each episode under its target lock.
	warnings := []string{}
	var placed, added []string
	var size int64
	placing := false
	rollback := func() {
		for _, f := range added {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				p.log.Warn("remove episode after failed placement", "job", job, "err", err)
			}
		}
		if placing {
			p.clearPlacing(job.ID)
		}
	}
	for _, e := range eps {
		meta := library.MetaOf(job)
		meta.Episode = store.Ptr(e.Episode)
		meta.Title = job.Archive.EpisodeTitles[strconv.Itoa(e.Episode)]
		t, err := naming.Resolve(cat.Path, naming.LayoutTV, meta)
		if err != nil {
			rollback()
			return processingFailed("episode %d: cannot resolve the library path: %v", e.Episode, err)
		}
		unlock, err := p.lockTarget(ctx, filepath.Join(seasonDir, t.Base))
		if err != nil {
			rollback()
			return err
		}
		existing, err := library.ExistingMedia(t)
		if err != nil {
			unlock()
			rollback()
			return processingFailed("cannot check for an existing file: %v", err)
		}
		if existing != "" && !job.Overwrite {
			unlock()
			warnings = append(warnings, fmt.Sprintf("episode %d skipped: already in the library", e.Episode))
			continue
		}
		if !placing {
			if _, err := p.st.SetPlacing(context.WithoutCancel(ctx), job.ID); err != nil {
				unlock()
				if errors.Is(err, store.ErrStatusMismatch) || errors.Is(err, store.ErrNotFound) {
					return nil // a cancel won: the service and the runner clean up
				}
				return processingFailed("cannot start placing: %v", err)
			}
			placing = true
			if p.afterSetPlacing != nil {
				p.afterSetPlacing()
			}
		}
		if st, err := os.Stat(e.Path); err == nil {
			size += st.Size()
		}
		final := t.MediaPath(filepath.Ext(e.Path))
		err = p.place(ctx, t, e.Path, nil, job.Overwrite)
		unlock()
		if err != nil {
			rollback()
			if ctx.Err() != nil {
				return err
			}
			return processingFailed("episode %d: %v", e.Episode, err)
		}
		placed = append(placed, final)
		if existing == "" {
			added = append(added, final)
		}
	}
	if len(placed) == 0 {
		return &runner.Failure{Code: runner.CodeAlreadyExists, Message: "every episode of the archive is already in the library"}
	}

	// 5. Clean up and complete.
	if err := os.RemoveAll(workDir); err != nil {
		p.log.Warn("delete work directory", "job", job, "err", err)
	}
	return p.complete(job, season.Dir, placed, size, warnings, season.RefreshDir)
}

// extractProgress persists the extraction progress of a processing job, at
// most every 500 ms.
func (p *Processor) extractProgress(job *store.Job) archive.Progress {
	var last time.Time
	return func(done, total int64) {
		now := time.Now()
		if now.Sub(last) < 500*time.Millisecond && done < total {
			return
		}
		last = now
		prog := store.Progress{DownloadedBytes: store.Ptr(done)}
		if total > 0 {
			prog.TotalBytes = store.Ptr(total)
			prog.Percent = store.Ptr(min(100, float64(done)/float64(total)*100))
		}
		row, err := p.st.UpdateIf(context.Background(), job.ID, []jobs.Status{jobs.StatusProcessing},
			store.Update{Progress: store.Set(&prog)})
		if err == nil {
			p.pub.Publish(service.Change{Kind: service.ChangeUpdated, Job: row})
		}
	}
}
