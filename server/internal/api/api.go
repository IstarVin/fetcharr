package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/logsafe"
	"github.com/istarvin/fetcharr/server/internal/service"
	"github.com/istarvin/fetcharr/server/internal/settings"
)

// UpdateResult is the body of a successful POST /api/system/ytdlp/update.
type UpdateResult struct {
	Previous string `json:"previous"`
	Current  string `json:"current"`
}

// YtdlpCheckResult reports installed and latest stable versions without updating.
type YtdlpCheckResult struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
}

// YtdlpUpdateChecker checks the upstream stable release without changing the binary.
type YtdlpUpdateChecker interface {
	LatestVersion(ctx context.Context) (string, error)
}

// ErrYtdlpBusy is returned by a YtdlpUpdater when a job is downloading; the
// API maps it to 409 ytdlp_busy.
var ErrYtdlpBusy = errors.New("ytdlp busy: a job is downloading")

// YtdlpUpdater updates the managed yt-dlp binary right away (spec §8). It is
// called only in managed mode. Phase S5 provides the real implementation.
type YtdlpUpdater interface {
	Update(ctx context.Context) (UpdateResult, error)
}

type unimplementedUpdater struct{}

func (unimplementedUpdater) Update(context.Context) (UpdateResult, error) {
	return UpdateResult{}, errors.New("yt-dlp update is not implemented")
}

// Options configures New.
type Options struct {
	Settings *settings.Manager
	Config   *config.Config
	Service  *service.Service
	// Version is the server version reported by GET /api/info.
	Version string
	// Updater serves POST /api/system/ytdlp/update (default: always fails
	// with 500 internal until phase S5).
	Updater YtdlpUpdater
	// WebSocket, when set, serves GET /api/ws (phase S2). It is mounted behind
	// the same auth middleware.
	WebSocket http.Handler
	// Logger receives request logs (default slog.Default()).
	Logger *slog.Logger
	// MaxBodyBytes caps request bodies (default 4 MiB).
	MaxBodyBytes int64
	// YtdlpVersion overrides how the yt-dlp version is probed (tests). The
	// default runs `<binary> --version`.
	YtdlpVersion func(ctx context.Context, binary string) (string, error)
	// WebUI, when set, serves the browser frontend for every path outside
	// /api. It is static code and is mounted without authentication.
	WebUI http.Handler
	// Now overrides the clock used for session expiry (tests).
	Now func() time.Time
}

type server struct {
	settings    *settings.Manager
	cfg         *config.Config
	svc         *service.Service
	version     string
	updater     YtdlpUpdater
	log         *slog.Logger
	maxBody     int64
	ytdlp       *versionCache
	playbackKey [32]byte
	clock       func() time.Time
}

