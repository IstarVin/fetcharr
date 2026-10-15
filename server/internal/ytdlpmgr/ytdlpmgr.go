// Package ytdlpmgr manages the server-owned yt-dlp binary (spec §8): it
// downloads the binary from the official GitHub releases when it is missing,
// updates it with `yt-dlp -U` at startup and every 24 hours, and serves the
// manual update endpoint.
//
// Updates replace the binary file in place, so they never run while a job is
// downloading. The auto-update puts the dispatcher on hold, waits for the
// downloads to finish, updates, and always releases the hold (decision D1).
// The manual update never waits: it returns api.ErrYtdlpBusy at once when a
// job is downloading (decision D8). Jobs in processing do not count.
package ytdlpmgr

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/istarvin/fetcharr/server/internal/api"
	"github.com/istarvin/fetcharr/server/internal/logsafe"
	"github.com/istarvin/fetcharr/server/internal/ytdlp"
)

// Defaults for Options.
const (
	// DefaultBaseURL serves the assets of the latest official release.
	DefaultBaseURL = "https://github.com/yt-dlp/yt-dlp/releases/latest/download"
	// DefaultInterval is the auto-update period (spec §8).
	DefaultInterval = 24 * time.Hour
	// DefaultDownloadTimeout bounds the first-start download.
	DefaultDownloadTimeout = 5 * time.Minute
	// DefaultUpdateTimeout bounds one `yt-dlp -U` run.
	DefaultUpdateTimeout = 5 * time.Minute

	maxBinaryBytes  = 256 << 20 // refuse absurdly large downloads
	versionTimeout  = 15 * time.Second
	maxOutputExcerp = 600 // bytes of yt-dlp output kept in an error
	tempPrefix      = ".yt-dlp-download-"
)

// Runner is the part of the download runner the manager needs; *runner.Runner
// implements it.
type Runner interface {
	Hold() (release func())
	WaitIdle(ctx context.Context) error
	Downloading() int
}

// Options configures New. Binary and Runner are required.
type Options struct {
	// Binary is the managed binary path (cfg.ManagedYtdlpPath()).
	Binary string
	Runner Runner
	Logger *slog.Logger

	// AutoUpdate enables the update at startup and every Interval in Run.
	AutoUpdate bool
	// Interval is the auto-update period (default DefaultInterval).
	Interval time.Duration

	// BaseURL is where release assets are downloaded from, without a trailing
	// slash (default DefaultBaseURL). Tests point it at an httptest server.
	BaseURL string
	// HTTPClient downloads the binary (default http.DefaultClient with
	// DownloadTimeout applied through the request context).
	HTTPClient *http.Client
	// DownloadTimeout bounds the download (default DefaultDownloadTimeout).
	DownloadTimeout time.Duration
	// UpdateTimeout bounds one `-U` run (default DefaultUpdateTimeout).
	UpdateTimeout time.Duration

	// GOOS, GOARCH and Musl select the release asset (default: the running
	// platform). Tests override them.
	GOOS, GOARCH string
	Musl         *bool
}

// Manager owns the managed yt-dlp binary. It implements api.YtdlpUpdater.
type Manager struct {
	binary   string
	rn       Runner
	log      *slog.Logger
	auto     bool
	interval time.Duration

	baseURL         string
	client          *http.Client
	downloadTimeout time.Duration
	updateTimeout   time.Duration
	asset           string

	// updMu serialises updates. The manual update only tries it (it never
	// waits), the auto-update blocks on it.
	updMu sync.Mutex

	mu         sync.Mutex
	downloaded bool // the binary was just downloaded, so it is the latest
}

var _ api.YtdlpUpdater = (*Manager)(nil)

// New returns a Manager. It does nothing until Ensure or Run is called.
func New(o Options) *Manager {
	m := &Manager{
		binary: o.Binary, rn: o.Runner, log: o.Logger, auto: o.AutoUpdate,
		interval: o.Interval, baseURL: strings.TrimRight(o.BaseURL, "/"),
		client: o.HTTPClient, downloadTimeout: o.DownloadTimeout, updateTimeout: o.UpdateTimeout,
	}
	if m.log == nil {
		m.log = slog.Default()
	}
	if m.interval <= 0 {
		m.interval = DefaultInterval
	}
	if m.baseURL == "" {
		m.baseURL = DefaultBaseURL
	}
	if m.client == nil {
		m.client = http.DefaultClient
	}
	if m.downloadTimeout <= 0 {
		m.downloadTimeout = DefaultDownloadTimeout
	}
	if m.updateTimeout <= 0 {
		m.updateTimeout = DefaultUpdateTimeout
	}
	goos, goarch := o.GOOS, o.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	musl := false
	if o.Musl != nil {
		musl = *o.Musl
	} else if goos == "linux" {
		musl = detectMusl()
	}
	m.asset = AssetName(goos, goarch, musl)
	return m
}

// AssetName returns the release asset to download for a platform: the
// standalone PyInstaller build where yt-dlp publishes one, else the python
// zipapp "yt-dlp" (which needs python3 on the host).
func AssetName(goos, goarch string, musl bool) string {
	switch goos {
	case "linux":
		libc := "linux"
		if musl {
			libc = "musllinux"
		}
		switch goarch {
		case "amd64":
			return "yt-dlp_" + libc
		case "arm64":
			return "yt-dlp_" + libc + "_aarch64"
		case "arm":
			if !musl {
				return "yt-dlp_linux_armv7l"
			}
		}
	case "darwin":
		return "yt-dlp_macos"
	}
	return "yt-dlp"
}

// detectMusl reports whether the host libc is musl (e.g. Alpine), where the
// glibc builds do not run.
func detectMusl() bool {
	m, _ := filepath.Glob("/lib/ld-musl-*.so.1")
	return len(m) > 0
}

