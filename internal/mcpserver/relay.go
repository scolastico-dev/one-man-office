package mcpserver

import (
	"context"
	"encoding/json"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

// Relay offers the tools of the upstream servers omo relays.
type Relay interface {
	// Tools lists the relayed tools id may use: filtered by the servers'
	// roles, allow and deny lists and sign-in state.
	Tools(ctx context.Context, id Identity) ([]RelayedTool, error)
	// Call calls the relayed tool name for id; progress, when not nil,
	// receives the upstream's progress notifications.
	Call(ctx context.Context, id Identity, name string, args json.RawMessage, progress func(done, total float64, msg string)) (*mcp.CallToolResult, error)
	// Generation is raised on any change of a relayed tool list.
	Generation() uint64
}

// RelayedTool is an upstream tool under the name omo offers it as
// (<server>__<tool>).
type RelayedTool struct {
	Name   string
	Server string
	Tool   *mcp.Tool
}

// Options are what the MCP endpoint needs from the server.
type Options struct {
	Relay Relay
	// Dispatch returns the in-process client that runs omo's tools as the
	// agent session of id (ClassAgent only).
	Dispatch func(id Identity) *apiclient.Client
	Settings func() api.MCPSettings
	// Seen records a request of an agent session.
	Seen func(session string)
	// Listeners holds the notification streams agent sessions open with a
	// GET of the endpoint; nil switches that off.
	Listeners *Listeners
	Log       *log.Logger
	// Version is omo's version, sent as the server's version.
	Version string
}
