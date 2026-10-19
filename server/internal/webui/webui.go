// Package webui serves the browser frontend built from web/ (see
// docs/fetcharr-web.md). The Vite build writes into dist/app, which is
// embedded into the binary. The tracked dist/.gitkeep keeps the embed valid
// when the frontend has not been built; the handler then serves a short
// notice instead of the app.
package webui

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// contentSecurityPolicy allows only the app's own scripts, styles, API,
// WebSocket and signed media links. Inline styles are allowed for the style
// attributes Svelte sets (progress widths, transitions).
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; connect-src 'self'; " +
	"media-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
	"object-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

const notBuilt = `<!doctype html>
<meta charset="utf-8">
<title>Fetcharr</title>
<p>The Fetcharr web UI is not part of this build. Run <code>npm ci &amp;&amp; npm run build</code> in <code>web/</code>, then rebuild the server.</p>
`

// Handler returns the handler for the embedded frontend.
func Handler() http.Handler {
	sub, err := fs.Sub(dist, "dist/app")
	if err != nil {
		panic(err) // "dist/app" is a valid static path
	}
	return New(sub)
}

// New serves the frontend from files: GET/HEAD only, "/" is index.html,
// everything else must be an existing file. The app uses hash routing, so
// there is no history fallback.
func New(files fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "index.html" {
			name = "index.html"
			h.Set("Cache-Control", "no-cache")
		} else if strings.HasPrefix(name, "assets/") {
			// Vite puts a content hash in every asset file name.
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		st, err := fs.Stat(files, name)
		switch {
		case err == nil && !st.IsDir():
		case name == "index.html" && errors.Is(err, fs.ErrNotExist):
			h.Set("Content-Type", "text/html; charset=utf-8")
			h.Del("Cache-Control")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(notBuilt))
			return
		default:
			h.Del("Cache-Control")
			http.NotFound(w, r)
			return
		}
		// Clear the path so ServeFileFS never redirects ".../index.html".
		rr := r.Clone(r.Context())
		rr.URL.Path = "/"
		http.ServeFileFS(w, rr, files, name)
	})
}
