package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/istarvin/fetcharr/server/internal/config"
)

// sessionEnv wraps a handler built over e's config and service, with a fake
// clock, a stub WebSocket and a stub web UI.
type sessionEnv struct {
	*env
	h   http.Handler
	now time.Time
}

func newSessionEnv(t *testing.T, mods ...func(*config.Config)) *sessionEnv {
	t.Helper()
	se := &sessionEnv{env: newEnv(t, mods...), now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	se.h = New(Options{
		Config: se.cfg, Service: se.svc, Version: "1.0.0", Updater: se.updater,
		Logger: newEnvLogger(se.env),
		Now:    func() time.Time { return se.now },
		WebSocket: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusSwitchingProtocols)
		}),
		WebUI: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "web ui")
		}),
	})
	return se
}

func newEnvLogger(e *env) *slog.Logger {
	return slog.New(slog.NewTextHandler(e.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (se *sessionEnv) do(method, target, body string, hdr map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	se.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(se.t.Context(), method, target, rd)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	se.h.ServeHTTP(rec, req)
	return rec
}

func (se *sessionEnv) login(t *testing.T) *http.Cookie {
	t.Helper()
	r := se.do("POST", "/api/session", `{"token":"`+testToken+`"}`, nil)
	if r.Code != http.StatusNoContent {
		t.Fatalf("login: %d %s", r.Code, r.Body)
	}
	cs := r.Result().Cookies()
	if len(cs) != 1 || cs[0].Name != sessionCookie {
		t.Fatalf("cookies: %v", cs)
	}
	c := cs[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "" || c.MaxAge <= 0 {
		t.Errorf("cookie attributes: %+v", c)
	}
	if strings.Contains(c.Value, testToken) {
		t.Error("cookie contains the token")
	}
	return &http.Cookie{Name: c.Name, Value: c.Value}
}

func TestSessionLogin(t *testing.T) {
	se := newSessionEnv(t)

	for _, body := range []string{`{"token":"wrong"}`, `{"token":""}`, `{}`} {
		r := se.do("POST", "/api/session", body, nil)
		if r.Code != 401 || len(r.Result().Cookies()) != 0 {
			t.Errorf("%s: %d %v", body, r.Code, r.Result().Cookies())
		}
	}
	if r := se.do("POST", "/api/session", `{"token":1}`, nil); r.Code != 400 {
		t.Errorf("bad body: %d", r.Code)
	}

	c := se.login(t)
	if r := se.do("GET", "/api/info", "", nil, c); r.Code != 200 {
		t.Fatalf("info with cookie: %d %s", r.Code, r.Body)
	}
	if r := se.do("GET", "/api/ws", "", nil, c); r.Code != http.StatusSwitchingProtocols {
		t.Errorf("ws with cookie: %d", r.Code)
	}
	r := se.do("POST", "/api/jobs", `{"url":"https://s.example/a.m3u8","category":"misc","title":"A"}`,
		map[string]string{"Content-Type": "application/json", "Sec-Fetch-Site": "same-origin"}, c)
	if r.Code != 201 {
		t.Errorf("same-origin create with cookie: %d %s", r.Code, r.Body)
	}
	if r := se.do("GET", "/api/info", "", nil); r.Code != 401 {
		t.Errorf("info without cookie: %d", r.Code)
	}
	// A wrong Bearer is not rescued by a valid cookie.
	if r := se.do("GET", "/api/info", "", map[string]string{"Authorization": "Bearer nope"}, c); r.Code != 401 {
		t.Errorf("wrong bearer + cookie: %d", r.Code)
	}
	if strings.Contains(se.logs.String(), c.Value) {
		t.Error("logs contain the session cookie")
	}
}

func TestSessionSecureBehindTLSProxy(t *testing.T) {
	se := newSessionEnv(t)
	r := se.do("POST", "/api/session", `{"token":"`+testToken+`"}`, map[string]string{"X-Forwarded-Proto": "https"})
	if cs := r.Result().Cookies(); len(cs) != 1 || !cs[0].Secure {
		t.Fatalf("cookies: %v", cs)
	}
}

func TestSessionRejected(t *testing.T) {
	se := newSessionEnv(t)
	c := se.login(t)

	tampered := *c
	tampered.Value = c.Value[:len(c.Value)-2] + "AA"
	if tampered.Value == c.Value {
		tampered.Value = c.Value[:len(c.Value)-2] + "BB"
	}
	exp := strings.Split(c.Value, ".")[1]
	extended := &http.Cookie{Name: sessionCookie, Value: strings.Replace(c.Value, exp, exp+"9", 1)}
	for name, bad := range map[string]*http.Cookie{
		"tampered": &tampered,
		"extended": extended,
		"garbage":  {Name: sessionCookie, Value: "garbage"},
		"v0":       {Name: sessionCookie, Value: "v0" + strings.TrimPrefix(c.Value, "v1")},
	} {
		if r := se.do("GET", "/api/info", "", nil, bad); r.Code != 401 {
			t.Errorf("%s: %d", name, r.Code)
		}
	}

	se.now = se.now.Add(sessionTTL + time.Second)
	if r := se.do("GET", "/api/info", "", nil, c); r.Code != 401 {
		t.Errorf("expired: %d", r.Code)
	}
}

func TestSessionInvalidatedByTokenChange(t *testing.T) {
	se := newSessionEnv(t)
	c := se.login(t)
	se.cfg.Server.AuthToken = "another-token-abcdef"
	h := New(Options{Config: se.cfg, Service: se.svc, Logger: newEnvLogger(se.env)})
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/api/info", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("old cookie after token change: %d", rec.Code)
	}
}

func TestSessionLogout(t *testing.T) {
	se := newSessionEnv(t)
	r := se.do("DELETE", "/api/session", "", nil)
	cs := r.Result().Cookies()
	if r.Code != 204 || len(cs) != 1 || cs[0].MaxAge >= 0 {
		t.Fatalf("logout: %d %v", r.Code, cs)
	}
}

func TestSessionWithoutAuth(t *testing.T) {
	se := newSessionEnv(t, func(c *config.Config) { c.Server.AuthToken = "" })
	r := se.do("POST", "/api/session", `{"token":"anything"}`, nil)
	if r.Code != 204 || len(r.Result().Cookies()) != 0 {
		t.Fatalf("%d %v", r.Code, r.Result().Cookies())
	}
	if r := se.do("GET", "/api/info", "", nil); r.Code != 200 {
		t.Errorf("info: %d", r.Code)
	}
}

func TestCrossOriginRejected(t *testing.T) {
	se := newSessionEnv(t)
	c := se.login(t)
	body := `{"url":"https://x.example/a.m3u8","category":"misc","title":"A"}`
	for name, hdr := range map[string]map[string]string{
		"sec-fetch-site": {"Sec-Fetch-Site": "cross-site"},
		"same-site":      {"Sec-Fetch-Site": "same-site"},
		"origin":         {"Origin": "https://evil.example"},
	} {
		r := se.do("POST", "/api/jobs", body, hdr, c)
		if r.Code != 403 || !strings.Contains(r.Body.String(), `"forbidden"`) {
			t.Errorf("%s: %d %s", name, r.Code, r.Body)
		}
		// Even the login and a Bearer request from a browser page elsewhere.
		if r := se.do("POST", "/api/session", `{"token":"`+testToken+`"}`, hdr); r.Code != 403 {
			t.Errorf("%s login: %d", name, r.Code)
		}
	}
	// Safe methods are never blocked; non-browser clients send neither header.
	if r := se.do("GET", "/api/info", "", map[string]string{"Sec-Fetch-Site": "cross-site"}, c); r.Code != 200 {
		t.Errorf("cross-site GET: %d", r.Code)
	}
	if r := se.do("POST", "/api/jobs", body, map[string]string{"Authorization": "Bearer " + testToken}); r.Code != 201 {
		t.Errorf("non-browser POST: %d %s", r.Code, r.Body)
	}
}

func TestWebUIRouting(t *testing.T) {
	se := newSessionEnv(t)
	for _, p := range []string{"/", "/assets/app.js", "/favicon.svg"} {
		r := se.do("GET", p, "", nil)
		if r.Code != 200 || r.Body.String() != "web ui" {
			t.Errorf("%s: %d %q", p, r.Code, r.Body)
		}
	}
	// API paths never fall through to the UI.
	if r := se.do("GET", "/api/nope", "", nil); r.Code != 401 {
		t.Errorf("/api/nope unauthenticated: %d", r.Code)
	}
	r := se.do("GET", "/api/nope", "", map[string]string{"Authorization": "Bearer " + testToken})
	if r.Code != 404 || !strings.Contains(r.Body.String(), "not_found") {
		t.Errorf("/api/nope: %d %s", r.Code, r.Body)
	}
	r = se.do("PUT", "/api/jobs", "", map[string]string{"Authorization": "Bearer " + testToken})
	if r.Code != 405 || r.Header().Get("Allow") != "GET, HEAD, POST, DELETE" {
		t.Errorf("PUT /api/jobs: %d %q", r.Code, r.Header().Get("Allow"))
	}
}
