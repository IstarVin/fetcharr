package api

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	ytdlpProbeTimeout = 10 * time.Second
	ytdlpFailureTTL   = 30 * time.Second
)

// versionCache caches the output of `yt-dlp --version`. Successes are kept
// until invalidated (after an update); failures only briefly so that a
// missing binary does not spawn a process on every request.
type versionCache struct {
	probe func(ctx context.Context, binary string) (string, error)

	mu      sync.Mutex
	binary  string
	version string
	failAt  time.Time
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
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// get returns the version of binary, or "" when it cannot be determined.
func (c *versionCache) get(ctx context.Context, binary string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.binary == binary {
		if c.version != "" {
			return c.version
		}
		if !c.failAt.IsZero() && time.Since(c.failAt) < ytdlpFailureTTL {
			return ""
		}
	}
	c.binary, c.version, c.failAt = binary, "", time.Time{}
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
	info := InfoDTO{
		Version:      s.version,
		YtdlpVersion: s.ytdlp.get(r.Context(), s.ytdlpBinary()),
		YtdlpManaged: s.cfg.YtdlpManaged(),
		Concurrency:  s.cfg.Downloads.Concurrency,
		AuthRequired: s.cfg.AuthRequired(),
		Categories:   make([]CategoryDTO, 0, len(s.cfg.Categories)),
	}
	for _, c := range s.cfg.Categories {
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