// New returns the HTTP handler for the whole API (everything lives under
// /api) and, when configured, the web UI. Middleware order, outermost first:
// request logging, panic recovery, cross-origin protection, routing and
// endpoint authentication.
func New(opts Options) http.Handler {
	s := &server{
		settings: opts.Settings,
		cfg:      opts.Config,
		svc:      opts.Service,
		version:  opts.Version,
		updater:  opts.Updater,
		log:      opts.Logger,
		maxBody:  opts.MaxBodyBytes,
		clock:    opts.Now,
	}
	if s.updater == nil {
		s.updater = unimplementedUpdater{}
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	if s.maxBody <= 0 {
		s.maxBody = maxBodyBytesDefault
	}
	s.ytdlp = newVersionCache(opts.YtdlpVersion)
	rand.Read(s.playbackKey[:])

	mux := http.NewServeMux()
	type route struct {
		method, path string
		h            http.HandlerFunc
	}
	routes := []route{
		{"GET", "/api/system/settings", s.handleGetSettings},
		{"PUT", "/api/system/settings", s.handleSaveSettings},
		{"GET", "/api/health", s.handleHealth},
		{"GET", "/api/info", s.handleInfo},
		{"POST", "/api/jobs", s.handleCreate},
		{"POST", "/api/jobs/batch", s.handleCreateBatch},
		{"GET", "/api/jobs", s.handleList},
		{"DELETE", "/api/jobs", s.handleDeleteMany},
		{"GET", "/api/jobs/{id}", s.handleGet},
		{"POST", "/api/jobs/{id}/playback", s.handlePlayback},
		{"DELETE", "/api/jobs/{id}", s.handleDelete},
		{"POST", "/api/jobs/{id}/pause", s.handleAction("pause")},
		{"POST", "/api/jobs/{id}/resume", s.handleAction("resume")},
		{"POST", "/api/jobs/{id}/cancel", s.handleAction("cancel")},
		{"POST", "/api/jobs/{id}/retry", s.handleAction("retry")},
		{"POST", "/api/batches/{batch_id}/pause", s.handleBatchAction("pause")},
		{"POST", "/api/batches/{batch_id}/resume", s.handleBatchAction("resume")},
		{"POST", "/api/batches/{batch_id}/cancel", s.handleBatchAction("cancel")},
		{"POST", "/api/batches/{batch_id}/retry", s.handleBatchAction("retry")},
		{"GET", "/api/system/ytdlp/check", s.handleYtdlpCheck},
		{"POST", "/api/system/ytdlp/update", s.handleYtdlpUpdate},
	}
	if opts.WebSocket != nil {
		routes = append(routes, route{"GET", "/api/ws", opts.WebSocket.ServeHTTP})
	}
	for _, rt := range routes {
		mux.Handle(rt.method+" "+rt.path, s.authenticate(rt.h))
	}
	// Players cannot reliably send custom headers. Only this read-only route
	// accepts a signed, expiring link instead of the server's Bearer token.
	mux.HandleFunc("GET /api/jobs/{id}/media", s.handleMedia)
	// The token in the body is the credential, and signing out must work
	// with a stale cookie, so neither route is behind authenticate.
	mux.HandleFunc("POST /api/session", s.handleCreateSession)
	mux.HandleFunc("DELETE /api/session", s.handleDeleteSession)
	// The catch-all also receives requests whose path exists but whose method
	// does not (a method-less pattern for those paths would conflict with
	// "GET /api/jobs/{id}"). Probe the mux to tell 405 from 404.
	fallback := s.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var allow []string
		for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodDelete} {
			probe := r.Clone(r.Context())
			probe.Method = m
			if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/" && pattern != "/api/" {
				allow = append(allow, m)
			}
		}
		if len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			writeError(w, http.StatusMethodNotAllowed, codeInvalidRequest, "method not allowed")
			return
		}
		writeError(w, http.StatusNotFound, codeNotFound, "no such endpoint")
	}))
	mux.Handle("/api/", fallback)
	if opts.WebUI != nil {
		mux.Handle("/", opts.WebUI)
	} else {
		mux.Handle("/", fallback)
	}

	// Browsers attach the session cookie to cross-site requests only under
	// SameSite rules; this also rejects unsafe cross-origin browser requests
	// outright (Sec-Fetch-Site / Origin). Requests without those headers,
	// such as the Android client, are unaffected.
	csrf := http.NewCrossOriginProtection()
	csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusForbidden, codeForbidden, "cross-origin request rejected")
	}))
	return s.logRequests(s.recoverPanics(csrf.Handler(mux)))
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// authenticate enforces the Bearer token or a browser session cookie on API
// endpoints except /api/health. The signed media stream and the session
// routes are registered separately. With no token configured this
// middleware is a pass-through.
func (s *server) authenticate(next http.Handler) http.Handler {
	token := s.cfg.Server.AuthToken
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		got, ok := bearerToken(r)
		if ok && tokenMatches(token, got) || !ok && s.validSession(r, token) {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="fetcharr"`)
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "missing or invalid bearer token")
	})
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, tok, found := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	tok = strings.TrimSpace(tok)
	return tok, tok != ""
}

// recoverPanics turns a handler panic into 500 internal. The panic value and
// stack are logged; request headers and bodies are not.
func (s *server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			s.log.Error("panic in handler", "method", r.Method, "path", r.URL.Path,
				"panic", logsafe.Text(fmt.Sprint(rec)), "stack", string(debug.Stack()))
			if sw, ok := w.(*statusWriter); !ok || !sw.wrote {
				writeError(w, http.StatusInternalServerError, codeInternal, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusWriter records the response status and size for the request log.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.status, w.wrote = http.StatusOK, true
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController (flush, hijack for WebSocket) reach the
// underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests logs one line per request with method, URL path (never the
// query string), status and duration. Headers and bodies are never logged.
// Health checks are logged at debug level to keep container probes quiet.
func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			level := slog.LevelInfo
			if r.URL.Path == "/api/health" {
				level = slog.LevelDebug
			}
			s.log.LogAttrs(r.Context(), level, "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", sw.status),
				slog.Int("bytes", sw.bytes),
				slog.Duration("duration", time.Since(start).Round(time.Microsecond)),
			)
		}()
		next.ServeHTTP(sw, r)
	})
}
