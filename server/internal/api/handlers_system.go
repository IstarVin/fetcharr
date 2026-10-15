package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/istarvin/fetcharr/server/internal/ytdlp"
)

const (
	ytdlpProbeTimeout = 10 * time.Second
	ytdlpFailureTTL   = 30 * time.Second
)

// versionCache caches the output of `yt-dlp --version`. Successes are kept
// until invalidated (after a manual update) or until the binary file changes
// (size or modification time, which covers the background auto-update that
// replaces it in place); failures only briefly so that a missing binary does
// not spawn a process on every request.
type versionCache struct {
	probe func(ctx context.Context, binary string) (string, error)

	mu      sync.Mutex
	binary  string
	stamp   fileStamp
	version string
	failAt  time.Time
}

// fileStamp identifies a version of the binary file; the zero value means
// the file could not be stat'ed.
type fileStamp struct {
	size    int64
	modTime time.Time
}

func (a fileStamp) equal(b fileStamp) bool {
	return a.size == b.size && a.modTime.Equal(b.modTime)
}

func stampOf(path string) fileStamp {
	st, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{size: st.Size(), modTime: st.ModTime()}
}

func newVersionCache(probe func(context.Context, string) (string, error)) *versionCache {
	if probe == nil {
		probe = runVersion
	}
	return &versionCache{probe: probe}
}

func runVersion(ctx context.Context, binary string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, ytdlpProbeTimeout)
	defer cancel()
	return ytdlp.Version(ctx, binary)
}

// get returns the version of binary, or "" when it cannot be determined.
func (c *versionCache) get(ctx context.Context, binary string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	stamp := stampOf(binary)
	if c.binary == binary && c.stamp.equal(stamp) {
		if c.version != "" {
			return c.version
		}
		if !c.failAt.IsZero() && time.Since(c.failAt) < ytdlpFailureTTL {
			return ""
		}
	}
	c.binary, c.stamp, c.version, c.failAt = binary, stamp, "", time.Time{}
	v, err := c.probe(ctx, binary)
	if err != nil || v == "" {
		c.failAt = time.Now()
		return ""
	}
	c.version = v
	return v
}

func (c *versionCache) invalidate() {
	c.mu.Lock()
	c.version, c.failAt = "", time.Time{}
	c.mu.Unlock()
}

// ytdlpBinary is the binary whose version is reported: the configured one, or
// the managed one in data_dir.
func (s *server) ytdlpBinary() string {
	if s.cfg.Ytdlp.Binary != "" {
		return s.cfg.Ytdlp.Binary
	}
	return s.cfg.ManagedYtdlpPath()
}

func (s *server) handleInfo(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfg.Current()
	info := InfoDTO{
		SettingsSupported: s.settings != nil,
		SetupRequired:     len(cfg.Categories) == 0,
		Version:           s.version,
		YtdlpVersion:      s.ytdlp.get(r.Context(), s.ytdlpBinary()),
		YtdlpManaged:      s.cfg.YtdlpManaged(),
		Concurrency:       cfg.Downloads.Concurrency,
		AuthRequired:      s.cfg.AuthRequired(),
		Categories:        make([]CategoryDTO, 0, len(cfg.Categories)),
	}
	for _, c := range cfg.Categories {
		info.Categories = append(info.Categories, CategoryDTO{
			ID:        c.ID,
			Label:     c.Label,
			Layout:    string(c.Layout),
			Path:      c.Path,
			FreeBytes: freeBytes(c.Path),
		})
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *server) handleYtdlpUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.YtdlpManaged() {
		writeError(w, http.StatusConflict, codeYtdlpUnmanaged, "yt-dlp is not managed: ytdlp.binary is set")
		return
	}
	res, err := s.updater.Update(r.Context())
	switch {
	case errors.Is(err, ErrYtdlpBusy):
		writeError(w, http.StatusConflict, codeYtdlpBusy, "cannot update yt-dlp while a job is downloading")
	case err != nil:
		s.fail(w, r, err)
	default:
		s.ytdlp.invalidate()
		writeJSON(w, http.StatusOK, res)
	}
}
