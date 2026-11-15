package server

import (
	"net/http"

	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// registerSettings adds the settings, project, profile, model, role and
// prompt routes.
func (s *Server) registerSettings(mux *http.ServeMux) {}

// settingsChanged receives every change of the settings store, from its own
// writes and from hand edits. The store calls it with its operation lock
// held: it must return quickly, must not block, and must never call a store
// mutator or Reload (that deadlocks). Hand longer work to a goroutine
// started with s.background.
func (s *Server) settingsChanged(c settings.Change) {}
