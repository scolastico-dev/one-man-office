package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
)

// maxRequestBody bounds one request to /mcp.
const maxRequestBody = 4 << 20

// relayedSeparator joins a relayed server's name and its tool's name; omo's
// own tool names never contain it.
const relayedSeparator = "__"

const (
	agentInstructions = "omo's board tools, named after the `omo ai` commands (`omo ai ticket show` is the tool ticket_show), " +
		"and the tools of the servers omo relays, named <server>__<tool>. `omo ai wait` is no tool: run it in the terminal. " +
		"Language-server issues in your worktree reach you as notifications/message on the stream you open with a GET of this endpoint " +
		"(Accept: text/event-stream), or, through `omo mcp stdio`, once you send logging/setLevel; otherwise as an `omo:` line in the terminal. " +
		"A wrong report is silenced with the tool lsp_ignore."
	noSessionInstructions = "This connection is not tied to an omo agent session, so it offers none of omo's board tools: " +
		"use the `omo ai` commands in the terminal for those. The tools here belong to the servers omo relays, named <server>__<tool>."
)

// NewHandler returns the Streamable HTTP handler of /mcp. It expects the
// request's Identity in its context. It serves without protocol sessions:
// every request builds the server of its own identity, so nothing of one
// caller is kept for another and an omo restart ends no client's session.
func NewHandler(o Options) http.Handler {
	h := &handler{o: o}
	commands() // build the tools now, not on the first request
	sdk := mcp.NewStreamableHTTPHandler(h.server, &mcp.StreamableHTTPOptions{
		Stateless: true,
		// omo checks Host and Origin itself, knowing allowed_hosts and
		// public_url.
		DisableLocalhostProtection: true,
		MaxRequestBodyBytes:        maxRequestBody,
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o.Settings != nil && !o.Settings().Enabled {
			http.Error(w, "omo's MCP endpoint is switched off", http.StatusNotFound)
			return
		}
		id, ok := IdentityFrom(r.Context())
		if !ok {
			http.Error(w, "no identity", http.StatusInternalServerError)
			return
		}
		if id.Class == ClassAgent && o.Seen != nil {
			o.Seen(id.Session)
		}
		if id.Class == ClassAgent && o.Listeners != nil && wantsStream(r) {
			o.Listeners.serve(w, r, id.Session)
			return
		}
		sdk.ServeHTTP(w, r)
	})
}

type handler struct {
	o Options
}

// server builds the MCP server of the request's identity: omo's tools the
// agent session's role sees, or none for a connection without a session,
// and the relayed tools of the identity.
func (h *handler) server(r *http.Request) *mcp.Server {
	id, _ := IdentityFrom(r.Context())
	instructions := noSessionInstructions
	if id.Class == ClassAgent {
		instructions = agentInstructions
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "omo", Version: h.o.Version}, &mcp.ServerOptions{
		Instructions: instructions,
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
		// The lists depend on who asks.
		SetCacheable: func(_ context.Context, _ mcp.Request, c *mcp.Cacheable) { c.CacheScope = "private" },
	})
	if id.Class == ClassAgent {
		a := aicmd.Audience{Role: id.Actor.Role, Purpose: id.Actor.Purpose}
		_, tools := commands()
		for _, t := range tools {
			if t.cmd.Sees(a) {
				s.AddTool(t.tool, h.runTool(id, t))
			}
		}
	}
	s.AddReceivingMiddleware(h.middleware(id))
	return s
}

// runTool runs an omo tool as the session of id, through the same routes
// as `omo ai`.
func (h *handler) runTool(id Identity, t omoTool) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		in, err := t.input(req.Params.Arguments, id.Workdir)
		if err != nil {
			return toolError(err.Error()), nil
		}
		res, err := t.cmd.Run(ctx, h.o.Dispatch(id), in)
		if err != nil {
			return toolError(aicmd.Explain(err).Error()), nil
		}
		return toolResult(res), nil
	}
}

// middleware adds the relayed tools to the list, routes their calls to the
// relay, and refuses omo's tools where they are not offered.
func (h *handler) middleware(id Identity) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "tools/list":
				res, err := next(ctx, method, req)
				if lr, ok := res.(*mcp.ListToolsResult); ok && err == nil && lr.NextCursor == "" {
					for _, t := range h.relayed(ctx, id) {
						lr.Tools = append(lr.Tools, t)
					}
				}
				return res, err
			case "tools/call":
				cr, ok := req.(*mcp.CallToolRequest)
				if !ok || cr.Params == nil {
					break
				}
				name := cr.Params.Name
				byName, _ := commands()
				if cmd, found := byName[name]; found {
					words := strings.Join(cmd.Path, " ")
					if id.Class != ClassAgent {
						return toolError(fmt.Sprintf("this MCP connection is not tied to an omo agent session, so it has no omo tools; "+
							"use `omo ai %s` in the terminal", words)), nil
					}
					if cmd.CommandLineOnly {
						return toolError(fmt.Sprintf("%s is no MCP tool; run `omo ai %s` in the terminal", name, words)), nil
					}
				}
				if strings.Contains(name, relayedSeparator) {
					return h.callRelayed(ctx, id, cr)
				}
			}
			return next(ctx, method, req)
		}
	}
}

// relayed lists the relayed tools of id under the names omo offers them
// as. A relay that cannot list them leaves them out of this answer.
func (h *handler) relayed(ctx context.Context, id Identity) []*mcp.Tool {
	if h.o.Relay == nil {
		return nil
	}
	list, err := h.o.Relay.Tools(ctx, id)
	if err != nil {
		h.logf("relayed tools left out of a list: %v", err)
		return nil
	}
	out := make([]*mcp.Tool, 0, len(list))
	for _, rt := range list {
		if rt.Tool == nil || !strings.HasPrefix(rt.Name, rt.Server+relayedSeparator) {
			continue
		}
		t := *rt.Tool
		t.Name = rt.Name
		t.Description = relayedDescription(rt.Server, rt.Tool.Description)
		out = append(out, &t)
	}
	return out
}

// callRelayed calls a relayed tool; only a tool listed for id is called.
func (h *handler) callRelayed(ctx context.Context, id Identity, req *mcp.CallToolRequest) (mcp.Result, error) {
	name := req.Params.Name
	listed := false
	for _, t := range h.relayed(ctx, id) {
		listed = listed || t.Name == name
	}
	if !listed {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("unknown tool %q", name)}
	}
	var progress func(done, total float64, msg string)
	if token := req.Params.GetProgressToken(); token != nil && req.Session != nil {
		progress = func(done, total float64, msg string) {
			_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: token, Progress: done, Total: total, Message: msg})
		}
	}
	res, err := h.o.Relay.Call(ctx, id, name, req.Params.Arguments, progress)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return toolError(fmt.Sprintf("%s gave no answer", name)), nil
	}
	return res, nil
}

func (h *handler) logf(format string, args ...any) {
	if h.o.Log != nil {
		h.o.Log.Printf("mcp: "+format, args...)
	}
}

// relayedDescription is the description of a relayed tool: omo's notice
// that the server, not omo, writes its results, then the server's own
// description. Only this endpoint adds the notice.
func relayedDescription(server, description string) string {
	notice := fmt.Sprintf("Relayed by omo from server `%s`; its results are written by that server, not by omo.", server)
	if description = strings.TrimSpace(description); description == "" {
		return notice
	}
	return notice + "\n\n" + description
}
