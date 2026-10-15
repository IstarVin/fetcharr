package mediaserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
)

const (
	testKey   = "jf-secret-api-key"
	testToken = "plex-secret-token"
)

func TestPathMap(t *testing.T) {
	m := newPathMap(map[string]string{
		"/media":     "/data/media",
		"/media/tv":  "/mnt/tv/",
		"/other/":    "/o",
		"/":          "",
		"/win":       `D:\lib`,
		"/media/ünï": "/uni",
		"/exact":     "/x",
	})
	tests := []struct{ in, want string }{
		{"/media/movies/Heat (1995)", "/data/media/movies/Heat (1995)"},
		{"/media", "/data/media"},
		{"/media/tv", "/mnt/tv"},
		{"/media/tv/Show", "/mnt/tv/Show"},                  // longest prefix wins
		{"/media/tvshows/Show", "/data/media/tvshows/Show"}, // not a segment boundary for /media/tv
		{"/mediax/a", "/mediax/a"},                          // "/media" must not match "/mediax"
		{"/other/a/b", "/o/a/b"},                            // trailing slash in the key
		{"/win/a/b", `D:\lib\a\b`},                          // windows media server
		{"/media/ünï/x", "/uni/x"},
		{"/exact", "/x"},
		{"/unmapped/a", "/unmapped/a"},
		{"relative/a", "relative/a"},
	}
	for _, tc := range tests {
		if got := m.apply(tc.in); got != tc.want {
			t.Errorf("apply(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// Root rewrite: "/" maps everything.
	root := newPathMap(map[string]string{"/": "/mnt"})
	if got := root.apply("/a/b"); got != "/mnt/a/b" {
		t.Errorf("root map: %q", got)
	}
	if got := newPathMap(nil).apply("/a"); got != "/a" {
		t.Errorf("nil map: %q", got)
	}
}

type request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   string
}

// fake records every request and answers through handle.
type fake struct {
	srv  *httptest.Server
	mu   sync.Mutex
	reqs []request
}

func newFake(t *testing.T, handle http.HandlerFunc) *fake {
	t.Helper()
	f := &fake{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, request{r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone(), string(b)})
		f.mu.Unlock()
		handle(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) requests() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]request(nil), f.reqs...)
}

// refreshes returns the requests that are refreshes (not section listings).
func (f *fake) refreshes() []request {
	var out []request
	for _, r := range f.requests() {
		if r.Path != "/library/sections" {
			out = append(out, r)
		}
	}
	return out
}

