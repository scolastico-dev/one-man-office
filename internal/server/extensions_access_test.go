package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
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
	{"GET /api/skillsets/search", nil},
	{"POST /api/skillsets/search/resolve", api.SkillSearchChoice{Source: "o/r", SkillID: "x"}},
	{"GET /api/plugins", nil},
	{"GET /api/plugins/catalog", nil},
	{"POST /api/plugins", api.PluginInstall{PluginSettings: api.PluginSettings{Name: "x", Source: "https://example.invalid/x.git"}, Acknowledge: api.PluginCommunity}},
	{"PUT /api/plugins/{name}", api.PluginInstall{PluginSettings: api.PluginSettings{Name: "x", Source: "https://example.invalid/x.git"}, Acknowledge: api.PluginCommunity}},
	{"POST /api/plugins/{name}/update", api.PluginUpdate{}},
	{"DELETE /api/plugins/{name}", nil},
	{"GET /api/plugins/{name}/log", nil},
	{"GET /api/plugins/{name}/tasks", nil},
	{"POST /api/plugins/{name}/tasks/{task}/stop", map[string]any{}},
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

// mcpRoutes lists the routes of the MCP settings; every route below /api/mcp
// and /api/oauth is checked by TestMCPRoutesRefuseAgents, listed or not.
var mcpRoutes = []string{
	"GET /api/mcp/servers",
	"GET /api/mcp/servers/{name}",
	"PUT /api/mcp/servers/{name}",
	"DELETE /api/mcp/servers/{name}",
	"POST /api/mcp/servers/test",
	"POST /api/mcp/servers/{name}/restart",
	"GET /api/mcp/servers/{name}/log",
	"POST /api/mcp/servers/{name}/connect",
	"POST /api/mcp/servers/{name}/disconnect",
	"GET /api/mcp/tokens",
	"POST /api/mcp/tokens",
	"DELETE /api/mcp/tokens/{id}",
}

// TestMCPRoutesRefuseAgents checks that the MCP settings routes are
// registered and that every route below /api/mcp and /api/oauth is user
// only and refuses an agent session with 403, as does the OAuth callback:
// these routes manage the user's credentials for upstream servers, the
// tokens of MCP clients and their grants.
func TestMCPRoutesRefuseAgents(t *testing.T) {
	s := newTestServer(t)
	relay := &fakeRelay{}
	s.mcpConf.relay, s.mcpConf.tokens = relay, &fakeTokens{}
	sess := liveSession(t, s, api.RoleDeveloper)
	for _, p := range mcpRoutes {
		if !s.registeredPattern(p) {
			t.Errorf("%s is not registered", p)
		}
	}
	n := 0
	for _, row := range s.routeTable {
		method, path, _ := strings.Cut(row.pattern, " ")
		if !strings.HasPrefix(path, "/api/mcp/") && !strings.HasPrefix(path, "/api/oauth/") && path != "/api/mcp" && path != "/api/oauth" {
			continue
		}
		n++
		if row.access != userOnly {
			t.Errorf("%s has access %s, want user only", row.pattern, row.access)
		}
		rec := socketDo(t, s, method, wildcard.ReplaceAllString(path, "1"), map[string]any{}, sess.ID)
		if rec.Code != http.StatusForbidden {
			t.Errorf("an agent got %d from %s, want 403", rec.Code, row.pattern)
		}
	}
	if n < len(mcpRoutes) {
		t.Fatalf("%d MCP routes in the table, want at least %d", n, len(mcpRoutes))
	}
	if rec := socketDo(t, s, "GET", "/oauth/callback?state=x", nil, sess.ID); rec.Code != http.StatusForbidden || relay.callbacks != 0 {
		t.Fatalf("an agent got %d from the OAuth callback (relay called %d times), want 403", rec.Code, relay.callbacks)
	}
}
