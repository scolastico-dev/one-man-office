package board

import "github.com/scolastico-dev/one-man-office/internal/api"

// Actor is who performs a board operation. An agent actor is bound to its
// session's ticket: a pm to its story, a developer to its task, a reviewer to
// the reviewed ticket and Project; the CEO has no ticket. Purpose and
// ReviewRound come from the session: an export job has the purpose export, a
// reviewer belongs to the review round it was started for.
type Actor struct {
	Role        api.Role
	Session     string
	Ticket      int64
	Project     string
	Name        string
	Purpose     api.Purpose
	ReviewRound int
}

// User is the human user.
var User = Actor{Role: api.RoleUser}

// System is the engine.
var System = Actor{Role: api.RoleSystem}

// API returns the wire form of the actor.
func (a Actor) API() api.Actor {
	return api.Actor{Role: a.Role, Session: a.Session, Name: a.Name}
}

// isAgent reports whether the actor is an agent session.
func (a Actor) isAgent() bool {
	switch a.Role {
	case api.RoleCEO, api.RolePM, api.RoleDeveloper, api.RoleReviewer:
		return true
	}
	return false
}

// sessionArg returns the actor's session as a nullable column value.
func (a Actor) sessionArg() any {
	if a.Session == "" {
		return nil
	}
	return a.Session
}