func jellyfinFake(t *testing.T) *fake {
	return newFake(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
}

const plexSections = `{"MediaContainer":{"size":3,"Directory":[
 {"key":"1","title":"Movies","Location":[{"id":1,"path":"/data/movies"}]},
 {"key":"2","title":"TV","Location":[{"id":2,"path":"/data/tv"},{"id":3,"path":"/data/tv-extra/"}]},
 {"key":"9","title":"Everything","Location":[{"id":9,"path":"/data"}]}
]}}`

func plexFake(t *testing.T) *fake {
	return newFake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/library/sections" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, plexSections)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

type syncBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newRefresher(t *testing.T, servers []config.MediaServer, mod func(*Options)) (*Refresher, *syncBuf) {
	t.Helper()
	logs := &syncBuf{}
	o := Options{
		Servers: servers, Debounce: 50 * time.Millisecond, Timeout: 5 * time.Second,
		Logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
	if mod != nil {
		mod(&o)
	}
	r := New(o)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.Close(ctx)
	})
	return r, logs
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func jellyfinCfg(f *fake, pm map[string]string) config.MediaServer {
	return config.MediaServer{Type: config.MediaServerJellyfin, URL: f.srv.URL + "/", APIKey: testKey, PathMap: pm}
}

func plexCfg(f *fake, pm map[string]string) config.MediaServer {
	return config.MediaServer{Type: config.MediaServerPlex, URL: f.srv.URL, Token: testToken, PathMap: pm}
}

func TestJellyfinRefresh(t *testing.T) {
	f := jellyfinFake(t)
	r, logs := newRefresher(t, []config.MediaServer{jellyfinCfg(f, map[string]string{"/media": "/data/media"})}, nil)
	r.Queue("/media/tv/Some Show/")
	eventually(t, "refresh", func() bool { return len(f.requests()) == 1 })
	req := f.requests()[0]
	if req.Method != http.MethodPost || req.Path != "/Library/Media/Updated" {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	if req.Header.Get("X-Emby-Token") != testKey {
		t.Errorf("X-Emby-Token = %q", req.Header.Get("X-Emby-Token"))
	}
	if got := req.Header.Get("Authorization"); got != `MediaBrowser Token="`+testKey+`"` {
		t.Errorf("Authorization = %q", got)
	}
	if ct := req.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
	if req.Query.Has("api_key") {
		t.Error("the api key must not be in the URL")
	}
	var body struct {
		Updates []struct{ Path, UpdateType string }
	}
	if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
		t.Fatalf("body %q: %v", req.Body, err)
	}
	if len(body.Updates) != 1 || body.Updates[0].Path != "/data/media/tv/Some Show" || body.Updates[0].UpdateType != "Modified" {
		t.Errorf("body = %s", req.Body)
	}
	eventually(t, "log", func() bool { return strings.Contains(logs.String(), "media server refresh requested") })
	if strings.Contains(logs.String(), testKey) {
		t.Error("the api key was logged")
	}
}

func TestPlexRefresh(t *testing.T) {
	f := plexFake(t)
	r, logs := newRefresher(t, []config.MediaServer{plexCfg(f, map[string]string{"/media": "/data"})}, nil)
	r.Queue("/media/tv/Show & Co")
	eventually(t, "refresh", func() bool { return len(f.refreshes()) == 1 })
	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %+v", reqs)
	}
	list, ref := reqs[0], reqs[1]
	if list.Method != http.MethodGet || list.Path != "/library/sections" || list.Header.Get("X-Plex-Token") != testToken {
		t.Errorf("section listing = %+v", list)
	}
	if list.Header.Get("Accept") != "application/json" {
		t.Errorf("Accept = %q", list.Header.Get("Accept"))
	}
	// Section 2 (/data/tv) is the longest location containing the folder; 9
	// (/data) also contains it.
	if ref.Method != http.MethodGet || ref.Path != "/library/sections/2/refresh" {
		t.Errorf("refresh = %s %s", ref.Method, ref.Path)
	}
	if got := ref.Query.Get("path"); got != "/data/tv/Show & Co" {
		t.Errorf("path query = %q", got)
	}
	if ref.Header.Get("X-Plex-Token") != testToken {
		t.Errorf("X-Plex-Token = %q", ref.Header.Get("X-Plex-Token"))
	}
	if ref.Query.Has("X-Plex-Token") {
		t.Error("the token must not be in the URL")
	}
	eventually(t, "log", func() bool { return strings.Contains(logs.String(), "media server refresh requested") })
	if strings.Contains(logs.String(), testToken) {
		t.Error("the token was logged")
	}
}

func TestPlexSectionSelection(t *testing.T) {
	f := plexFake(t)
	r, _ := newRefresher(t, []config.MediaServer{plexCfg(f, nil)}, nil)
	for folder, section := range map[string]string{
		"/data/movies":        "1",
		"/data/movies/Heat":   "1",
		"/data/tv-extra/Show": "2", // location with a trailing slash
		"/data/other":         "9",
		"/data/moviesX/Heat":  "9", // "/data/movies" is not a prefix at a segment boundary
	} {
		before := len(f.refreshes())
		r.Queue(folder)
		eventually(t, folder, func() bool { return len(f.refreshes()) == before+1 })
		got := f.refreshes()[before].Path
		if want := "/library/sections/" + section + "/refresh"; got != want {
			t.Errorf("%s: refreshed %s, want %s", folder, got, want)
		}
	}
}

func TestPlexNoMatchingSectionIsLoggedAndIgnored(t *testing.T) {
	f := plexFake(t)
	r, logs := newRefresher(t, []config.MediaServer{plexCfg(f, nil)}, nil)
	r.Queue("/elsewhere/show")
	eventually(t, "log", func() bool { return strings.Contains(logs.String(), "media server refresh failed") })
	if len(f.refreshes()) != 0 {
		t.Errorf("refreshed without a section: %+v", f.refreshes())
	}
	if !strings.Contains(logs.String(), "no plex library section") || strings.Contains(logs.String(), testToken) {
		t.Errorf("logs: %s", logs.String())
	}
}

func TestFailuresAreLoggedWithoutSecrets(t *testing.T) {
	boom := newFake(t, func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) })
	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close() // connection refused
	servers := []config.MediaServer{
		jellyfinCfg(boom, nil),
		plexCfg(boom, nil),
		{Type: config.MediaServerJellyfin, URL: deadURL, APIKey: testKey},
		{Type: "emby", URL: deadURL},
	}
	r, logs := newRefresher(t, servers, nil)
	r.Queue("/media/movies/Heat")
	eventually(t, "three failures", func() bool { return strings.Count(logs.String(), "media server refresh failed") == 3 })
	out := logs.String()
	for _, secret := range []string{testKey, testToken} {
		if strings.Contains(out, secret) {
			t.Errorf("logs contain %q:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "unknown type") {
		t.Errorf("unknown server type not reported:\n%s", out)
	}
}

func TestDebounceCoalescesPerFolder(t *testing.T) {
	f := jellyfinFake(t)
	r, _ := newRefresher(t, []config.MediaServer{jellyfinCfg(f, nil)}, nil)
	// A season of completions in one folder, and one in another.
	for range 6 {
		r.Queue("/media/tv/Show A")
		time.Sleep(5 * time.Millisecond)
	}
	r.Queue("/media/tv/Show B")
	r.Queue("/media/tv/Show B/") // same folder after cleaning
	eventually(t, "two refreshes", func() bool { return len(f.requests()) == 2 })
	time.Sleep(200 * time.Millisecond) // nothing more may arrive
	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d refreshes, want 2 (one per folder): %+v", len(reqs), reqs)
	}
	paths := map[string]bool{}
	for _, q := range reqs {
		var b struct{ Updates []struct{ Path string } }
		if err := json.Unmarshal([]byte(q.Body), &b); err != nil {
			t.Fatal(err)
		}
		paths[b.Updates[0].Path] = true
	}
	if !paths["/media/tv/Show A"] || !paths["/media/tv/Show B"] {
		t.Errorf("paths = %v", paths)
	}
	// After the window a new completion starts a new one.
	r.Queue("/media/tv/Show A")
	eventually(t, "third refresh", func() bool { return len(f.requests()) == 3 })
}

