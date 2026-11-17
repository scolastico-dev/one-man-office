// Package fakemcp is a scripted MCP server for tests: the upstream that omo
// relays. Tests serve it over HTTP (Handler, SSEHandler) or run it over
// standard input and output as their own test binary, re-executed with
// FAKEMCP_SERVER=1:
//
//	func TestMain(m *testing.M) {
//		if fakemcp.Enabled() {
//			os.Exit(fakemcp.Main())
//		}
//		os.Exit(m.Run())
//	}
//
// The variable does not start with OMO_, because omo never hands its own
// variables to the servers it starts.
//
// Its tools: echo, whoami (process id and working folder), printenv (the
// names of its environment variables, never values), sleep (with progress),
// crash, big (a text of the given size), rpcerror (a JSON-RPC error),
// add_tool (adds the tool "added", which announces a changed list) and
// stderr (writes a text to standard error). Modes replace them with hostile
// behaviour; see the Mode constants.
package fakemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
)

// Environment variables the stdio server reads.
const (
	// EnvEnable is "1" in a process that must run as the fake server.
	EnvEnable = "FAKEMCP_SERVER"
	// EnvMode selects a Mode.
	EnvMode = "FAKEMCP_MODE"
	// EnvCount is the number of tools of ModeMany.
	EnvCount = "FAKEMCP_COUNT"
	// EnvDelay delays the start of the server, as a Go duration.
	EnvDelay = "FAKEMCP_DELAY"
)

// Mode is the behaviour of the fake server.
type Mode string

const (
	// ModeNormal offers the scripted tools.
	ModeNormal Mode = ""
	// ModeMany offers Count tools t0, t1, ...
	ModeMany Mode = "many"
	// ModeCollide offers tools whose names collide with omo's tools, with
	// each other after cleaning, or are invalid or too long.
	ModeCollide Mode = "collide"
	// ModeGarbage prints text that is not JSON-RPC on standard output and
	// never answers (stdio only).
	ModeGarbage Mode = "garbage"
	// ModeExit exits at once with status 1 (stdio only).
	ModeExit Mode = "exit"
	// ModeExtras also offers a resource and a prompt.
	ModeExtras Mode = "extras"
)

// Config configures a fake server.
type Config struct {
	Mode  Mode
	Count int
}

// Enabled reports whether this process must run as the fake server.
func Enabled() bool { return os.Getenv(EnvEnable) == "1" }

// Main runs the fake server on standard input and output.
func Main() int {
	if d, err := time.ParseDuration(os.Getenv(EnvDelay)); err == nil {
		time.Sleep(d)
	}
	cfg := Config{Mode: Mode(os.Getenv(EnvMode))}
	cfg.Count, _ = strconv.Atoi(os.Getenv(EnvCount))
	switch cfg.Mode {
	case ModeExit:
		fmt.Fprintln(os.Stderr, "fakemcp: exiting as asked")
		return 1
	case ModeGarbage:
		for i := 0; ; i++ {
			fmt.Fprintf(os.Stdout, "this is not JSON-RPC %d\n", i)
			if _, err := os.Stdin.Read(make([]byte, 1024)); err != nil {
				return 0
			}
		}
	}
	if err := New(cfg).Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, "fakemcp:", err)
		return 1
	}
	return 0
}

// Handler serves a fake server over Streamable HTTP.
func Handler(cfg Config) http.Handler {
	s := New(cfg)
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{DisableLocalhostProtection: true})
}

// SSEHandler serves a fake server over the older HTTP+SSE transport.
func SSEHandler(cfg Config) http.Handler {
	s := New(cfg)
	return mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return s }, &mcp.SSEOptions{DisableLocalhostProtection: true})
}

type textIn struct {
	Text string `json:"text,omitempty"`
}

type sleepIn struct {
	Millis int `json:"millis"`
	Steps  int `json:"steps,omitempty"`
}

type bigIn struct {
	Bytes int `json:"bytes"`
}

