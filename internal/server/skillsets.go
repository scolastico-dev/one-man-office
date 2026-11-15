package server

import (
	"context"
	"net/http"
)

// skillSetFeature holds the state of the skill set routes and checkouts.
type skillSetFeature struct{}

// registerSkillSets registers the /api/skillsets routes.
func (s *Server) registerSkillSets(mux *http.ServeMux) {}

// startSkillSets starts the skill set checkouts. It runs after the board and
// the settings exist and before the engine starts.
func (s *Server) startSkillSets(ctx context.Context) {}

// stopSkillSets stops the skill set work before the database is closed.
func (s *Server) stopSkillSets() {}
