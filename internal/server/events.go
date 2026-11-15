package server

import (
	"context"
	"net/http"
)

// registerEvents adds the event stream route.
func (s *Server) registerEvents(mux *http.ServeMux) {}

// boardChanged is the board's Notify hook: it runs after every committed
// board change, outside the transaction. It must not block.
func (s *Server) boardChanged() {}

// runEvents runs the event fan-out until ctx ends. New starts it; Close
// stops it and waits for it to return.
func (s *Server) runEvents(ctx context.Context) {}
