package server

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// mcpLogFolder is the folder of the relayed servers' logs below the home's
// log folder, which the settings export leaves out.
const mcpLogFolder = "mcp"

// mcpCallbackBase carries the address a request reached omo by (mcpBase)
// in the context of an OAuth sign-in at an upstream server.
type mcpCallbackBase struct{}

// startMCPRelay creates the relay of the upstream MCP servers, hands it to
// /mcp and to the settings routes and starts the work that stops the
// processes of ended agent sessions. The sign-ins at upstream servers are
// kept in the database. Call it once the routes are registered.
func (s *Server) startMCPRelay() {
	s.mcp.pool = mcprelay.New(mcprelay.Options{
		Servers:    s.mcpRelayedServers,
		Configured: s.settings.MCPServer,
		Auth:       s.mcpAuth.upstream,
		Redirect:   s.mcpCallbackAddress,
		LogDir:     filepath.Join(s.opts.Layout.Logs, mcpLogFolder),
		Env:        childenv.Inherited,
		Emit:       s.mcpEmit,
		Log:        s.log,
		Version:    s.opts.Version,
	})
	s.mcp.relay, s.mcpConf.relay = s.mcp.pool, s.mcp.pool
	s.mcp.on.Store(s.mcpSettings().Enabled)
	s.mcp.wake = make(chan struct{}, 1)
	s.background(s.runMCPSessionEnds)
}

// stopMCPRelay stops every process of the relay.
func (s *Server) stopMCPRelay() {
	if s.mcp.pool != nil {
		s.mcp.pool.Close()
	}
}

// mcpRelayedServers are the servers the relay runs: none while MCP is
// switched off in the general settings.
func (s *Server) mcpRelayedServers() []api.MCPServer {
	if !s.mcpSettings().Enabled {
		return nil
	}
	return s.settings.MCPServers()
}

// withMCPCallbackBase returns the context of an upstream sign-in started by
// r: its callback returns to the address r reached omo by.
func (s *Server) withMCPCallbackBase(r *http.Request) context.Context {
	return context.WithValue(r.Context(), mcpCallbackBase{}, s.mcpBase(r))
}

// mcpCallbackAddress is omo's OAuth callback for a sign-in started with
// ctx: at the address the browser reached omo by, else at public_url, else
// on loopback.
func (s *Server) mcpCallbackAddress(ctx context.Context) string {
	base, _ := ctx.Value(mcpCallbackBase{}).(string)
	if base == "" {
		base = s.opts.Config.PublicBase()
	}
	if base == "" {
		base = "http://localhost:" + strconv.Itoa(s.opts.Config.Port)
	}
	return strings.TrimSuffix(base, "/") + "/oauth/callback"
}

// mcpGeneralChanged takes a change of the general MCP switch into use:
// switched off, the relay stops every server; switched on, they start again
// on their next use.
func (s *Server) mcpGeneralChanged(c settings.Change) {
	if c.Section != settings.SectionGeneral || c.Err != nil || s.mcp.pool == nil {
		return
	}
	on := s.mcpSettings().Enabled
	if s.mcp.on.Swap(on) == on {
		return
	}
	var names []string
	for _, m := range s.settings.MCPServers() {
		names = append(names, m.Name)
	}
	s.mcp.pool.Changed(names)
	s.mcpEmit(api.KindMCPToolsChanged, nil)
}

// wakeMCPSessionEnds asks runMCPSessionEnds to look for ended sessions; the
// board calls it after every change.
func (s *Server) wakeMCPSessionEnds() {
	select {
	case s.mcp.wake <- struct{}{}:
	default:
	}
}

// runMCPSessionEnds stops the relayed processes of the agent sessions that
// used /mcp and have ended, however they ended, until ctx ends.
func (s *Server) runMCPSessionEnds(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.mcp.wake:
		}
		s.endMCPSessions(ctx)
	}
}

// endMCPSessions stops the relayed processes of every session that used
// /mcp and is no longer live, and forgets it.
func (s *Server) endMCPSessions(ctx context.Context) {
	s.mcp.mu.Lock()
	seen := make([]string, 0, len(s.mcp.seen))
	for id := range s.mcp.seen {
		seen = append(seen, id)
	}
	s.mcp.mu.Unlock()
	if len(seen) == 0 {
		return
	}
	// A session enters seen only while it is live, so one that is missing
	// from the live list has ended.
	live, err := s.board.AgentSessions(ctx, board.User, true)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Printf("MCP relay: list the live agent sessions: %v", err)
		}
		return
	}
	alive := make(map[string]bool, len(live))
	for _, sess := range live {
		alive[sess.ID] = sess.State != api.SessionEnded
	}
	for _, id := range seen {
		if alive[id] {
			continue
		}
		s.mcp.mu.Lock()
		delete(s.mcp.seen, id)
		s.mcp.mu.Unlock()
		s.mcp.pool.EndInstance(id)
	}
}
