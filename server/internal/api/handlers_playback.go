package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/istarvin/fetcharr/server/internal/timefmt"
)

const playbackLifetime = 24 * time.Hour

func (s *server) playbackSignature(id, expires string) string {
	mac := hmac.New(sha256.New, s.playbackKey[:])
	mac.Write([]byte(id + "\n" + expires))
	return hex.EncodeToString(mac.Sum(nil))
}

// handlePlayback requires normal API authentication and issues access to one
// file only. Return a relative URL so reverse-proxy prefixes are preserved by
// the client, without trusting forwarded host headers.
func (s *server) handlePlayback(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := s.svc.OpenMedia(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	expires := time.Now().Add(playbackLifetime)
	exp := strconv.FormatInt(expires.Unix(), 10)
	path := "jobs/" + url.PathEscape(id) + "/media?" + url.Values{
		"expires": {exp}, "signature": {s.playbackSignature(id, exp)},
	}.Encode()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, struct {
		URL       string       `json:"url"`
		MimeType  string       `json:"mime_type"`
		ExpiresAt timefmt.Time `json:"expires_at"`
	}{path, mediaType(f.Name()), timefmt.New(expires)})
}

func (s *server) handleMedia(w http.ResponseWriter, r *http.Request) {
	id, q := r.PathValue("id"), r.URL.Query()
	exp, signature := q.Get("expires"), q.Get("signature")
	deadline, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() >= deadline ||
		!hmac.Equal([]byte(signature), []byte(s.playbackSignature(id, exp))) {
		writeError(w, http.StatusUnauthorized, codeUnauthorized, "playback link is invalid or expired; open Play again")
		return
	}
	f, err := s.svc.OpenMedia(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// The API server normally limits writes to 60 seconds. Playback can take
	// hours, so clear that deadline for this authorized stream only.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", mediaType(f.Name()))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	// ServeContent handles HEAD and byte ranges for seeking without loading
	// the file into memory or transcoding it.
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func mediaType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	// Keep these stable across hosts whose MIME databases differ.
	switch ext {
	case ".mkv":
		return "video/x-matroska"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".ts", ".m2ts", ".mts":
		return "video/mp2t"
	}
	if typ := mime.TypeByExtension(ext); typ != "" {
		return typ
	}
	return "application/octet-stream"
}
