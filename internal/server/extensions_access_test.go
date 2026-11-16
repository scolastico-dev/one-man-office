package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// extensionRoute is one route of the usage, skill set and plugin API.
type extensionRoute struct {
	pattern string
	body    any
}

// extensionRoutes lists every route of the extension API with a body its
// handler would accept.
var extensionRoutes = []extensionRoute{
	{"GET /api/usage", nil},
	{"POST /api/usage/refresh", map[string]any{}},
	{"GET /api/usage/scripts", nil},
	{"GET /api/usage/scripts/{name}", nil},
	{"PUT /api/usage/scripts/{name}", api.UsageScriptUpdate{Text: "function usage() end"}},
	{"DELETE /api/usage/scripts/{name}", nil},
	{"POST /api/usage/scripts/{name}/test", api.UsageTest{Text: "function usage() end", Profile: "p"}},
	{"POST /api/tickets/{n}/start-anyway", api.StartAnyway{}},
	{"GET /api/skillsets", nil},
	{"PUT /api/skillsets/{name}", api.SkillSet{Repository: "https://example.invalid/x.git", Update: api.SkillSetPinned}},
	{"DELETE /api/skillsets/{name}", nil},
	{"POST /api/skillsets/{name}/update", map[string]any{}},
	{"GET /api/plugins", nil},
	{"GET /api/plugins/catalog", nil},
	{"POST /api/plugins", api.PluginInstall{PluginSettings: api.PluginSettings{Name: "x", Source: "https://example.invalid/x.git"}, Acknowledge: api.PluginCommunity}},
	{"PUT /api/plugins/{name}", api.PluginInstall{PluginSettings: api.PluginSettings{Name: "x", Source: "https://example.invalid/x.git"}, Acknowledge: api.PluginCommunity}},
	{"POST /api/plugins/{name}/update", api.PluginUpdate{}},
	{"DELETE /api/plugins/{name}", nil},
	{"GET /api/plugins/{name}/log", nil},
	{"POST /api/plugins/{name}/actions/{action}", api.PluginRun{}},
}

func (s *Server) registeredPattern(pattern string) bool {
	for _, row := range s.routeTable {
		if row.pattern == pattern {
			return true
		}
	}
	return false
}

// TestExtensionRoutesRefuseAgents checks that every extension route is
// registered and refuses an agent session with 403.
func TestExtensionRoutesRefuseAgents(t *testing.T) {
	s := newTestServer(t)
	sess := liveSession(t, s, api.RoleDeveloper)
	for _, r := range extensionRoutes {
		t.Run(r.pattern, func(t *testing.T) {
			if !s.registeredPattern(r.pattern) {
				t.Fatalf("%s is not registered", r.pattern)
			}
			method, path, _ := strings.Cut(r.pattern, " ")
			rec := socketDo(t, s, method, wildcard.ReplaceAllString(path, "1"), r.body, sess.ID)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("an agent got %d from %s, want 403", rec.Code, r.pattern)
			}
		})
	}
}

// TestExtensionRoutesAreUserOnly checks the access rule in the table: no
// extension route may be readable by agents.
func TestExtensionRoutesAreUserOnly(t *testing.T) {
	s := newTestServer(t)
	for _, row := range s.routeTable {
		for _, r := range extensionRoutes {
			if row.pattern == r.pattern && row.access != userOnly {
				t.Errorf("%s has access %s, want user only", row.pattern, row.access)
			}
		}
	}
}

// TestExtensionRoutesAllRegistered asserts that the whole wire table is
// registered.
func TestExtensionRoutesAllRegistered(t *testing.T) {
	s := newTestServer(t)
	for _, r := range extensionRoutes {
		if !s.registeredPattern(r.pattern) {
			t.Errorf("%s is not registered", r.pattern)
		}
	}
}

// TestReservedRoutesGone checks that no extension path answers 501 any more.
func TestReservedRoutesGone(t *testing.T) {
	s := newTestServer(t)
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/tickets/1/export"},
		{"GET", "/api/usage"},
		{"GET", "/api/skillsets"},
		{"GET", "/api/plugins/x"},
	} {
		req := withRequestContext(newRequest(t, c.method, "http://omo"+c.path, nil), board.User, s.log)
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, req)
		if rec.Code == http.StatusNotImplemented {
			t.Errorf("%s %s still answers 501", c.method, c.path)
		}
	}
	if len(reservedRoutes) != 0 {
		t.Errorf("reservedRoutes = %v, want none", reservedRoutes)
	}
}
