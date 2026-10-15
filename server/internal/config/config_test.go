package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"0", 0, false},
		{"1024", 1024, false},
		{"500B", 500, false},
		{"500MB", 500_000_000, false},
		{"5GB", 5_000_000_000, false},
		{"5gb", 5_000_000_000, false},
		{"5 GB", 5_000_000_000, false},
		{" 5GB ", 5_000_000_000, false},
		{"1TB", 1_000_000_000_000, false},
		{"2KB", 2000, false},
		{"500MiB", 500 * 1024 * 1024, false},
		{"1GiB", 1 << 30, false},
		{"1.5GB", 1_500_000_000, false},
		{"1.5GiB", 3 << 29, false},
		{"2G", 2 << 30, false},
		{"1KiB", 1024, false},
		{"2TiB", 2 << 40, false},
		{"", 0, true},
		{"GB", 0, true},
		{"-5GB", 0, true},
		{"5XB", 0, true},
		{"five", 0, true},
		{"1.2.3GB", 0, true},
		{"99999999999TB", 0, true},
		{"5GB5", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseByteSize(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// fixture creates category dirs and a data dir under a temp dir and returns
// a minimal valid YAML plus the dirs.
type fixture struct {
	root, data, movies, tv string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	f := fixture{root: root, data: filepath.Join(root, "data"), movies: filepath.Join(root, "movies"), tv: filepath.Join(root, "tv")}
	for _, d := range []string{f.data, f.movies, f.tv} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f fixture) yaml(extra string) string {
	return "data_dir: " + f.data + "\ncategories:\n" +
		"  - {id: movies, label: Movies, path: " + f.movies + ", layout: movie}\n" +
		"  - {id: tv, path: " + f.tv + ", layout: tv}\n" + extra
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func okLook(string) (string, error) { return "/bin/true", nil }

func write(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaultsAndAccessors(t *testing.T) {
	f := newFixture(t)
	cfg, err := LoadWithOptions(write(t, f.root, f.yaml("")), Options{LookPath: okLook})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Host != "0.0.0.0" || cfg.Server.Port != 8080 || cfg.Server.LogLevel != "info" || cfg.Server.AuthToken != "" {
		t.Errorf("server defaults: %+v", cfg.Server)
	}
	if cfg.Downloads.Concurrency != 2 || cfg.Downloads.MergeFormat != "mp4" || cfg.Downloads.MinFreeSpace != 5_000_000_000 || len(cfg.Downloads.ExtraArgs) != 0 {
		t.Errorf("downloads defaults: %+v", cfg.Downloads)
	}
	if !cfg.Ytdlp.AutoUpdate || cfg.Ytdlp.Binary != "" || !cfg.YtdlpManaged() {
		t.Errorf("ytdlp defaults: %+v", cfg.Ytdlp)
	}
	if cfg.History.RetentionDays != 0 || cfg.AuthRequired() {
		t.Errorf("history/auth defaults")
	}
	if cfg.Addr() != "0.0.0.0:8080" {
		t.Errorf("Addr = %q", cfg.Addr())
	}
	if cfg.Categories[0].Label != "Movies" || cfg.Categories[1].Label != "tv" {
		t.Errorf("labels: %+v", cfg.Categories)
	}
	if c, ok := cfg.Category("tv"); !ok || c.Layout != "tv" {
		t.Errorf("Category(tv) = %+v %v", c, ok)
	}
	if _, ok := cfg.Category("nope"); ok {
		t.Error("Category(nope) found")
	}
	for _, sub := range []string{cfg.WorkDir(), cfg.BinDir()} {
		if st, err := os.Stat(sub); err != nil || !st.IsDir() {
			t.Errorf("subfolder %s not created: %v", sub, err)
		}
	}
	if cfg.DBPath() != filepath.Join(f.data, "fetcharr.db") || cfg.JobWorkDir("x") != filepath.Join(f.data, "work", "x") ||
		cfg.ManagedYtdlpPath() != filepath.Join(f.data, "bin", "yt-dlp") {
		t.Errorf("paths wrong")
	}
}

func TestLoadFullFile(t *testing.T) {
	f := newFixture(t)
	y := f.yaml(`
server: {host: 127.0.0.1, port: 9090, auth_token: s3cret, log_level: debug}
downloads:
  concurrency: 4
  merge_format: mkv
  min_free_space: 500MiB
  extra_args: ["--limit-rate", "5M"]
ytdlp: {auto_update: false}
history: {retention_days: 30}
media_servers:
  - {type: jellyfin, url: "http://jellyfin:8096", api_key: k, path_map: {"/media": "/data/media"}}
  - {type: Plex, url: "http://plex:32400", token: t}
`)
	cfg, err := LoadWithOptions(write(t, f.root, y), Options{LookPath: okLook})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr() != "127.0.0.1:9090" || cfg.Server.AuthToken != "s3cret" || !cfg.AuthRequired() || cfg.Server.LogLevel != "debug" {
		t.Errorf("server: %+v", cfg.Server)
	}
	if cfg.Downloads.Concurrency != 4 || cfg.Downloads.MergeFormat != "mkv" || cfg.Downloads.MinFreeSpace != 500<<20 ||
		strings.Join(cfg.Downloads.ExtraArgs, " ") != "--limit-rate 5M" {
		t.Errorf("downloads: %+v", cfg.Downloads)
	}
	if cfg.Ytdlp.AutoUpdate || cfg.History.RetentionDays != 30 {
		t.Errorf("ytdlp/history: %+v %+v", cfg.Ytdlp, cfg.History)
	}
	if len(cfg.MediaServers) != 2 || cfg.MediaServers[0].PathMap["/media"] != "/data/media" || cfg.MediaServers[1].Type != "plex" {
		t.Errorf("media servers: %+v", cfg.MediaServers)
	}
}

func TestMinFreeSpaceYAMLForms(t *testing.T) {
	f := newFixture(t)
	for in, want := range map[string]int64{"0": 0, "5GB": 5_000_000_000, "'500MB'": 500_000_000, "1048576": 1048576} {
		cfg, err := LoadWithOptions(write(t, f.root, f.yaml("downloads: {min_free_space: "+in+"}\n")), Options{LookPath: okLook})
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if int64(cfg.Downloads.MinFreeSpace) != want {
			t.Errorf("%s: got %d want %d", in, cfg.Downloads.MinFreeSpace, want)
		}
	}
	_, err := LoadWithOptions(write(t, f.root, f.yaml("downloads: {min_free_space: lots}\n")), Options{LookPath: okLook})
	if err == nil || !strings.Contains(err.Error(), "min_free_space") && !strings.Contains(err.Error(), "invalid byte size") {
		t.Errorf("err = %v", err)
	}
}

func TestParseValidationErrors(t *testing.T) {
	cat := func(id, layout string) string {
		return "  - {id: " + id + ", path: /tmp, layout: " + layout + "}\n"
	}
	tests := []struct {
		name string
		yaml string
		want string // substring of the error
	}{
		{"no categories", "data_dir: /d\n", "at least one category"},
		{"empty categories", "categories: []\n", "at least one category"},
		{"dup id", "categories:\n" + cat("a", "flat") + cat("a", "tv"), `duplicate id "a"`},
		{"uppercase id", "categories:\n" + cat("Movies", "movie"), "must match"},
		{"dash id", "categories:\n" + cat("a-b", "movie"), "must match"},
		{"empty id", "categories:\n  - {path: /tmp, layout: flat}\n", "must match"},
		{"bad layout", "categories:\n" + cat("a", "series"), `layout "series"`},
		{"missing layout", "categories:\n  - {id: a, path: /tmp}\n", "layout"},
		{"missing path", "categories:\n  - {id: a, layout: flat}\n", "path must not be empty"},
		{"port 0", "server: {port: 0}\ncategories:\n" + cat("a", "flat"), "server.port"},
		{"port too big", "server: {port: 70000}\ncategories:\n" + cat("a", "flat"), "server.port"},
		{"log level", "server: {log_level: trace}\ncategories:\n" + cat("a", "flat"), "log_level"},
		{"concurrency 0", "downloads: {concurrency: 0}\ncategories:\n" + cat("a", "flat"), "concurrency"},
		{"concurrency negative", "downloads: {concurrency: -1}\ncategories:\n" + cat("a", "flat"), "concurrency"},
		{"bad min_free_space", "downloads: {min_free_space: 5XB}\ncategories:\n" + cat("a", "flat"), "unknown unit"},
		{"negative min_free_space", "downloads: {min_free_space: -5GB}\ncategories:\n" + cat("a", "flat"), "invalid byte size"},
		{"empty merge format", "downloads: {merge_format: ''}\ncategories:\n" + cat("a", "flat"), "merge_format"},
		{"negative retention", "history: {retention_days: -1}\ncategories:\n" + cat("a", "flat"), "retention_days"},
		{"empty data_dir", "data_dir: ''\ncategories:\n" + cat("a", "flat"), "data_dir"},
		{"unknown key", "serverr: {}\ncategories:\n" + cat("a", "flat"), "serverr"},
		{"bad yaml", "categories: [", "parse YAML"},
		{"media server type", "media_servers: [{type: emby, url: 'http://x'}]\ncategories:\n" + cat("a", "flat"), "jellyfin or plex"},
		{"jellyfin without key", "media_servers: [{type: jellyfin, url: 'http://x'}]\ncategories:\n" + cat("a", "flat"), "api_key"},
		{"plex without token", "media_servers: [{type: plex, url: 'http://x'}]\ncategories:\n" + cat("a", "flat"), "token"},
		{"media server bad url", "media_servers: [{type: plex, token: t, url: 'ftp://x'}]\ncategories:\n" + cat("a", "flat"), "http(s) URL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestParseReportsAllProblemsTogether(t *testing.T) {
	_, err := Parse([]byte("server: {port: 0}\ndownloads: {concurrency: 0}\n"))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, s := range []string{"server.port", "concurrency", "at least one category"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("missing %q in %v", s, err)
		}
	}
}

func TestParseEmptyDocument(t *testing.T) {
	_, err := Parse(nil)
	if err == nil || !strings.Contains(err.Error(), "at least one category") {
		t.Errorf("err = %v", err)
	}
}

func TestEnvironmentValidation(t *testing.T) {
	t.Run("category path missing", func(t *testing.T) {
		f := newFixture(t)
		mustDo(t, os.Remove(f.tv))
		_, err := LoadWithOptions(write(t, f.root, f.yaml("")), Options{LookPath: okLook})
		if err == nil || !strings.Contains(err.Error(), "does not exist") || !strings.Contains(err.Error(), "(tv)") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("category path is a file", func(t *testing.T) {
		f := newFixture(t)
		mustDo(t, os.Remove(f.tv))
		mustDo(t, os.WriteFile(f.tv, nil, 0o644))
		_, err := LoadWithOptions(write(t, f.root, f.yaml("")), Options{LookPath: okLook})
		if err == nil || !strings.Contains(err.Error(), "not a directory") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("category path not writable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores permissions")
		}
		f := newFixture(t)
		mustDo(t, os.Chmod(f.movies, 0o555))
		t.Cleanup(func() { _ = os.Chmod(f.movies, 0o755) })
		_, err := LoadWithOptions(write(t, f.root, f.yaml("")), Options{LookPath: okLook})
		if err == nil || !strings.Contains(err.Error(), "not writable") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("data_dir missing", func(t *testing.T) {
		f := newFixture(t)
		mustDo(t, os.Remove(f.data))
		_, err := LoadWithOptions(write(t, f.root, f.yaml("")), Options{LookPath: okLook})
		if err == nil || !strings.Contains(err.Error(), "data_dir") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("data_dir not writable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores permissions")
		}
		f := newFixture(t)
		mustDo(t, os.Chmod(f.data, 0o555))
		t.Cleanup(func() { _ = os.Chmod(f.data, 0o755) })
		_, err := LoadWithOptions(write(t, f.root, f.yaml("")), Options{LookPath: okLook})
		if err == nil || !strings.Contains(err.Error(), "data_dir") || !strings.Contains(err.Error(), "not writable") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("ffmpeg and ffprobe missing", func(t *testing.T) {
		f := newFixture(t)
		missing := func(string) (string, error) { return "", errors.New("not found") }
		_, err := LoadWithOptions(write(t, f.root, f.yaml("")), Options{LookPath: missing})
		if err == nil || !strings.Contains(err.Error(), "ffmpeg not found") || !strings.Contains(err.Error(), "ffprobe not found") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("only ffprobe missing", func(t *testing.T) {
		f := newFixture(t)
		look := func(name string) (string, error) {
			if name == "ffprobe" {
				return "", errors.New("nope")
			}
			return "/bin/true", nil
		}
		_, err := LoadWithOptions(write(t, f.root, f.yaml("")), Options{LookPath: look})
		if err == nil || strings.Contains(err.Error(), "ffmpeg not found") || !strings.Contains(err.Error(), "ffprobe not found") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("configured ytdlp binary missing", func(t *testing.T) {
		f := newFixture(t)
		look := func(name string) (string, error) {
			if name == "/nonexistent/yt-dlp" {
				return "", errors.New("no such file")
			}
			return "/bin/true", nil
		}
		_, err := LoadWithOptions(write(t, f.root, f.yaml("ytdlp: {binary: /nonexistent/yt-dlp}\n")), Options{LookPath: look})
		if err == nil || !strings.Contains(err.Error(), "ytdlp.binary") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("configured ytdlp binary present", func(t *testing.T) {
		f := newFixture(t)
		bin := filepath.Join(f.root, "yt-dlp")
		mustDo(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))
		look := func(name string) (string, error) {
			if name == bin {
				return bin, nil
			}
			return "/bin/true", nil
		}
		cfg, err := LoadWithOptions(write(t, f.root, f.yaml("ytdlp: {binary: "+bin+"}\n")), Options{LookPath: look})
		if err != nil || cfg.YtdlpManaged() {
			t.Fatalf("cfg=%+v err=%v", cfg, err)
		}
	})
	t.Run("ytdlp binary is a directory", func(t *testing.T) {
		f := newFixture(t)
		look := func(string) (string, error) { return f.root, nil }
		_, err := LoadWithOptions(write(t, f.root, f.yaml("ytdlp: {binary: "+f.root+"}\n")), Options{LookPath: look})
		if err == nil || !strings.Contains(err.Error(), "not a file") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("multiple environment errors reported together", func(t *testing.T) {
		f := newFixture(t)
		mustDo(t, os.Remove(f.tv))
		mustDo(t, os.Remove(f.movies))
		missing := func(string) (string, error) { return "", errors.New("x") }
		_, err := LoadWithOptions(write(t, f.root, f.yaml("")), Options{LookPath: missing})
		if err == nil {
			t.Fatal("expected error")
		}
		for _, s := range []string{"(movies)", "(tv)", "ffmpeg", "ffprobe"} {
			if !strings.Contains(err.Error(), s) {
				t.Errorf("missing %q in %v", s, err)
			}
		}
	})
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil || !strings.Contains(err.Error(), "read config") {
		t.Errorf("err = %v", err)
	}
}

func TestAddrIPv6(t *testing.T) {
	c := Default()
	c.Server.Host = "::1"
	if c.Addr() != "[::1]:8080" {
		t.Errorf("Addr = %q", c.Addr())
	}
}

func TestResolvePath(t *testing.T) {
	t.Setenv("FETCHARR_CONFIG", "")
	if got := ResolvePath(""); got != "./config.yaml" {
		t.Errorf("default: %q", got)
	}
	t.Setenv("FETCHARR_CONFIG", "/etc/f.yaml")
	if got := ResolvePath(""); got != "/etc/f.yaml" {
		t.Errorf("env: %q", got)
	}
	if got := ResolvePath("/flag.yaml"); got != "/flag.yaml" {
		t.Errorf("flag: %q", got)
	}
}

func TestMakeAbsolute(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	c := Default()
	c.DataDir = "data"
	c.Categories = []Category{{ID: "misc", Path: "media/misc"}}
	c.Ytdlp.Binary = "./bin/yt-dlp"
	if err := c.MakeAbsolute(); err != nil {
		t.Fatal(err)
	}
	if c.DataDir != filepath.Join(cwd, "data") || c.Categories[0].Path != filepath.Join(cwd, "media/misc") ||
		c.Ytdlp.Binary != filepath.Join(cwd, "bin/yt-dlp") {
		t.Errorf("not absolute: %q %q %q", c.DataDir, c.Categories[0].Path, c.Ytdlp.Binary)
	}
	if got := c.JobWorkDir("j1"); !filepath.IsAbs(got) {
		t.Errorf("JobWorkDir = %q", got)
	}
	// A bare binary name is left for the PATH lookup; empty stays empty.
	c = Default()
	c.Ytdlp.Binary = "yt-dlp"
	if err := c.MakeAbsolute(); err != nil || c.Ytdlp.Binary != "yt-dlp" {
		t.Errorf("bare name changed: %q %v", c.Ytdlp.Binary, err)
	}
	c.Ytdlp.Binary = ""
	if err := c.MakeAbsolute(); err != nil || c.Ytdlp.Binary != "" {
		t.Errorf("empty binary changed: %q %v", c.Ytdlp.Binary, err)
	}
}
