package server

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

// staleDialTimeout bounds the probe of a leftover socket.
const staleDialTimeout = 500 * time.Millisecond

// SessionHeader carries an agent's session id on the socket.
const SessionHeader = "X-Omo-Session"

// SocketHandler serves the routes to local callers on the socket. A request
// without X-Omo-Session is the user (only the same OS user can reach the
// socket). A request with the header is the agent of that live session; an
// unknown or ended session is rejected with 401 unknown_session and never
// treated as the user.
func (s *Server) SocketHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		r = withRequestContext(r, board.User, s.log)
		values, sent := r.Header[http.CanonicalHeaderKey(SessionHeader)]
		if sent {
			if len(values) != 1 {
				writeError(w, r, api.Errorf(api.CodeUnknownSession, "send exactly one %s header", SessionHeader))
				return
			}
			actor, err := s.board.ResolveSession(r.Context(), strings.TrimSpace(values[0]))
			if err != nil {
				writeError(w, r, err)
				return
			}
			r = withRequestContext(r, actor, s.log)
		}
		if ownAuth(r.URL.Path) {
			s.serveOwnAuth(w, r, true)
			return
		}
		if !requireJSON(w, r) {
			return
		}
		s.mux.ServeHTTP(s.withEventID(w, r), r)
	})
}

// listenSocket listens on a fresh socket endpoint of the run folder and
// records it in SocketEndpoint. Closing the listener also removes the
// endpoint's files.
func (s *Server) listenSocket() (net.Listener, error) {
	run := s.opts.Layout.Run
	if err := os.MkdirAll(run, 0o700); err != nil {
		return nil, err
	}
	removeStaleSocketDir(run)
	actual, _, cleanup, err := transport.Endpoint(run)
	if err != nil {
		return nil, err
	}
	l, err := transport.Listen(actual)
	if err != nil {
		cleanup()
		return nil, err
	}
	s.mu.Lock()
	s.SocketEndpoint = actual
	s.mu.Unlock()
	return &cleanupListener{Listener: l, cleanup: cleanup}, nil
}

// cleanupListener runs cleanup once after the listener is closed.
type cleanupListener struct {
	net.Listener
	once    sync.Once
	cleanup func()
}

func (l *cleanupListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(l.cleanup)
	return err
}

// removeStaleSocketDir removes the private temporary folder that the socket
// link of a crashed earlier run still points to. Only a folder that the
// transport created (omo-sock-* directly in the temporary directory) is
// removed, and only when nothing answers on the old socket: a live server
// keeps its folder.
func removeStaleSocketDir(run string) {
	if runtime.GOOS == "windows" {
		return
	}
	old, err := transport.ClientEndpoint(run)
	if err != nil {
		return
	}
	dir := filepath.Dir(old)
	if filepath.Base(old) != "omo.sock" || !strings.HasPrefix(filepath.Base(dir), "omo-sock-") ||
		filepath.Clean(filepath.Dir(dir)) != filepath.Clean(os.TempDir()) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), staleDialTimeout)
	defer cancel()
	if conn, err := transport.Dial(ctx, old); err == nil {
		conn.Close()
		return
	}
	os.RemoveAll(dir)
}
