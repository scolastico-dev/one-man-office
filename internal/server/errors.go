package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"

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

// checkJSONContentType requires Content-Type application/json (parameters such
// as charset are allowed).
func checkJSONContentType(r *http.Request) *api.Error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return api.Errorf(api.CodeBadRequest, "the request body must be application/json")
	}
	return nil
}

// requireJSON answers with bad_request and returns false when r is a
// state-changing API request (anything under /api except GET, HEAD and
// OPTIONS) without a JSON Content-Type; a raw upload (rawUpload) needs
// application/octet-stream instead. A cross-site form post cannot set
// that header without a CORS preflight, so this backs up the Origin check on
// routes that read no body.
func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	if !isAPI(r.URL.Path) {
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if rawUpload(r) {
		media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != rawUploadType {
			writeError(w, r, api.Errorf(api.CodeBadRequest, "the request body must be %s", rawUploadType))
			return false
		}
		return true
	}
	if err := checkJSONContentType(r); err != nil {
		writeError(w, r, err)
		return false
	}
	return true
}

// rawUploadType is the Content-Type of a raw upload body. Like
// application/json it is not one a cross-site form can send without a CORS
// preflight, which omo never grants.
const rawUploadType = "application/octet-stream"

// rawUploadPath matches the routes whose body is raw bytes instead of JSON:
// the image uploads and the settings zip of an import.
var rawUploadPath = regexp.MustCompile(`^/api/(tickets/[0-9]+/images|terminal-images|settings/import)$`)

// rawUpload reports whether r is a request to a raw upload route; such a
// request must carry Content-Type application/octet-stream and nothing else.
func rawUpload(r *http.Request) bool {
	return r.Method == http.MethodPost && rawUploadPath.MatchString(r.URL.Path)
}

// decode reads the JSON body of r into v. The body must be application/json,
// at most 1 MiB, a single value and free of unknown fields. The returned
// error is an *api.Error (bad_request or too_large) for writeError; decode
// writes nothing itself. Refusals name the field, never a value of the body.
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if err := checkJSONContentType(r); err != nil {
		return err
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
	default:
		// The parser's own message can quote what it could not read (a
		// duration, a number too large); the refusal names the field only,
		// when the decoder names it: the body is not kept to look further.
		return settingsDecodeError(err, "", func() string { return "" })
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