func TestDebounceRestartsOnRepeat(t *testing.T) {
	f := jellyfinFake(t)
	r, _ := newRefresher(t, []config.MediaServer{jellyfinCfg(f, nil)}, func(o *Options) { o.Debounce = 400 * time.Millisecond })
	r.Queue("/media/movies")
	time.Sleep(250 * time.Millisecond)
	r.Queue("/media/movies") // restarts the window: fires at ~650 ms, not 400 ms
	time.Sleep(250 * time.Millisecond)
	if n := len(f.requests()); n != 0 {
		t.Fatalf("refreshed after 500 ms (%d requests); the repeat should restart the window", n)
	}
	eventually(t, "refresh", func() bool { return len(f.requests()) == 1 })
}

func TestMaxWaitBoundsPostponement(t *testing.T) {
	f := jellyfinFake(t)
	r, _ := newRefresher(t, []config.MediaServer{jellyfinCfg(f, nil)}, func(o *Options) {
		o.Debounce, o.MaxWait = 200*time.Millisecond, 400*time.Millisecond
	})
	deadline := time.Now().Add(3 * time.Second)
	for len(f.requests()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a steady stream of completions postponed the refresh forever")
		}
		r.Queue("/media/movies") // every 50 ms, well inside the window
		time.Sleep(50 * time.Millisecond)
	}
}

func TestEveryServerIsNotified(t *testing.T) {
	jf, px := jellyfinFake(t), plexFake(t)
	r, _ := newRefresher(t, []config.MediaServer{jellyfinCfg(jf, nil), plexCfg(px, nil)}, nil)
	if !r.Enabled() {
		t.Fatal("not enabled")
	}
	r.Queue("/data/tv/Show")
	eventually(t, "both", func() bool { return len(jf.requests()) == 1 && len(px.refreshes()) == 1 })
}

func TestNoServersIsANoop(t *testing.T) {
	r, _ := newRefresher(t, nil, nil)
	if r.Enabled() {
		t.Error("enabled without servers")
	}
	r.Queue("/media/x")
	r.OnCompleted(nil, "/media/x")
	r.mu.Lock()
	n := len(r.pending)
	r.mu.Unlock()
	if n != 0 {
		t.Errorf("pending = %d", n)
	}
}

func TestCloseFlushesPendingRefreshes(t *testing.T) {
	f := jellyfinFake(t)
	r, _ := newRefresher(t, []config.MediaServer{jellyfinCfg(f, nil)}, func(o *Options) { o.Debounce = time.Hour })
	r.Queue("/media/a")
	r.OnCompleted(nil, "/media/b")
	r.Queue("")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r.Close(ctx)
	if n := len(f.requests()); n != 2 {
		t.Fatalf("Close sent %d refreshes, want the 2 pending ones", n)
	}
	r.Queue("/media/c") // ignored after Close
	r.Close(ctx)        // idempotent
	time.Sleep(50 * time.Millisecond)
	if n := len(f.requests()); n != 2 {
		t.Errorf("%d requests after Close", n)
	}
}

func TestCloseAbortsSlowServerWhenContextExpires(t *testing.T) {
	release := make(chan struct{})
	f := newFake(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	defer close(release)
	r, _ := newRefresher(t, []config.MediaServer{jellyfinCfg(f, nil)}, func(o *Options) { o.Debounce = time.Hour })
	r.Queue("/media/a")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	r.Close(ctx)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Close took %v", d)
	}
}
