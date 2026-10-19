// Package config loads and validates the server's YAML configuration
// (spec §3).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/istarvin/fetcharr/server/internal/logsafe"
	"github.com/istarvin/fetcharr/server/internal/naming"
)

// Config mirrors the YAML file of spec §3.
type Config struct {
	runtime      *Runtime
	Server       Server        `yaml:"server"`
	DataDir      string        `yaml:"data_dir"`
	Downloads    Downloads     `yaml:"downloads"`
	Ytdlp        Ytdlp         `yaml:"ytdlp"`
	History      History       `yaml:"history"`
	Categories   []Category    `yaml:"categories"`
	MediaServers []MediaServer `yaml:"media_servers"`
}

// Server is the `server:` section.
type Server struct {
	Host      string `yaml:"host"`
	Port      int    `yaml:"port"`
	AuthToken string `yaml:"auth_token"` // empty = no auth
	LogLevel  string `yaml:"log_level"`  // debug | info | warn | error
	WebUI     bool   `yaml:"web_ui"`     // serve the browser frontend at /
}

// Downloads is the `downloads:` section.
type Downloads struct {
	Concurrency  int      `yaml:"concurrency"`
	MergeFormat  string   `yaml:"merge_format"`
	MinFreeSpace ByteSize `yaml:"min_free_space"` // 0 disables the guard
	ExtraArgs    []string `yaml:"extra_args"`
}

// Ytdlp is the `ytdlp:` section.
type Ytdlp struct {
	Binary     string `yaml:"binary"` // empty = server-managed binary
	AutoUpdate bool   `yaml:"auto_update"`
}

// History is the `history:` section.
type History struct {
	RetentionDays int `yaml:"retention_days"` // 0 = keep forever
}

// Category is one destination folder plus layout rule (spec §2.1).
type Category struct {
	ID     string        `yaml:"id" json:"id"`
	Label  string        `yaml:"label" json:"label"` // defaults to ID
	Path   string        `yaml:"path" json:"path"`
	Layout naming.Layout `yaml:"layout" json:"layout"`
}

// Media server types.
const (
	MediaServerJellyfin = "jellyfin"
	MediaServerPlex     = "plex"
)

// MediaServer is one entry of `media_servers:`.
type MediaServer struct {
	Type    string            `yaml:"type"` // jellyfin | plex
	URL     string            `yaml:"url"`
	APIKey  string            `yaml:"api_key"` // jellyfin
	Token   string            `yaml:"token"`   // plex
	PathMap map[string]string `yaml:"path_map"`
}

// DefaultConfigPath is used when neither --config nor FETCHARR_CONFIG is set.
const DefaultConfigPath = "./config.yaml"

// ResolvePath picks the config path: the --config flag value if non-empty,
// else $FETCHARR_CONFIG, else DefaultConfigPath.
func ResolvePath(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("FETCHARR_CONFIG"); v != "" {
		return v
	}
	return DefaultConfigPath
}

// Default returns a Config holding the defaults of spec §3, ready for
// first-run category setup through the app.
func Default() *Config {
	return &Config{
		Server:    Server{Host: "0.0.0.0", Port: 8080, LogLevel: "info", WebUI: true},
		DataDir:   "/data",
		Downloads: Downloads{Concurrency: 2, MergeFormat: "mp4", MinFreeSpace: ByteSize(5 * 1000 * 1000 * 1000)},
		Ytdlp:     Ytdlp{AutoUpdate: true},
	}
}

// Options controls environment checks; the zero value uses the real system.
type Options struct {
	// LookPath finds executables (default exec.LookPath).
	LookPath func(string) (string, error)
}

// Load reads path, applies defaults and runs every startup validation rule of
// spec §3, including the filesystem and PATH checks. All problems are
// reported together in one error. Subfolders of data_dir are created.
func Load(path string) (*Config, error) { return LoadWithOptions(path, Options{}) }

