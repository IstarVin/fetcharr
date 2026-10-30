package resolve

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestLive resolves a real link: FETCHARR_LIVE_RESOLVE=<redirector or HubCloud URL>.
func TestLive(t *testing.T) {
	link := os.Getenv("FETCHARR_LIVE_RESOLVE")
	if link == "" {
		t.Skip("set FETCHARR_LIVE_RESOLVE to run against the real hosts")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	sources, err := New(nil).Resolve(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sources {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
		req.Header.Set("Range", "bytes=0-15")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("%s: unreachable", s.Server)
			continue
		}
		_ = resp.Body.Close()
		t.Logf("%s: HTTP %d, %s, Content-Range %q", s.Server, resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Range"))
	}
}
