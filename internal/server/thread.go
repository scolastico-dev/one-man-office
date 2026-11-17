package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// maxLogBytes is the most of a saved agent log one answer carries.
const maxLogBytes = 2 << 20

// maxLogSegments bounds the rotated segments looked for next to a log.
const maxLogSegments = 64

func (s *Server) registerThread(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tickets/{n}/thread", s.getThread)
	mux.HandleFunc("GET /api/notifications", s.getNotifications)
	mux.HandleFunc("PUT /api/notifications/seen", s.putNotificationsSeen)
	mux.HandleFunc("GET /api/sessions/{id}/log", s.getSessionLog)
}

// strictInt parses an optional query value that is a plain decimal integer:
// an optional minus sign and digits, nothing else, given at most once.
// Range checks are the board's.
func strictInt(r *http.Request, name string) (int64, error) {
	vals := r.URL.Query()[name]
	if len(vals) == 0 {
		return 0, nil
	}
	raw := vals[0]
	digits := strings.TrimPrefix(raw, "-")
	ok := len(vals) == 1 && digits != ""
	for _, c := range digits {
		ok = ok && c >= '0' && c <= '9'
	}
	var v int64
	if ok {
		var err error
		v, err = strconv.ParseInt(raw, 10, 64)
		ok = err == nil
	}
	if !ok {
		return 0, api.Errorf(api.CodeBadRequest, "%s must be an integer, got %q", name, raw)
	}
	return v, nil
}

func (s *Server) getThread(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	before, err := strictInt(r, "before")
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit, err := strictInt(r, "limit")
	if err == nil && (limit > 1<<20 || limit < -(1<<20)) {
		// Far beyond any page: let the board's range rule answer.
		limit = 1 << 20
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.TicketThread(r.Context(), actorFrom(r), n, before, int(limit))
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) getNotifications(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	out, err := s.board.UserMentions(r.Context(), actorFrom(r), board.MaxMentionPage)
	reply(w, r, http.StatusOK, out, err)
}

func (s *Server) putNotificationsSeen(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	var req api.NotificationsSeen
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.SetMentionsSeen(r.Context(), actorFrom(r), req.Through)
	reply(w, r, http.StatusOK, out, err)
}

var errLogGone = api.Errorf(api.CodeNotFound, "The log of this session is no longer kept.")

func (s *Server) getSessionLog(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	path, err := s.board.SessionLogPath(r.Context(), actorFrom(r), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	body, truncated, err := readLogTail(s.opts.Layout.Logs, path)
	if err != nil {
		writeError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	if truncated {
		h.Set("Omo-Log-Truncated", "true")
	}
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

// insideDir resolves path and reports it when it lies inside the resolved
// dir. A path that does not resolve (missing, dangling) is not inside.
func insideDir(dir, path string) (string, bool) {
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return real, true
}

// logPiece is one file of a log that may be read.
type logPiece struct {
	path string
	size int64
}

// readLogTail returns the newest maxLogBytes of the log at path and its
// rotated segments (path.N, higher N older), oldest first, cut at a line
// start when it had to cut. Only files that resolve inside dir are read; any
// other segment is skipped, and a log whose own path is not inside dir is
// reported as gone.
func readLogTail(dir, path string) ([]byte, bool, error) {
	if !filepath.IsAbs(path) {
		return nil, false, errLogGone
	}
	var pieces []logPiece // newest first
	add := func(p string) {
		real, ok := insideDir(dir, p)
		if !ok {
			return
		}
		if fi, err := os.Stat(real); err == nil && fi.Mode().IsRegular() {
			pieces = append(pieces, logPiece{real, fi.Size()})
		}
	}
	add(path)
	for i := 1; i <= maxLogSegments; i++ {
		add(fmt.Sprintf("%s.%d", path, i))
	}
	if len(pieces) == 0 {
		return nil, false, errLogGone
	}
	remaining := int64(maxLogBytes)
	var chunks [][]byte // newest first
	truncated := false
	for _, p := range pieces {
		take := p.size
		if take > remaining {
			take = remaining
			truncated = true
		}
		data, err := readRange(p.path, p.size-take, take)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		chunks = append(chunks, data)
		remaining -= int64(len(data))
		if truncated || remaining <= 0 {
			if remaining <= 0 && !truncated {
				// Exactly full: older segments, if any, are cut off.
				truncated = len(chunks) < len(pieces)
			}
			break
		}
	}
	var out []byte
	for i := len(chunks) - 1; i >= 0; i-- {
		out = append(out, chunks[i]...)
	}
	if truncated {
		if i := strings.IndexByte(string(out), '\n'); i >= 0 {
			out = out[i+1:]
		}
	}
	return out, truncated, nil
}

func readRange(path string, off, n int64) ([]byte, error) {
	f, err := homefile.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	got, err := f.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:got], nil
}
