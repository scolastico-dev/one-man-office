// Package mcpbridge is `omo mcp stdio`: an MCP server on standard input and
// output that forwards every request to omo's /mcp on the local socket.
//
// The bridge carries the identity `omo ai` carries: the agent session of
// OMO_AGENT_ID, sent as X-Omo-Session, or none when the variable is unset.
// Nothing else names the caller, so an agent program's MCP entry holds no
// token and no session id, only the command `omo mcp stdio`. A bridge
// without a session gets none of omo's board tools; omo then tells the
// caller to use `omo ai` in the terminal.
//
// omo serves /mcp without protocol sessions, so the bridge connects again
// on the next request when omo restarted, and pushes
// notifications/tools/list_changed to its client when omo says the tools
// changed. It ends when its standard input closes.
package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

// endpoint is the address of omo's MCP endpoint on the socket; the host is
// never resolved, every connection goes through Options.Dial.
const endpoint = "http://omo/mcp"

// fallbackInstructions are the instructions of a bridge that cannot reach
// omo when its client connects; omo's own replace them otherwise.
const fallbackInstructions = "omo's board tools, named after the `omo ai` commands (`omo ai ticket show` is the tool ticket_show). " +
	"omo was not reachable when this connection started: if no omo tools are listed, use the `omo ai` commands in the terminal."

// Options configure Run.
type Options struct {
	// Dial opens a connection to omo's socket. It is called for every
	// connection, so a restarted omo is found again.
	Dial func(ctx context.Context) (net.Conn, error)
	// Session is the agent session the bridge acts as (OMO_AGENT_ID); ""
	// is a connection without a session.
	Session string
	// In and Out are the MCP client's side: newline-delimited JSON-RPC.
	In  io.Reader
	Out io.Writer
	// Log receives what goes wrong; nil discards. It never receives the
	// session id.
	Log *log.Logger
	// Version is omo's version, reported to the MCP client.
	Version string
}

// Run serves the MCP client on o.In and o.Out until its input ends or ctx
// ends.
func Run(ctx context.Context, o Options) error {
	if o.Log == nil {
		o.Log = log.New(io.Discard, "", 0)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b := &bridge{o: o}
	b.srv = mcp.NewServer(&mcp.Implementation{Name: "omo", Version: o.Version}, &mcp.ServerOptions{
		Instructions: fallbackInstructions,
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}, Logging: &mcp.LoggingCapabilities{}},
	})
	b.ctx = ctx
	b.srv.AddReceivingMiddleware(b.forward)
	b.up = &upstream{dial: o.Dial, session: o.Session, version: o.Version, progress: b.progress}
	defer b.up.close()
	go watchEvents(ctx, o.Dial, b.toolsChanged, o.Log)
	err := b.srv.Run(ctx, &mcp.IOTransport{Reader: io.NopCloser(o.In), Writer: nopWriteCloser{o.Out}})
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

type bridge struct {
	o   Options
	srv *mcp.Server
	up  *upstream
	// ctx is the bridge's own lifetime, not a request's.
	ctx context.Context

	mu sync.Mutex
	// session is the client's session, for progress and log notifications.
	session *mcp.ServerSession
	// listening is set once the client asked for notifications; the stream
	// to omo then runs until the bridge ends.
	listening bool
}

// forward answers tools/list and tools/call from omo, and puts omo's
// instructions into the answer of initialize and server/discover. Every
// other method is the bridge's own (ping, notifications, subscriptions).
func (b *bridge) forward(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if ss, ok := req.GetSession().(*mcp.ServerSession); ok {
			b.mu.Lock()
			b.session = ss
			b.mu.Unlock()
		}
		switch method {
		case "initialize", "server/discover":
			res, err := next(ctx, method, req)
			if err != nil {
				return res, err
			}
			if text, ok := b.up.instructions(ctx); ok {
				switch r := res.(type) {
				case *mcp.InitializeResult:
					r.Instructions = text
				case *mcp.DiscoverResult:
					r.Instructions = text
				}
			}
			return res, nil
		case "tools/list":
			p, _ := req.GetParams().(*mcp.ListToolsParams)
			return b.up.listTools(ctx, p)
		case "logging/setLevel":
			// A client that asks for notifications says it listens for them.
			res, err := next(ctx, method, req)
			if err == nil {
				b.startListening()
			}
			return res, err
		case "tools/call":
			p, ok := req.GetParams().(*mcp.CallToolParamsRaw)
			if !ok || p == nil {
				break
			}
			return b.up.callTool(ctx, p)
		}
		return next(ctx, method, req)
	}
}

// progress hands a progress notification of a forwarded call to the
// client; the token is the client's own, forwarded with the call.
func (b *bridge) progress(ctx context.Context, p *mcp.ProgressNotificationParams) {
	b.mu.Lock()
	ss := b.session
	b.mu.Unlock()
	if ss != nil {
		_ = ss.NotifyProgress(ctx, p)
	}
}

// changeMarker is a tool that exists only for a moment: adding and removing
// it is how the SDK's server sends notifications/tools/list_changed (to a
// legacy client on its connection, to a modern one on its
// subscriptions/listen stream). The bridge answers tools/list from omo, so
// no client ever sees it.
const changeMarker = "omo_bridge_tools_changed"

