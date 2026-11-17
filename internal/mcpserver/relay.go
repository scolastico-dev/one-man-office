package mcpserver

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

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
	Log  *log.Logger
}

// NewHandler returns the Streamable HTTP handler of /mcp. It expects the
// request's Identity in its context.
func NewHandler(o Options) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "the MCP endpoint is not available yet", http.StatusNotImplemented)
	})
}
