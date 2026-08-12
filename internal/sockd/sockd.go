// Package sockd is the office-side Unix-socket / Windows-pipe server. Every agent verb
// round-trips through here; handlers persist state before returning, which
// is what makes verb acknowledgment durable.
package sockd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/proto"
)

type Handler func(agentID string, args json.RawMessage) (any, error)

type AuthFunc func(agentID, verb string) error

type Server struct {
	path     string
	auth     AuthFunc
	mu       sync.RWMutex
	handlers map[string]Handler
	ln       net.Listener
}

func New(path string, auth AuthFunc) *Server {
	return &Server{path: path, auth: auth, handlers: map[string]Handler{}}
}

func (s *Server) Handle(verb string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[verb] = h
}

func (s *Server) ListenAndServe() error {
	if err := s.Listen(); err != nil {
		return err
	}
	return s.Serve()
}

// Listen binds the socket or pipe synchronously. Office startup uses this to
// make a newly-written instance lock immediately verifiable.
func (s *Server) Listen() error {
	os.Remove(s.path) // stale socket from a previous run
	ln, err := listen(s.path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	return nil
}

// Serve accepts connections on a listener previously created by Listen.
func (s *Server) Serve() error {
	s.mu.RLock()
	ln := s.ln
	s.mu.RUnlock()
	if ln == nil {
		return fmt.Errorf("server is not listening")
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.serve(conn)
	}
}

func (s *Server) Close() error {
	s.mu.RLock()
	ln := s.ln
	s.mu.RUnlock()
	if ln != nil {
		return ln.Close()
	}
	return nil
}

func (s *Server) serve(conn net.Conn) {
	defer conn.Close()
	var req proto.Request
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		return
	}
	resp := s.dispatch(req)
	json.NewEncoder(conn).Encode(resp)
}

func (s *Server) dispatch(req proto.Request) proto.Response {
	if s.auth != nil {
		if err := s.auth(req.AgentID, req.Verb); err != nil {
			return proto.Response{Error: err.Error()}
		}
	}
	s.mu.RLock()
	h, ok := s.handlers[req.Verb]
	s.mu.RUnlock()
	if !ok {
		return proto.Response{Error: fmt.Sprintf("unknown verb %q", req.Verb)}
	}
	data, err := h(req.AgentID, req.Args)
	if err != nil {
		return proto.Response{Error: err.Error()}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return proto.Response{Error: err.Error()}
	}
	return proto.Response{OK: true, Data: raw}
}
