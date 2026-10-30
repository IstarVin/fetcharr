package resolve

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeHosts answers every request from the saved pages, so the whole chain
// runs without the network.
type fakeHosts struct {
	t        *testing.T
	referers map[string]string
	pages    map[string]string // host -> page
}

func (f *fakeHosts) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	f.referers[host] = req.Header.Get("Referer")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Request: req, Body: io.NopCloser(strings.NewReader(""))}
	switch host {
	case "pixel.hubcloud.ist":
		resp.StatusCode = http.StatusFound
		resp.Header.Set("Location", "https://pixel.abc.workers.dev/?id=1")
	case "pixel.abc.workers.dev":
		resp.StatusCode = http.StatusFound
		resp.Header.Set("Location", "https://cryptonewz.online/dl.php?link=https://video-downloads.googleusercontent.com/file123")
	default:
		page, ok := f.pages[host]
		if !ok {
			resp.StatusCode = http.StatusNotFound
		} else {
			resp.Body = io.NopCloser(strings.NewReader(page))
		}
	}
	return resp, nil
}

func newFake(t *testing.T) (*Resolver, *fakeHosts) {
	f := &fakeHosts{t: t, referers: map[string]string{}, pages: map[string]string{
		"greenmotors.club": fixture(t, "redirect-greenmotors.html"),
		"hubdrive.bar":     fixture(t, "hubdrive-file.html"),
		"hubcloud.ist":     fixture(t, "hubcloud-drive.html"),
		"gamerxyt.com":     fixture(t, "hubcloud-servers.html"),
	}}
	return New(&http.Client{Transport: f}), f
}

func TestMatches(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://greenmotors.club/?id=abc":       true,
		"https://greenmotors.cc/?id=abc":         true,
		"https://HubCloud.foo/drive/x":           true,
		"https://hubdrive.bar/file/1":            true,
		"https://cdn.example/movie.mkv":          false,
		"https://example.com/hubcloud.ist/drive": false,
		"::bad":                                  false,
	} {
		if got := Matches(raw); got != want {
			t.Errorf("Matches(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestDecodeRedirect(t *testing.T) {
	got, ok := DecodeRedirect(fixture(t, "redirect-greenmotors.html"))
	if !ok || got != "https://hubcloud.ist/drive/0lymmyaxdyc15gy" {
		t.Fatalf("DecodeRedirect = %q, %v", got, ok)
	}
	if _, ok := DecodeRedirect("<html>nothing</html>"); ok {
		t.Fatal("decoded a page without payload")
	}
}

func TestPageParsers(t *testing.T) {
	if got := HubDriveTarget(fixture(t, "hubdrive-file.html")); got != "https://hubcloud.ist/drive/0lymmyaxdyc15gy" {
		t.Errorf("HubDriveTarget = %q", got)
	}
	if got := HubCloudNext(fixture(t, "hubcloud-drive.html")); !strings.HasPrefix(got, "https://gamerxyt.com/hubcloud.php?host=hubcloud&id=0lymmyaxdyc15gy&token=") {
		t.Errorf("HubCloudNext = %q", got)
	}
	var names []string
	for _, m := range Mirrors(fixture(t, "hubcloud-servers.html")) {
		names = append(names, m.Server)
	}
	if strings.Join(names, ",") != "FSL Server,Server : 10Gbps,Buzz Server" {
		t.Errorf("Mirrors = %v", names)
	}
}

func TestResolveChain(t *testing.T) {
	for _, start := range []string{
		"https://greenmotors.club/?id=Q1dxTzFn",
		"https://hubdrive.bar/file/8049839000",
		"https://hubcloud.ist/drive/0lymmyaxdyc15gy",
	} {
		r, f := newFake(t)
		got, err := r.Resolve(context.Background(), start)
		if err != nil {
			t.Fatalf("%s: %v", start, err)
		}
		if len(got) != 2 || got[0].Server != "FSL" || got[1].Server != "10Gbps" {
			t.Fatalf("%s: sources = %+v", start, got)
		}
		if !strings.Contains(got[0].URL, ".r2.cloudflarestorage.com/hub/") || !got[0].Resumable {
			t.Errorf("FSL source = %+v", got[0])
		}
		if got[1].URL != "https://video-downloads.googleusercontent.com/file123" || got[1].Resumable {
			t.Errorf("10Gbps source = %+v", got[1])
		}
		if f.referers["gamerxyt.com"] != "https://hubcloud.ist/drive/0lymmyaxdyc15gy" {
			t.Errorf("server list Referer = %q", f.referers["gamerxyt.com"])
		}
	}
}

func TestResolveErrorsAreRetryableAndURLFree(t *testing.T) {
	r, _ := newFake(t)
	_, err := r.Resolve(context.Background(), "https://greenmotors.gone/?id=secret")
	if !IsRetryable(err) {
		t.Fatalf("err = %v, want retryable", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "?id=") {
		t.Errorf("error leaks the link: %v", err)
	}
}

func TestDLLink(t *testing.T) {
	if got, ok := dlLink("https://x/dl.php?link=https%3A%2F%2Fg.example%2Ff"); !ok || got != "https://g.example/f" {
		t.Errorf("escaped = %q %v", got, ok)
	}
	if _, ok := dlLink("https://x/other"); ok {
		t.Error("matched a URL without link=")
	}
}
