package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/istarvin/fetcharr/server/internal/logsafe"
	"github.com/istarvin/fetcharr/server/internal/service"
)

// Error codes that only the HTTP layer produces (spec §6.1).
const (
	codeUnauthorized    = "unauthorized"
	codeYtdlpUnmanaged  = "ytdlp_unmanaged"
	codeYtdlpBusy       = "ytdlp_busy"
	codeInternal        = "internal"
	codeInvalidRequest  = string(service.CodeInvalidRequest)
	codeNotFound        = string(service.CodeNotFound)
	codeInvalidState    = string(service.CodeInvalidState)
	codeDuplicate       = string(service.CodeDuplicate)
	maxBodyBytesDefault = 4 << 20
)

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		slog.Error("encode response", "err", err)
		status = http.StatusInternalServerError
		buf.Reset()
		buf.WriteString(`{"error":{"code":"internal","message":"internal error"}}` + "\n")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// statusFor maps a service error code to its HTTP status (spec §6.1).
func statusFor(c service.Code) int {
	switch c {
	case service.CodeInvalidRequest:
		return http.StatusBadRequest
	case service.CodeNotFound:
		return http.StatusNotFound
	case service.CodeInvalidState, service.CodeDuplicate:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// fail writes the error envelope for err. Typed service errors keep their
// code; anything else is logged and reported as 500 internal without details.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var se *service.Error
	if errors.As(err, &se) {
		writeError(w, statusFor(se.Code), string(se.Code), se.Message)
		return
	}
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", logsafe.Err(err))
	writeError(w, http.StatusInternalServerError, codeInternal, "internal error")
}

func badRequest(w http.ResponseWriter, format string, args ...any) {
	writeError(w, http.StatusBadRequest, codeInvalidRequest, fmt.Sprintf(format, args...))
}

// decodeJSON reads one JSON value from the (size-limited) body into dst.
// Unknown fields are rejected: the spec does not say to ignore them. When
// optional is true an empty body is accepted and leaves dst untouched.
// It writes the 400 response itself and returns false on failure.
func (s *server) decodeJSON(w http.ResponseWriter, r *http.Request, dst any, optional bool) bool {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		// Reject trailing garbage after the first value.
		if _, terr := dec.Token(); !errors.Is(terr, io.EOF) {
			err = errors.New("unexpected data after the JSON value")
		}
	}
	if err == nil {
		return true
	}
	if optional && errors.Is(err, io.EOF) {
		return true
	}
	var (
		mbe *http.MaxBytesError
		ute *json.UnmarshalTypeError
		syn *json.SyntaxError
	)
	switch {
	case errors.As(err, &mbe):
		badRequest(w, "request body is too large (limit %d bytes)", mbe.Limit)
	case errors.Is(err, io.EOF):
		badRequest(w, "request body is required")
	case errors.As(err, &ute):
		badRequest(w, "field '%s' has the wrong type (expected %s)", ute.Field, ute.Type)
	case errors.As(err, &syn), errors.Is(err, io.ErrUnexpectedEOF):
		badRequest(w, "request body is not valid JSON")
	default:
		badRequest(w, "invalid request body: %s", err.Error())
	}
	return false
}
