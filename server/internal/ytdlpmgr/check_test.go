package ytdlpmgr_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLatestVersionDoesNotDownloadOrRunBinary(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		status       int
		want         string
	}{
		{"stable", "/yt-dlp/yt-dlp/releases/tag/2026.10.01", 200, "2026.10.01"},
		{"bad tag", "/yt-dlp/yt-dlp/releases/tag/latest", 200, ""},
		{"missing tag", "/unexpected", 200, ""},
		{"upstream failure", "/unavailable", 503, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodHead {
					t.Errorf("method = %s, want HEAD", r.Method)
				}
				if r.URL.Path == "/releases/latest" {
					http.Redirect(w, r, tc.target, http.StatusFound)
					return
				}
				w.WriteHeader(tc.status)
			}))
			defer upstream.Close()
			m, _ := newManager(t, upstream.URL+"/releases/latest/download", "amd64")
			// No binary is installed and no Runner exists: checking needs neither.
			got, err := m.LatestVersion(t.Context())
			if tc.want == "" {
				if err == nil {
					t.Fatal("expected error")
				}
			} else if err != nil || got != tc.want {
				t.Fatalf("LatestVersion = %q, %v; want %q", got, err, tc.want)
			}
			if requests != 2 {
				t.Errorf("requests = %d, want 2", requests)
			}
		})
	}
}
