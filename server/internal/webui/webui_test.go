package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func get(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target, nil))
	return rec
}

func TestServesBuiltApp(t *testing.T) {
	h := New(fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>Fetcharr</title>")},
		"assets/index-abc.js":  {Data: []byte("console.log(1)")},
		"assets/index-abc.css": {Data: []byte("body{}")},
		"favicon.svg":          {Data: []byte("<svg/>")},
	})

	for _, p := range []string{"/", "/index.html"} {
		r := get(t, h, "GET", p)
		if r.Code != 200 || !strings.Contains(r.Body.String(), "<title>Fetcharr") {
			t.Fatalf("%s: %d %q", p, r.Code, r.Body)
		}
		if got := r.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", p, got)
		}
		if !strings.HasPrefix(r.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s: Content-Type = %q", p, r.Header().Get("Content-Type"))
		}
		if !strings.Contains(r.Header().Get("Content-Security-Policy"), "default-src 'self'") {
			t.Errorf("%s: CSP = %q", p, r.Header().Get("Content-Security-Policy"))
		}
		if r.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", p)
		}
	}

	r := get(t, h, "GET", "/assets/index-abc.js")
	if r.Code != 200 || r.Body.String() != "console.log(1)" {
		t.Fatalf("asset: %d %q", r.Code, r.Body)
	}
	if got := r.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("asset Cache-Control = %q", got)
	}
	if ct := r.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("asset Content-Type = %q", ct)
	}

	if r := get(t, h, "GET", "/favicon.svg"); r.Code != 200 || r.Header().Get("Cache-Control") != "" {
		t.Errorf("favicon: %d %q", r.Code, r.Header().Get("Cache-Control"))
	}
	if r := get(t, h, "HEAD", "/"); r.Code != 200 || r.Body.Len() != 0 {
		t.Errorf("HEAD: %d %q", r.Code, r.Body)
	}
	for _, p := range []string{"/history", "/assets", "/assets/", "/assets/missing.js"} {
		if r := get(t, h, "GET", p); r.Code != 404 {
			t.Errorf("%s: %d", p, r.Code)
		}
	}
	if r := get(t, h, "POST", "/"); r.Code != 405 || r.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: %d %q", r.Code, r.Header().Get("Allow"))
	}
}

func TestNotBuilt(t *testing.T) {
	h := New(fstest.MapFS{})
	r := get(t, h, "GET", "/")
	if r.Code != 404 || !strings.Contains(r.Body.String(), "npm run build") {
		t.Fatalf("%d %q", r.Code, r.Body)
	}
	if r.Header().Get("Content-Security-Policy") == "" {
		t.Error("missing CSP")
	}
}

func TestEmbeddedHandler(t *testing.T) {
	// Works whether or not the frontend has been built into dist/app.
	r := get(t, Handler(), "GET", "/")
	if r.Code != 200 && r.Code != 404 {
		t.Fatalf("%d %q", r.Code, r.Body)
	}
}
