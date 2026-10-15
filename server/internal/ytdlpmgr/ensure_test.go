package ytdlpmgr_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/istarvin/fetcharr/server/internal/ytdlp"
	"github.com/istarvin/fetcharr/server/internal/ytdlpmgr"
)

func TestAssetName(t *testing.T) {
	tests := []struct {
		goos, goarch string
		musl         bool
		want         string
	}{
		{"linux", "amd64", false, "yt-dlp_linux"},
		{"linux", "arm64", false, "yt-dlp_linux_aarch64"},
		{"linux", "arm", false, "yt-dlp_linux_armv7l"},
		{"linux", "amd64", true, "yt-dlp_musllinux"},
		{"linux", "arm64", true, "yt-dlp_musllinux_aarch64"},
		{"darwin", "arm64", false, "yt-dlp_macos"},
		{"linux", "riscv64", false, "yt-dlp"}, // python zipapp
		{"freebsd", "amd64", false, "yt-dlp"},
	}
	for _, tc := range tests {
		if got := ytdlpmgr.AssetName(tc.goos, tc.goarch, tc.musl); got != tc.want {
			t.Errorf("AssetName(%s,%s,musl=%v) = %q, want %q", tc.goos, tc.goarch, tc.musl, got, tc.want)
		}
	}
}

// releases is a fake GitHub "latest/download" endpoint.
type releases struct {
	srv  *httptest.Server
	mu   sync.Mutex
	hits []string
	body []byte
	code int
}

func newReleases(t *testing.T, body []byte) *releases {
	t.Helper()
	r := &releases{body: body, code: http.StatusOK}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.hits = append(r.hits, req.URL.Path)
		code, b := r.code, r.body
		r.mu.Unlock()
		if code != http.StatusOK {
			http.Error(w, "nope", code)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *releases) requested() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.hits...)
}

func newManager(t *testing.T, base string, goarch string) (*ytdlpmgr.Manager, string) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin", "yt-dlp")
	musl := false
	return ytdlpmgr.New(ytdlpmgr.Options{
		Binary: bin, BaseURL: base, Logger: slog.New(slog.DiscardHandler),
		GOOS: "linux", GOARCH: goarch, Musl: &musl,
	}), bin
}

func TestEnsureDownloadsCorrectAssetAtomically(t *testing.T) {
	body, err := os.ReadFile(fakeYtdlpPath(t))
	if err != nil {
		t.Fatal(err)
	}
	for arch, asset := range map[string]string{"amd64": "/yt-dlp_linux", "arm64": "/yt-dlp_linux_aarch64"} {
		t.Run(arch, func(t *testing.T) {
			rel := newReleases(t, body)
			m, bin := newManager(t, rel.srv.URL, arch)
			if err := m.Ensure(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := rel.requested(); len(got) != 1 || got[0] != asset {
				t.Errorf("requested %v, want [%s]", got, asset)
			}
			st, err := os.Stat(bin)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != 0o755 {
				t.Errorf("mode = %o, want 755", st.Mode().Perm())
			}
			if v, err := ytdlp.Version(t.Context(), bin); err != nil || v != "2099.01.01" {
				t.Errorf("Version = %q, %v", v, err)
			}
			entries, _ := os.ReadDir(filepath.Dir(bin))
			if len(entries) != 1 {
				t.Errorf("bin dir has %d entries, want only the binary: %v", len(entries), entries)
			}
			// A second start finds the binary and downloads nothing.
			if err := m.Ensure(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := rel.requested(); len(got) != 1 {
				t.Errorf("second Ensure downloaded again: %v", got)
			}
		})
	}
}

func TestEnsureFailuresLeaveNoBinary(t *testing.T) {
	tests := []struct {
		name string
		code int
		body string
	}{
		{"http error", http.StatusNotFound, ""},
		{"not an executable program", http.StatusOK, "<html>rate limited</html>"},
		{"empty", http.StatusOK, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rel := newReleases(t, []byte(tc.body))
			rel.code = tc.code
			m, bin := newManager(t, rel.srv.URL, "amd64")
			if err := m.Ensure(context.Background()); err == nil {
				t.Fatal("Ensure succeeded")
			}
			if _, err := os.Stat(bin); !os.IsNotExist(err) {
				t.Errorf("binary exists after a failed download: %v", err)
			}
			if entries, _ := os.ReadDir(filepath.Dir(bin)); len(entries) != 0 {
				t.Errorf("temporary files left behind: %v", entries)
			}
		})
	}
}

func TestEnsureKeepsExistingBinaryAndRemovesStaleTemps(t *testing.T) {
	rel := newReleases(t, []byte("x"))
	m, bin := newManager(t, rel.srv.URL, "amd64")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("existing"), 0o755); err != nil { //nolint:gosec // test file
		t.Fatal(err)
	}
	stale := filepath.Join(filepath.Dir(bin), ".yt-dlp-download-123")
	if err := os.WriteFile(stale, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "existing" {
		t.Errorf("existing binary was replaced: %q", b)
	}
	if len(rel.requested()) != 0 {
		t.Errorf("unexpected download: %v", rel.requested())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale temp file not removed: %v", err)
	}
}
