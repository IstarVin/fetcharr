package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Browser sessions (spec §6.1). Browsers cannot send a Bearer header on a
// WebSocket upgrade, so the web UI exchanges the API token for an HttpOnly
// cookie. The cookie is stateless: an expiry plus an HMAC keyed by the token,
// so it survives restarts and stops working when the token changes.
const (
	sessionCookie  = "fetcharr_session"
	sessionVersion = "v1"
	sessionTTL     = 30 * 24 * time.Hour
)

type sessionRequest struct {
	Token string `json:"token"`
}

// handleCreateSession serves POST /api/session. It is not behind the auth
// middleware: the token in the body is the credential.
func (s *server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var req sessionRequest
	if !s.decodeJSON(w, r, &req, false) {
		return
	}
	token := s.cfg.Server.AuthToken
	if token == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !tokenMatches(token, req.Token) {
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "invalid token")
		return
	}
	exp := s.now().Add(sessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    signSession(token, exp.Unix()),
		Expires:  exp,
		MaxAge:   int(sessionTTL / time.Second),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		// No Path: it defaults to the request's directory (".../api"), which
		// keeps the cookie scoped to the API behind any reverse-proxy prefix.
	})
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteSession serves DELETE /api/session (sign out). It needs no
// valid session, so a stale cookie can always be cleared.
func (s *server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// validSession reports whether r carries an unexpired session cookie signed
// with token.
func (s *server) validSession(r *http.Request, token string) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	ver, rest, ok := strings.Cut(c.Value, ".")
	if !ok || ver != sessionVersion {
		return false
	}
	expText, _, ok := strings.Cut(rest, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expText, 10, 64)
	if err != nil || s.now().Unix() >= exp {
		return false
	}
	return hmac.Equal([]byte(c.Value), []byte(signSession(token, exp)))
}

func signSession(token string, exp int64) string {
	expText := strconv.FormatInt(exp, 10)
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("fetcharr-session|" + sessionVersion + "|" + expText))
	return sessionVersion + "." + expText + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// tokenMatches compares fixed-length digests in constant time, which hides
// the token length as well as its content.
func tokenMatches(want, got string) bool {
	a, b := sha256.Sum256([]byte(want)), sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
