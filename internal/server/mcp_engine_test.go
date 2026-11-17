package server

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
)

// specRunner is a testRunner that keeps the spec of every start.
type specRunner struct {
	testRunner
	mu    sync.Mutex
	specs []agents.Spec
}

func (r *specRunner) Start(spec agents.Spec) (agents.Agent, error) {
	r.mu.Lock()
	r.specs = append(r.specs, spec)
	r.mu.Unlock()
	return r.testRunner.Start(spec)
}

func (r *specRunner) last(t *testing.T) agents.Spec {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.specs) == 0 {
		t.Fatal("no agent was started")
	}
	return r.specs[len(r.specs)-1]
}

// The general MCP switches reach the engine: an agent started after
// mcp.enabled or its program's mcp.inject switch is turned off gets no MCP
// bridge, and one started after it is turned on again gets it.
func TestEngineFollowsTheMCPSwitches(t *testing.T) {
	for _, cli := range []string{api.CLIClaude, api.CLICodex} {
		t.Run(cli, func(t *testing.T) {
			r := &specRunner{}
			prof := fakeProfile()
			prof.CLI = cli
			s, _ := engineServer(t, r, prof, true)
			b := newBrowser(t, s)
			start := func() string {
				t.Helper()
				wantStatus(t, asUser(b, s, "POST", "/api/ceo/new-session", empty).StatusCode, http.StatusOK)
				return r.last(t).MCPBridge
			}
			set := func(change func(*api.MCPSettings)) {
				t.Helper()
				g := s.settings.General()
				change(&g.MCP)
				if err := s.settings.SetGeneralIf("", g); err != nil {
					t.Fatal(err)
				}
				s.waitSettingsEvents(context.Background())
			}
			if start() == "" {
				t.Fatal("no bridge while MCP is on")
			}
			set(func(m *api.MCPSettings) {
				if cli == api.CLIClaude {
					m.Inject.Claude = false
				} else {
					m.Inject.Codex = false
				}
			})
			if got := start(); got != "" {
				t.Fatalf("bridge %q with the program's injection off", got)
			}
			set(func(m *api.MCPSettings) {
				*m = api.MCPSettings{Enabled: false, Inject: api.MCPInject{Claude: true, Codex: true}}
			})
			if got := start(); got != "" {
				t.Fatalf("bridge %q with MCP off", got)
			}
			set(func(m *api.MCPSettings) { m.Enabled = true })
			if start() == "" {
				t.Fatal("no bridge after MCP is on again")
			}
		})
	}
}
