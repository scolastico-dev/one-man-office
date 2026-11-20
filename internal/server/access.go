package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// routeAccess says who may call an API route. The zero value is no rule at
// all: registerRoutes refuses a row that does not state one.
type routeAccess int

const (
	// userOnly routes answer 403 forbidden to every caller but the user,
	// before the handler runs.
	userOnly routeAccess = iota + 1
	// agentRead routes may also be called by agent sessions. Only GET routes
	// may be agent-readable; the handler itself decides what an agent sees.
	agentRead
	// agentWrite routes change something and may also be called by agent
	// sessions; the board applies its own write rule for the agent (for an
	// image upload, the rule for comments). Only non-GET routes take it.
	agentWrite
	// localHumanOnly additionally requires socket provenance, absent on TCP/relay.
	localHumanOnly
)

func (a routeAccess) String() string {
	switch a {
	case userOnly:
		return "user only"
	case localHumanOnly:
		return "local socket human only"
	case agentRead:
		return "agents may read"
	case agentWrite:
		return "agents may write within the board's rules"
	default:
		return fmt.Sprintf("no rule (%d)", int(a))
	}
}

// apiRoute is one row of the route table: a ServeMux pattern with its method
// ("GET /api/folders"), its handler and who may call it.
type apiRoute struct {
	pattern string
	handler http.HandlerFunc
	access  routeAccess
}

// registerRoutes registers each row on mux behind a wrapper that enforces
// its access rule, and records the row in s.routeTable, so that one test
// can check every row's rule. It panics on a row without a rule, without a
// method in its pattern, without a handler, marked agentRead for a method
// other than GET or agentWrite for GET: a route table that does not say who may call a route is a
// programming error and must stop the server from starting.
func (s *Server) registerRoutes(mux *http.ServeMux, rows []apiRoute) {
	for _, row := range rows {
		method, _, ok := strings.Cut(row.pattern, " ")
		if !ok || method == "" || strings.HasPrefix(method, "/") {
			panic(fmt.Sprintf("route %q: the pattern must name its method", row.pattern))
		}
		if row.handler == nil {
			panic(fmt.Sprintf("route %q: no handler", row.pattern))
		}
		var h http.HandlerFunc
		switch row.access {
		case userOnly:
			next := row.handler
			h = func(w http.ResponseWriter, r *http.Request) {
				if !requireUser(w, r) {
					return
				}
				next(w, r)
			}
		case localHumanOnly:
			next := row.handler
			h = func(w http.ResponseWriter, r *http.Request) {
				if !requireUser(w, r) {
					return
				}
				local, _ := r.Context().Value(relayHumanKey{}).(bool)
				if !local {
					writeError(w, r, api.Errorf(api.CodeForbidden, "relay administration requires a human on the local socket"))
					return
				}
				next(w, r)
			}
		case agentRead:
			if method != http.MethodGet {
				panic(fmt.Sprintf("route %q: only GET routes may be readable by agents", row.pattern))
			}
			h = row.handler
		case agentWrite:
			if method == http.MethodGet {
				panic(fmt.Sprintf("route %q: a GET route is agentRead, not agentWrite", row.pattern))
			}
			h = row.handler
		default:
			panic(fmt.Sprintf("route %q: no access rule", row.pattern))
		}
		mux.HandleFunc(row.pattern, h)
		s.routeTable = append(s.routeTable, row)
	}
}