type empty struct{}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// New returns a fake server.
func New(cfg Config) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "fakemcp", Version: "1"}, nil)
	switch cfg.Mode {
	case ModeMany:
		for i := range cfg.Count {
			mcp.AddTool(s, &mcp.Tool{Name: fmt.Sprintf("t%d", i), Description: strings.Repeat("d", 100)}, func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
				return text("ok"), nil, nil
			})
		}
		return s
	case ModeCollide:
		for _, name := range []string{"ticket_show", "done", "a__b", "we!rd", "we?rd", "we_rd", strings.Repeat("l", 130)} {
			mcp.AddTool(s, &mcp.Tool{Name: name}, func(_ context.Context, req *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
				return text("called " + req.Params.Name), nil, nil
			})
		}
		s.AddTool(&mcp.Tool{Name: "bad_schema", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return text("ok"), nil
		})
		// The SDK refuses a tool whose input is not an object; a hostile
		// server sends one anyway.
		s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
				res, err := next(ctx, method, req)
				if list, ok := res.(*mcp.ListToolsResult); ok {
					for _, t := range list.Tools {
						if t.Name == "bad_schema" {
							t.InputSchema = json.RawMessage(`{"type":"string"}`)
						}
					}
				}
				return res, err
			}
		})
		return s
	case ModeExtras:
		s.AddResource(&mcp.Resource{URI: "fake://r", Name: "r"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{}, nil
		})
		s.AddPrompt(&mcp.Prompt{Name: "p"}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{}, nil
		})
	}
	mcp.AddTool(s, &mcp.Tool{Name: "echo", Description: "Answers the text."}, func(_ context.Context, _ *mcp.CallToolRequest, in textIn) (*mcp.CallToolResult, any, error) {
		return text("echo: " + in.Text), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "whoami", Description: "Answers the process id and working folder."}, func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
		wd, _ := os.Getwd()
		return text(fmt.Sprintf("pid=%d cwd=%s", os.Getpid(), wd)), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "printenv", Description: "Answers the names of the environment variables."}, func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
		var names []string
		for _, kv := range childenv.Inherited() {
			k, _, _ := strings.Cut(kv, "=")
			names = append(names, k)
		}
		slices.Sort(names)
		return text(strings.Join(names, "\n")), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "sleep", Description: "Sleeps, reporting progress."}, func(ctx context.Context, req *mcp.CallToolRequest, in sleepIn) (*mcp.CallToolResult, any, error) {
		steps := max(in.Steps, 1)
		for i := range steps {
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-time.After(time.Duration(in.Millis) * time.Millisecond / time.Duration(steps)):
			}
			if tok := req.Params.GetProgressToken(); tok != nil {
				req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Progress: float64(i + 1), Total: float64(steps), Message: "step"})
			}
		}
		return text("slept"), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "crash", Description: "Ends the process."}, func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
		os.Exit(3)
		return nil, nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "big", Description: "Answers a text of the given size."}, func(_ context.Context, _ *mcp.CallToolRequest, in bigIn) (*mcp.CallToolResult, any, error) {
		return text(strings.Repeat("x", in.Bytes)), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "rpcerror", Description: "Fails with a JSON-RPC error carrying the text."}, func(_ context.Context, _ *mcp.CallToolRequest, in textIn) (*mcp.CallToolResult, any, error) {
		return nil, nil, &jsonrpc.Error{Code: -32001, Message: in.Text}
	})
	mcp.AddTool(s, &mcp.Tool{Name: "stderr", Description: "Writes the text to standard error."}, func(_ context.Context, _ *mcp.CallToolRequest, in textIn) (*mcp.CallToolResult, any, error) {
		fmt.Fprintln(os.Stderr, in.Text)
		return text("written"), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "add_tool", Description: "Adds the tool added."}, func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
		mcp.AddTool(s, &mcp.Tool{Name: "added"}, func(context.Context, *mcp.CallToolRequest, empty) (*mcp.CallToolResult, any, error) {
			return text("added"), nil, nil
		})
		return text("ok"), nil, nil
	})
	return s
}