// Asset returns the release asset name Ensure downloads.
func (m *Manager) Asset() string { return m.asset }

// Ensure downloads the binary when it does not exist yet (first start). The
// download goes to a temporary file in the same directory, is checked with
// `--version`, made executable (0755) and renamed into place atomically, so
// a partial download never becomes the binary.
func (m *Manager) Ensure(ctx context.Context) error {
	dir := filepath.Dir(m.binary)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	removeStaleTemps(dir)
	if st, err := os.Stat(m.binary); err == nil && st.Mode().IsRegular() {
		return nil
	}
	url := m.baseURL + "/" + m.asset
	m.log.Info("downloading yt-dlp", "asset", m.asset, "to", m.binary)
	if err := m.download(ctx, url, dir); err != nil {
		return fmt.Errorf("download yt-dlp (%s): %w", m.asset, err)
	}
	m.mu.Lock()
	m.downloaded = true
	m.mu.Unlock()
	if v, err := m.version(ctx); err == nil {
		m.log.Info("yt-dlp installed", "version", v)
	}
	return nil
}

func (m *Manager) download(ctx context.Context, url, dir string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, m.downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %s", resp.Status)
	}
	f, err := os.CreateTemp(dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxBinaryBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n == 0 || n > maxBinaryBytes {
		return fmt.Errorf("unexpected download size %d", n)
	}
	if err = os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	// Reject an error page or a truncated file before it replaces anything.
	vctx, vcancel := context.WithTimeout(ctx, versionTimeout)
	defer vcancel()
	if _, err = ytdlp.Version(vctx, tmp); err != nil {
		return fmt.Errorf("downloaded file is not a working yt-dlp: %w", err)
	}
	return os.Rename(tmp, m.binary)
}

func removeStaleTemps(dir string) {
	stale, _ := filepath.Glob(filepath.Join(dir, tempPrefix+"*"))
	for _, p := range stale {
		_ = os.Remove(p)
	}
}

func (m *Manager) version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	return ytdlp.Version(ctx, m.binary)
}

// Run is the auto-update loop: when auto_update is enabled it updates at
// startup (unless the binary was just downloaded) and then every Interval,
// until ctx is done. Failures are logged and the loop continues. Run returns
// at once when auto-update is disabled.
func (m *Manager) Run(ctx context.Context) {
	if !m.auto {
		return
	}
	m.mu.Lock()
	skip := m.downloaded
	m.downloaded = false
	m.mu.Unlock()
	if !skip {
		m.autoUpdate(ctx)
	}
	t := time.NewTicker(m.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.autoUpdate(ctx)
		}
	}
}

// AutoUpdate performs one drained update (D1): hold the dispatcher, wait
// until nothing is downloading, update, release the hold. The hold is
// released on every path, including failure, panic and cancellation. Errors
// are logged and returned.
func (m *Manager) AutoUpdate(ctx context.Context) error {
	release := m.rn.Hold()
	defer release()
	if m.rn.Downloading() > 0 {
		m.log.Info("yt-dlp update due; waiting for running downloads to finish", "downloading", m.rn.Downloading())
	}
	if err := m.rn.WaitIdle(ctx); err != nil {
		return err
	}
	m.updMu.Lock()
	defer m.updMu.Unlock()
	_, err := m.update(ctx)
	return err
}

func (m *Manager) autoUpdate(ctx context.Context) {
	if err := m.AutoUpdate(ctx); err != nil && ctx.Err() == nil {
		m.log.Error("yt-dlp auto-update failed", "err", logsafe.Err(err))
	}
}

// Update implements api.YtdlpUpdater (D8). It returns api.ErrYtdlpBusy at
// once when a job is downloading or another update is running; otherwise it
// holds the dispatcher, re-checks (a job may have started in between) and
// updates right away.
func (m *Manager) Update(ctx context.Context) (api.UpdateResult, error) {
	if m.rn.Downloading() > 0 {
		return api.UpdateResult{}, api.ErrYtdlpBusy
	}
	release := m.rn.Hold()
	defer release()
	if m.rn.Downloading() > 0 {
		return api.UpdateResult{}, api.ErrYtdlpBusy
	}
	if !m.updMu.TryLock() {
		return api.UpdateResult{}, api.ErrYtdlpBusy
	}
	defer m.updMu.Unlock()
	return m.update(ctx)
}

// update runs `binary -U` and reports the versions before and after. The
// caller holds updMu and the dispatcher hold.
func (m *Manager) update(ctx context.Context) (api.UpdateResult, error) {
	prev, err := m.version(ctx)
	if err != nil {
		return api.UpdateResult{}, fmt.Errorf("read current yt-dlp version: %w", err)
	}
	uctx, cancel := context.WithTimeout(ctx, m.updateTimeout)
	defer cancel()
	cmd := exec.CommandContext(uctx, m.binary, "-U")
	cmd.WaitDelay = 5 * time.Second
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return api.UpdateResult{}, fmt.Errorf("yt-dlp -U: %w: %s", err, excerpt(out.String()))
	}
	cur, err := m.version(ctx)
	if err != nil {
		return api.UpdateResult{}, fmt.Errorf("read updated yt-dlp version: %w", err)
	}
	if cur == prev {
		m.log.Info("yt-dlp is up to date", "version", cur)
	} else {
		m.log.Info("yt-dlp updated", "previous", prev, "current", cur)
	}
	return api.UpdateResult{Previous: prev, Current: cur}, nil
}

// excerpt returns the tail of yt-dlp's output for an error message.
func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxOutputExcerp {
		s = "..." + s[len(s)-maxOutputExcerp:]
	}
	return s
}