// toolsChanged tells the client that omo's tools changed.
func (b *bridge) toolsChanged() {
	b.srv.AddTool(&mcp.Tool{Name: changeMarker, InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	b.srv.RemoveTools(changeMarker)
}

// upstream is the MCP client of omo's /mcp. Its connection is made at the
// first need and made again after a failure: omo serves without protocol
// sessions, so a new connection loses nothing.
type upstream struct {
	dial     func(ctx context.Context) (net.Conn, error)
	session  string
	version  string
	progress func(context.Context, *mcp.ProgressNotificationParams)

	mu sync.Mutex
	cs *mcp.ClientSession
}

// connect returns the current connection to omo, making one if needed.
func (u *upstream) connect(ctx context.Context) (*mcp.ClientSession, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.cs != nil {
		return u.cs, nil
	}
	hc := &http.Client{Transport: &sessionTransport{session: u.session, base: &http.Transport{
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return u.dial(ctx) },
		DisableKeepAlives: true,
	}}}
	c := mcp.NewClient(&mcp.Implementation{Name: "omo-mcp-bridge", Version: u.version}, &mcp.ClientOptions{
		ProgressNotificationHandler: func(ctx context.Context, r *mcp.ProgressNotificationClientRequest) {
			if r.Params != nil {
				u.progress(ctx, r.Params)
			}
		},
	})
	cs, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc, MaxRetries: -1, DisableStandaloneSSE: true}, nil)
	if err != nil {
		return nil, unreachable(err)
	}
	u.cs = cs
	return cs, nil
}

// drop forgets cs after a failure, so the next request connects again.
func (u *upstream) drop(cs *mcp.ClientSession) {
	u.mu.Lock()
	if u.cs == cs {
		u.cs = nil
	}
	u.mu.Unlock()
	_ = cs.Close()
}

func (u *upstream) close() {
	u.mu.Lock()
	cs := u.cs
	u.cs = nil
	u.mu.Unlock()
	if cs != nil {
		_ = cs.Close()
	}
}

// instructions are omo's instructions for this bridge's identity.
func (u *upstream) instructions(ctx context.Context) (string, bool) {
	cs, err := u.connect(ctx)
	if err != nil {
		return "", false
	}
	res := cs.InitializeResult()
	if res == nil || res.Instructions == "" {
		return "", false
	}
	return res.Instructions, true
}

// listTools lists omo's tools. A broken connection is made again once:
// listing changes nothing.
func (u *upstream) listTools(ctx context.Context, p *mcp.ListToolsParams) (mcp.Result, error) {
	for attempt := 0; ; attempt++ {
		cs, err := u.connect(ctx)
		if err != nil {
			return nil, err
		}
		res, err := cs.ListTools(ctx, p)
		if err == nil {
			return res, nil
		}
		if rpcError(err) || ctx.Err() != nil {
			return nil, err
		}
		u.drop(cs)
		if attempt > 0 {
			return nil, unreachable(err)
		}
	}
}

// callTool calls one of omo's tools. A call is never sent twice: after a
// broken connection the caller is told, and the next request connects
// again.
func (u *upstream) callTool(ctx context.Context, p *mcp.CallToolParamsRaw) (mcp.Result, error) {
	cs, err := u.connect(ctx)
	if err != nil {
		return toolError(err.Error()), nil
	}
	params := &mcp.CallToolParams{Meta: p.Meta, Name: p.Name}
	if len(p.Arguments) > 0 {
		params.Arguments = p.Arguments
	}
	res, err := cs.CallTool(ctx, params)
	switch {
	case err == nil:
		return res, nil
	case rpcError(err), ctx.Err() != nil:
		return nil, err
	}
	u.drop(cs)
	return toolError(unreachable(err).Error()), nil
}

// rejectedByTransport is the SDK's code of a request its transport could
// not deliver (jsonrpc2.ErrRejected); it is no answer of omo.
const rejectedByTransport = -32005

// rpcError reports whether err is omo's JSON-RPC answer (an unknown tool,
// bad arguments) rather than a failure to reach it.
func rpcError(err error) bool {
	var re *jsonrpc.Error
	var rf *refusal
	return errors.As(err, &re) && re.Code != rejectedByTransport && !errors.As(err, &rf)
}

// unreachable words a failure to reach omo's endpoint for the agent; a
// refusal by omo keeps omo's reason.
func unreachable(err error) error {
	var rf *refusal
	if errors.As(err, &rf) {
		return rf
	}
	return fmt.Errorf("omo's MCP endpoint did not answer (%v); if omo is not running, start it; `omo ai` in the terminal does the same as these tools", err)
}

func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// sessionTransport sends the agent session with every request.
type sessionTransport struct {
	session string
	base    http.RoundTripper
}

// RoundTrip sends r as the session. omo's refusal of the request itself (an
// ended session, the endpoint switched off) is returned as an *refusal, so
// the caller learns omo's reason rather than an HTTP status.
func (t *sessionTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.session != "" {
		r = r.Clone(r.Context())
		r.Header.Set(apiclient.SessionHeader, t.session)
	}
	resp, err := t.base.RoundTrip(r)
	if err != nil || resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
		return resp, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var env api.ErrorEnvelope
	if json.Unmarshal(data, &env) == nil && env.Error != nil && env.Error.Message != "" {
		return nil, &refusal{fmt.Sprintf("%s (%s)", env.Error.Message, env.Error.Code)}
	}
	text := strings.TrimSpace(string(data))
	if len(text) > 200 || text == "" {
		text = http.StatusText(resp.StatusCode)
	}
	return nil, &refusal{text}
}

// refusal is omo's answer refusing a request to its endpoint.
type refusal struct{ reason string }

func (r *refusal) Error() string { return "omo refused the request: " + r.reason }

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// retryWait is the pause before the event stream is opened again; tests
// shorten it.
var retryWait = time.Second
