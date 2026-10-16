package api

import (
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/jobs"
	"github.com/istarvin/fetcharr/server/internal/store"
)

func seedMedia(t *testing.T, e *env) *store.Job {
	t.Helper()
	root := e.cfg.Categories[2].Path
	path := filepath.Join(root, "test video.mp4")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	j := newJob("https://example.com/video", jobs.StatusCompleted)
	j.DestinationRoot, j.FilePath = root, &path
	return e.seed(j)
}

func playbackPath(t *testing.T, e *env, id string) string {
	t.Helper()
	r := e.want(e.request("POST", "/api/jobs/"+id+"/playback", nil), 200)
	obj := r.Obj(t)
	if obj["mime_type"] != "video/mp4" || obj["expires_at"] == nil {
		t.Fatalf("playback metadata: %s", r.Raw)
	}
	return "/api/" + obj["url"].(string)
}

func TestPlaybackStreamAndSeeking(t *testing.T) {
	for _, auth := range []bool{false, true} {
		t.Run(map[bool]string{false: "no auth", true: "bearer auth"}[auth], func(t *testing.T) {
			e := newEnv(t, func(c *config.Config) {
				if !auth {
					c.Server.AuthToken = ""
				}
			})
			j := seedMedia(t, e)
			path := playbackPath(t, e, j.ID)
			if strings.Contains(path, testToken) {
				t.Fatal("link leaked API token")
			}
			r := e.want(e.requestWith("GET", path, nil, ""), 200)
			if string(r.Raw) != "0123456789" || r.Header.Get("Content-Type") != "video/mp4" || r.Header.Get("Accept-Ranges") != "bytes" {
				t.Fatalf("stream response: %+v", r)
			}
			head := e.want(e.requestWith("HEAD", path, nil, ""), 200)
			if len(head.Raw) != 0 || head.Header.Get("Content-Length") != "10" {
				t.Fatalf("HEAD response: %+v", head)
			}
			for _, c := range []struct {
				header, body, contentRange string
				status                     int
			}{
				{"bytes=3-6", "3456", "bytes 3-6/10", 206},
				{"bytes=-2", "89", "bytes 8-9/10", 206},
				{"bytes=20-", "", "bytes */10", 416},
			} {
				req := httptest.NewRequestWithContext(t.Context(), "GET", path, nil)
				req.Header.Set("Range", c.header)
				rec := httptest.NewRecorder()
				e.h.ServeHTTP(rec, req)
				if rec.Code != c.status || rec.Header().Get("Content-Range") != c.contentRange || (c.status == 206 && rec.Body.String() != c.body) {
					t.Errorf("range %s: %d %s %q", c.header, rec.Code, rec.Header(), rec.Body.String())
				}
			}
			if strings.Contains(e.logs.String(), "signature=") {
				t.Fatal("logs leaked playback signature")
			}
		})
	}
}

func TestPlaybackAuthorizationAndScope(t *testing.T) {
	e := newEnv(t)
	j := seedMedia(t, e)
	e.want(e.requestWith("POST", "/api/jobs/"+j.ID+"/playback", nil, ""), 401)
	e.want(e.requestWith("GET", "/api/jobs/"+j.ID+"/media", nil, ""), 401)
	path := playbackPath(t, e, j.ID)
	e.want(e.requestWith("GET", strings.Replace(path, j.ID, "different-job", 1), nil, ""), 401)
	parsed, _ := url.Parse(path)
	for _, exp := range []string{"0", "9999999999", "invalid"} {
		q := parsed.Query()
		q.Set("expires", exp)
		parsed.RawQuery = q.Encode()
		e.want(e.requestWith("GET", parsed.String(), nil, ""), 401)
	}
	// A playback signature cannot authorize job access or mint another link.
	query := strings.SplitN(path, "?", 2)[1]
	e.want(e.requestWith("GET", "/api/jobs/"+j.ID+"?"+query, nil, ""), 401)
	e.want(e.requestWith("POST", "/api/jobs/"+j.ID+"/playback?"+query, nil, ""), 401)
	// A restart rotates the signing key.
	e.h = New(Options{Config: e.cfg, Service: e.svc, Logger: e.logsLogger()})
	e.want(e.requestWith("GET", path, nil, ""), 401)
}

func TestPlaybackUnavailableFiles(t *testing.T) {
	for _, kind := range []string{"pending", "failed", "cancelled", "no-path", "no-root", "missing", "directory", "outside", "symlink", "parent-symlink"} {
		t.Run(kind, func(t *testing.T) {
			e := newEnv(t)
			root := e.cfg.Categories[2].Path
			path := filepath.Join(root, "video.mp4")
			j := newJob("https://example.com/video", jobs.StatusCompleted)
			j.FilePath, j.DestinationRoot = &path, root
			want := 404
			switch kind {
			case "pending", "failed", "cancelled":
				j.Status = jobs.Status(kind)
				want = 409
			case "no-path":
				j.FilePath = nil
				want = 409
			case "no-root":
				j.DestinationRoot = ""
				want = 400
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "outside", "symlink", "parent-symlink":
				outside := t.TempDir()
				secret := filepath.Join(outside, "secret.mp4")
				if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "outside":
					j.FilePath = &secret
					want = 400
				case "symlink":
					if err := os.Symlink(secret, path); err != nil {
						t.Fatal(err)
					}
				default:
					link := filepath.Join(root, "link")
					if err := os.Symlink(outside, link); err != nil {
						t.Fatal(err)
					}
					j.FilePath = store.Ptr(filepath.Join(link, "secret.mp4"))
				}
			}
			e.seed(j)
			e.want(e.request("POST", "/api/jobs/"+j.ID+"/playback", nil), want)
		})
	}
	e := newEnv(t)
	e.want(e.request("POST", "/api/jobs/missing/playback", nil), 404)
}

func TestPlaybackRechecksFileAndJob(t *testing.T) {
	e := newEnv(t)
	j := seedMedia(t, e)
	path := playbackPath(t, e, j.ID)
	if err := os.Remove(*j.FilePath); err != nil {
		t.Fatal(err)
	}
	e.want(e.requestWith("GET", path, nil, ""), 404)
	e.want(e.request("DELETE", "/api/jobs/"+j.ID, nil), 204)
	e.want(e.requestWith("GET", path, nil, ""), 404)
}

func TestExpiredPlaybackSignature(t *testing.T) {
	s := &server{}
	exp := strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
	path := "/api/jobs/job/media?expires=" + exp + "&signature=" + s.playbackSignature("job", exp)
	r := httptest.NewRequestWithContext(t.Context(), "GET", path, nil)
	r.SetPathValue("id", "job")
	w := httptest.NewRecorder()
	s.handleMedia(w, r)
	if w.Code != 401 {
		t.Fatalf("expired, correctly signed link: %d %s", w.Code, w.Body.String())
	}
}

type playbackDeadlineRecorder struct {
	*httptest.ResponseRecorder
	cleared bool
}

func (w *playbackDeadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.cleared = deadline.IsZero()
	return nil
}

func TestPlaybackClearsApiWriteTimeout(t *testing.T) {
	e := newEnv(t)
	j := seedMedia(t, e)
	path := playbackPath(t, e, j.ID)
	w := &playbackDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	e.h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
	if !w.cleared || w.Code != 200 {
		t.Fatalf("long streams must clear the API write deadline: cleared=%v status=%d", w.cleared, w.Code)
	}
}
