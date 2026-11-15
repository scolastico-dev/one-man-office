package api

import (
	"fmt"
	"net/http"
)

// Error codes carried in the error envelope.
const (
	CodeBadRequest         = "bad_request"
	CodeUnauthorized       = "unauthorized"
	CodeUnknownSession     = "unknown_session"
	CodeForbidden          = "forbidden"
	CodeNotFound           = "not_found"
	CodeInvalidTransition  = "invalid_transition"
	CodeStaleDescription   = "stale_description"
	CodeFieldLocked        = "field_locked"
	CodeConflict           = "conflict"
	CodeInUse              = "in_use"
	CodeTooLarge           = "too_large"
	CodeValidation         = "validation"
	CodeDependencyScope    = "dependency_scope"
	CodeDependencyCycle    = "dependency_cycle"
	CodeInvalidMention     = "invalid_mention"
	CodeProjectUnavailable = "project_unavailable"
	CodeInvalidSettings    = "invalid_settings"
	CodeStaleSettings      = "stale_settings"
	CodeInternal           = "internal"
	CodeNotImplemented     = "not_implemented"
	CodeUnavailable        = "unavailable"
)

var codeStatus = map[string]int{
	CodeBadRequest:         http.StatusBadRequest,
	CodeUnauthorized:       http.StatusUnauthorized,
	CodeUnknownSession:     http.StatusUnauthorized,
	CodeForbidden:          http.StatusForbidden,
	CodeNotFound:           http.StatusNotFound,
	CodeInvalidTransition:  http.StatusConflict,
	CodeStaleDescription:   http.StatusConflict,
	CodeFieldLocked:        http.StatusConflict,
	CodeConflict:           http.StatusConflict,
	CodeInUse:              http.StatusConflict,
	CodeTooLarge:           http.StatusRequestEntityTooLarge,
	CodeValidation:         http.StatusUnprocessableEntity,
	CodeDependencyScope:    http.StatusUnprocessableEntity,
	CodeDependencyCycle:    http.StatusUnprocessableEntity,
	CodeInvalidMention:     http.StatusUnprocessableEntity,
	CodeProjectUnavailable: http.StatusUnprocessableEntity,
	CodeInvalidSettings:    http.StatusUnprocessableEntity,
	CodeStaleSettings:      http.StatusConflict,
	CodeInternal:           http.StatusInternalServerError,
	CodeNotImplemented:     http.StatusNotImplemented,
	CodeUnavailable:        http.StatusServiceUnavailable,
}

// StatusFor returns the HTTP status of an error code; unknown codes map to 500.
func StatusFor(code string) int {
	if s, ok := codeStatus[code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Error is the body of an error response, wrapped as {"error": ...} by
// ErrorEnvelope.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf builds an Error whose status comes from the code table.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Status: StatusFor(code), Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrorEnvelope is the JSON shape of every error response.
type ErrorEnvelope struct {
	Error *Error `json:"error"`
}