// LoadWithOptions is Load with injectable environment lookups (for tests).
func LoadWithOptions(path string, opts Options) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if err := cfg.MakeAbsolute(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if err := cfg.ValidateEnvironment(opts); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, nil
}

// MakeAbsolute turns the configured paths into absolute ones, resolved against
// the current directory: data_dir, the category paths and ytdlp.binary (the
// last only when it contains a path separator; a bare name is looked up on
// PATH). yt-dlp runs with its work directory as the current directory, so
// every path handed to it must not depend on the server's working directory.
func (c *Config) MakeAbsolute() error {
	abs := func(p *string) error {
		if *p == "" {
			return nil
		}
		a, err := filepath.Abs(*p)
		if err != nil {
			return err
		}
		*p = a
		return nil
	}
	if err := abs(&c.DataDir); err != nil {
		return fmt.Errorf("data_dir: %w", err)
	}
	for i := range c.Categories {
		if err := abs(&c.Categories[i].Path); err != nil {
			return fmt.Errorf("categories[%d]: path: %w", i, err)
		}
	}
	if strings.ContainsRune(c.Ytdlp.Binary, filepath.Separator) {
		if err := abs(&c.Ytdlp.Binary); err != nil {
			return fmt.Errorf("ytdlp.binary: %w", err)
		}
	}
	return nil
}

// Parse decodes YAML, applies defaults and runs the checks that need no
// filesystem access. Unknown keys are an error so typos do not go unnoticed.
func Parse(data []byte) (*Config, error) {
	return parse(data, true)
}

func parse(data []byte, validate bool) (*Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	cfg.applyDefaults()
	if validate {
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	for i := range c.Categories {
		if c.Categories[i].Label == "" {
			c.Categories[i].Label = c.Categories[i].ID
		}
	}
	for i := range c.MediaServers {
		c.MediaServers[i].Type = strings.ToLower(c.MediaServers[i].Type)
	}
}

var categoryIDRe = regexp.MustCompile(`^[a-z0-9_]+$`)

// Validate runs the pure (no I/O) rules and returns every violation joined.
func (c *Config) Validate() error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		add("server.port: %d is not a valid port (1-65535)", c.Server.Port)
	}
	switch c.Server.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		add("server.log_level: %q must be one of debug, info, warn, error", c.Server.LogLevel)
	}
	if strings.TrimSpace(c.DataDir) == "" {
		add("data_dir: must not be empty")
	}
	if c.Downloads.Concurrency < 1 {
		add("downloads.concurrency: %d must be at least 1", c.Downloads.Concurrency)
	}
	if strings.TrimSpace(c.Downloads.MergeFormat) == "" {
		add("downloads.merge_format: must not be empty")
	}
	if c.Downloads.MinFreeSpace < 0 {
		add("downloads.min_free_space: must not be negative")
	}
	if c.History.RetentionDays < 0 {
		add("history.retention_days: %d must not be negative", c.History.RetentionDays)
	}

	seen := map[string]bool{}
	for i, cat := range c.Categories {
		where := fmt.Sprintf("categories[%d]", i)
		if cat.ID != "" {
			where += " (" + cat.ID + ")"
		}
		if !categoryIDRe.MatchString(cat.ID) {
			add("%s: id %q must match %s", where, cat.ID, categoryIDRe)
		} else if seen[cat.ID] {
			add("%s: duplicate id %q", where, cat.ID)
		}
		seen[cat.ID] = true
		if !naming.ValidLayout(cat.Layout) {
			add("%s: layout %q must be movie, tv or flat", where, cat.Layout)
		}
		if strings.TrimSpace(cat.Path) == "" {
			add("%s: path must not be empty", where)
		}
	}

	for i, ms := range c.MediaServers {
		where := fmt.Sprintf("media_servers[%d]", i)
		switch ms.Type {
		case MediaServerJellyfin:
			if ms.APIKey == "" {
				add("%s: jellyfin requires api_key", where)
			}
		case MediaServerPlex:
			if ms.Token == "" {
				add("%s: plex requires token", where)
			}
		default:
			add("%s: type %q must be jellyfin or plex", where, ms.Type)
		}
		if u, err := url.Parse(ms.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			add("%s: url %q must be an http(s) URL", where, logsafe.Raw(ms.URL))
		}
	}
	return errors.Join(errs...)
}

