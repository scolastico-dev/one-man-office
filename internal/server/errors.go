package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// maxBody is the largest accepted request body.
const maxBody = 1 << 20

type contextKey int

const (
	actorKey contextKey = iota
	logKey
)

// withRequestContext attaches the caller and the server log to r.
func withRequestContext(r *http.Request, a board.Actor, l *log.Logger) *http.Request {
	ctx := context.WithValue(r.Context(), actorKey, a)
	ctx = context.WithValue(ctx, logKey, l)
	return r.WithContext(ctx)
}

// actorFrom returns the caller of r, as resolved by BrowserHandler or
// SocketHandler.
func actorFrom(r *http.Request) board.Actor {
	a, _ := r.Context().Value(actorKey).(board.Actor)
	return a
}

// logf writes to the server log attached to r.
func logf(r *http.Request, format string, args ...any) {
	if l, ok := r.Context().Value(logKey).(*log.Logger); ok {
		l.Printf(format, args...)
	}
}

// writeJSON answers with status and v as JSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		status = http.StatusInternalServerError
		data, _ = json.Marshal(api.ErrorEnvelope{Error: api.Errorf(api.CodeInternal, "internal server error")})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	w.Write(append(data, '\n'))
}

// writeError answers with the error envelope. An *api.Error keeps its code
// and status; any other error is logged and answered with a generic 500
// internal.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var e *api.Error
	if errors.As(err, &e) {
		status := e.Status
		if status == 0 {
			status = api.StatusFor(e.Code)
		}
		writeJSON(w, status, api.ErrorEnvelope{Error: e})
		return
	}
	logf(r, "%s %s: %v", r.Method, r.URL.Path, err)
	writeJSON(w, http.StatusInternalServerError, api.ErrorEnvelope{Error: api.Errorf(api.CodeInternal, "internal server error")})
}

// decode reads the JSON body of r into v. The body must be application/json,
// at most 1 MiB, a single value and free of unknown fields. The returned
// error is an *api.Error (bad_request or too_large) for writeError; decode
// writes nothing itself.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return api.Errorf(api.CodeBadRequest, "the request body must be application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return decodeError(err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err != nil {
			if e := decodeError(err); e.Code == api.CodeTooLarge {
				return e
			}
		}
		return api.Errorf(api.CodeBadRequest, "the request body must hold a single JSON value")
	}
	return nil
}

func decodeError(err error) *api.Error {
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return api.Errorf(api.CodeTooLarge, "the request body exceeds %d bytes", maxBody)
	case errors.Is(err, io.EOF):
		return api.Errorf(api.CodeBadRequest, "the request body is empty")
	default:
		return api.Errorf(api.CodeBadRequest, "invalid request body: %v", err)
	}
}

// requireUser answers 403 forbidden and returns false unless the caller is
// the user.
func requireUser(w http.ResponseWriter, r *http.Request) bool {
	if actorFrom(r).Role == api.RoleUser {
		return true
	}
	writeError(w, r, api.Errorf(api.CodeForbidden, "only the user may do this"))
	return false
}
