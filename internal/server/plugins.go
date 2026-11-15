package server

import (
	"context"
	"net/http"
)

// pluginFeature holds the state of the plugin routes and of the plugin
// runtime.
type pluginFeature struct{}

// registerPlugins registers the /api/plugins routes.
func (s *Server) registerPlugins(mux *http.ServeMux) {}

// startPlugins starts the plugin runtime. It runs after the board and the
// settings exist and before the engine starts.
func (s *Server) startPlugins(ctx context.Context) {}

// stopPlugins stops the plugin runtime before the database is closed.
func (s *Server) stopPlugins() {}
