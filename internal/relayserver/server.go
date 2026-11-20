package relayserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// Server holds transient routing only. Close also waits for upgraded sockets;
// net/http Shutdown alone cannot shut down hijacked WebSocket connections.
type Server struct {
	mu        sync.Mutex
	wg        sync.WaitGroup
	closed    bool
	hosts     map[string]*peer
	sockets   map[*peer]bool
	password  [32]byte
	protected bool
}

func New(cfg Config) (*Server, error) {
	password, protected, err := passwordHash(cfg.PasswordFile)
	if err != nil {
		return nil, err
	}
	return &Server{hosts: make(map[string]*peer), sockets: make(map[*peer]bool), password: password, protected: protected}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.protected {
		username, password, ok := r.BasicAuth()
		hash := sha256.Sum256([]byte(password))
		if !ok || username != "relay" || subtle.ConstantTimeCompare(hash[:], s.password[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="omo relay", charset="UTF-8"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	// No admission or endpoint credentials belong in a query string.
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	switch {
	case r.URL.Path == "/v1/ws":
		s.serveSocket(w, r)
	case len(r.URL.Path) >= len("/notifications/") && r.URL.Path[:len("/notifications/")] == "/notifications/":
		s.serveNotification(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) serveSocket(w http.ResponseWriter, r *http.Request) {
	// Native clients supply admission headers. Browser cross-origin upgrades are
	// still refused by websocket.Accept's default Origin check.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(relayproto.MaxControlSize)
	p := newPeer(c)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		p.cancel()
		c.CloseNow()
		return
	}
	s.sockets[p] = true
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	defer s.disconnect(p)
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	typ, b, err := c.Read(ctx)
	cancel()
	if err != nil || typ != websocket.MessageText {
		return
	}
	m, err := relayproto.DecodeControl(b)
	if err != nil {
		return
	}
	code := s.bind(p, m)
	if code != "" {
		b, _ := relayproto.EncodeControl(relayproto.Control{V: 1, Type: "error", Code: code})
		ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
		_ = c.Write(ctx, websocket.MessageText, b)
		cancel()
		return
	}
	writerDone, pingDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(writerDone); p.writeLoop(s) }()
	go func() { defer close(pingDone); p.keepalive(s) }()
	for {
		typ, b, err := c.Read(p.ctx)
		if err != nil || typ != websocket.MessageText {
			break
		}
		m, err := relayproto.DecodeControl(b)
		if err != nil || !s.route(p, m) {
			break
		}
	}
	s.disconnect(p)
	<-writerDone
	<-pingDone
}

// Close discards all registrations and pending requests, and joins socket work.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	var victims []*peer
	for p := range s.sockets {
		victims = append(victims, s.dropLocked(p)...)
	}
	s.mu.Unlock()
	closePeers(victims)
	s.wg.Wait()
	return nil
}
