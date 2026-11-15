package server

import (
	"context"
	"net/http"

	"github.com/scolastico-dev/one-man-office/internal/engine"
)

// usageFeature holds the state of the usage routes and of the usage tracker.
type usageFeature struct{}

// registerUsage registers the usage routes (and POST /api/tickets/{n}/start-anyway).
func (s *Server) registerUsage(mux *http.ServeMux) {}

// startUsage starts the usage tracker. It runs after the board and the
// settings exist and before the engine starts.
func (s *Server) startUsage(ctx context.Context) {}

// stopUsage stops the usage tracker before the database is closed.
func (s *Server) stopUsage() {}

// usageView is what the engine asks about models; nil means every model is
// unmetered.
func (s *Server) usageView() engine.UsageView { return nil }
