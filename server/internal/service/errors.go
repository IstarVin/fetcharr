package service

import (
	"errors"
	"fmt"
)

// Code is an API error code (spec §6.1).
type Code string

// Error codes produced by the service. The API layer maps them to HTTP
// statuses; any other error is an unexpected failure (500 internal).
const (
	CodeInvalidRequest Code = "invalid_request" // 400
	CodeNotFound       Code = "not_found"       // 404
	CodeInvalidState   Code = "invalid_state"   // 409
	CodeDuplicate      Code = "duplicate"       // 409
)

// Error is a typed service error. Message is safe to show to the client.
type Error struct {
	Code    Code
	Message string
	err     error
}

func (e *Error) Error() string { return e.Message }

// Unwrap returns the underlying cause, if any.
func (e *Error) Unwrap() error { return e.err }

// CodeOf returns the Code of err if it is (or wraps) an *Error.
func CodeOf(err error) (Code, bool) {
	var se *Error
	if errors.As(err, &se) {
		return se.Code, true
	}
	return "", false
}

func newErr(code Code, cause error, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...), err: cause}
}

func invalid(format string, args ...any) *Error {
	return newErr(CodeInvalidRequest, nil, format, args...)
}

func notFound(what, id string) *Error {
	return newErr(CodeNotFound, nil, "%s %s not found", what, id)
}