// ValidateEnvironment runs the checks that touch the system: every category
// path exists and is writable, data_dir is writable (its work/ and bin/
// subfolders are created), ffmpeg and ffprobe are on PATH, and a configured
// ytdlp.binary exists and is executable. A managed yt-dlp binary (empty
// ytdlp.binary) is downloaded later by the yt-dlp manager, not checked here.
func (c *Config) ValidateEnvironment(opts Options) error {
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	for i, cat := range c.Categories {
		if err := checkWritableDir(cat.Path); err != nil {
			add("categories[%d] (%s): path %s", i, cat.ID, err)
		}
	}

	if err := checkWritableDir(c.DataDir); err != nil {
		add("data_dir: %s", err)
	} else {
		for _, sub := range []string{c.WorkDir(), c.BinDir()} {
			if err := os.MkdirAll(sub, 0o755); err != nil {
				add("data_dir: cannot create %s: %v", sub, err)
			}
		}
	}

	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := lookPath(tool); err != nil {
			add("%s not found on PATH", tool)
		}
	}

	if c.Ytdlp.Binary != "" {
		p, err := lookPath(c.Ytdlp.Binary)
		if err != nil {
			add("ytdlp.binary: %q is not an executable file: %v", c.Ytdlp.Binary, err)
		} else if st, err := os.Stat(p); err != nil || st.IsDir() {
			add("ytdlp.binary: %q is not a file", c.Ytdlp.Binary)
		}
	}
	return errors.Join(errs...)
}

// checkWritableDir verifies dir exists, is a directory and that a file can be
// created in it. The returned error text starts after the subject, e.g.
// "/x does not exist".
func checkWritableDir(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%q does not exist", dir)
		}
		return fmt.Errorf("%q: %w", dir, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}
	f, err := os.CreateTemp(dir, ".fetcharr-write-test-*")
	if err != nil {
		return fmt.Errorf("%q is not writable: %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name) // best-effort cleanup of the probe file
	return nil
}

// Addr returns the listen address, "host:port".
func (c *Config) Addr() string {
	return net.JoinHostPort(c.Server.Host, strconv.Itoa(c.Server.Port))
}

// AuthRequired reports whether a bearer token is configured.
func (c *Config) AuthRequired() bool { return c.Server.AuthToken != "" }

// YtdlpManaged reports whether the server manages its own yt-dlp binary.
func (c *Config) YtdlpManaged() bool { return c.Ytdlp.Binary == "" }

// WorkDir is <data_dir>/work, the parent of per-job work directories.
func (c *Config) WorkDir() string { return filepath.Join(c.DataDir, "work") }

// BinDir is <data_dir>/bin, home of the managed yt-dlp binary.
func (c *Config) BinDir() string { return filepath.Join(c.DataDir, "bin") }

// ManagedYtdlpPath is <data_dir>/bin/yt-dlp.
func (c *Config) ManagedYtdlpPath() string { return filepath.Join(c.BinDir(), "yt-dlp") }

// DBPath is the SQLite database file, <data_dir>/fetcharr.db.
func (c *Config) DBPath() string { return filepath.Join(c.DataDir, "fetcharr.db") }

// JobWorkDir is <data_dir>/work/<jobID>.
func (c *Config) JobWorkDir(jobID string) string { return filepath.Join(c.WorkDir(), jobID) }

// Category returns the category with the given id.
func (c *Config) Category(id string) (Category, bool) {
	for _, cat := range c.Current().Categories {
		if cat.ID == id {
			return cat, true
		}
	}
	return Category{}, false
}
