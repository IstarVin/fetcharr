package api

import (
	"errors"
	"net/http"

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
	var d settings.Document
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
