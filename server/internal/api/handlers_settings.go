package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/istarvin/fetcharr/server/internal/config"
	"github.com/istarvin/fetcharr/server/internal/settings"
)

func (s *server) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	if s.settings == nil {
		writeError(w, 404, codeNotFound, "server settings are unavailable")
		return
	}
	writeJSON(w, http.StatusOK, s.settings.Get())
}

func (s *server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, 404, codeNotFound, "server settings are unavailable")
		return
	}
	// Fields an older client does not know keep their saved values: decoding
	// into a struct leaves absent keys untouched.
	current := s.settings.Current().Downloads
	d := settings.Document{Downloads: settings.Downloads{
		RateLimit: current.RateLimit, WindowStart: current.WindowStart,
		WindowEnd: current.WindowEnd, AutoRetries: current.AutoRetries,
	}}
	if !s.decodeJSON(w, r, &d, false) {
		return
	}
	out, err := s.settings.Save(r.Context(), d)
	var validation *settings.ValidationError
	switch {
	case errors.Is(err, settings.ErrConflict):
		writeError(w, http.StatusConflict, "settings_conflict", err.Error())
	case errors.Is(err, settings.ErrCategoryBusy):
		writeError(w, http.StatusConflict, "category_busy", err.Error())
	case errors.As(err, &validation):
		badRequest(w, "%s", validation.Message)
	case err != nil:
		s.fail(w, r, err)
	default:
		writeJSON(w, http.StatusOK, out)
	}
}

// NotificationTester delivers a test message to one notification target.
type NotificationTester interface {
	Test(ctx context.Context, target config.Notification) error
}

// handleTestNotification sends a test message to a saved target. Delivery
// failures are reported as invalid_request with the reason, so the editor
// can show it next to the target.
func (s *server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil || s.notifier == nil {
		writeError(w, http.StatusNotFound, codeNotFound, "notifications are unavailable")
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !s.decodeJSON(w, r, &body, false) {
		return
	}
	target, ok := s.settings.Notification(body.ID)
	if !ok {
		writeError(w, http.StatusNotFound, codeNotFound, "no saved notification target with that id; save settings first")
		return
	}
	if err := s.notifier.Test(r.Context(), target); err != nil {
		badRequest(w, "test notification failed: %v", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
