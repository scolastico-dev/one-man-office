package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

func TestMCPStatusWithoutSession(t *testing.T) {
	startAI(t)
	out := mustExecute(t, "mcp", "status")
	contains(t, out, "connection: not tied to an omo agent session")
	contains(t, out, "use the `omo ai` commands in the terminal")
	contains(t, out, "tools (0): none")
}

func TestMCPStatusOfAnAgentSession(t *testing.T) {
	e := startAI(t)
	s, err := e.board.CreateSession(context.Background(), board.NewSession{Role: api.RoleCEO, Name: "Ada", Model: "m/x", Workdir: t.TempDir(), LogPath: t.TempDir() + "/l"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_AGENT_ID", s.ID)
	out := mustExecute(t, "mcp", "status")
	contains(t, out, "connection: the agent session of OMO_AGENT_ID")
	contains(t, out, "    whoami\n")
	if strings.Contains(out, s.ID) || strings.Contains(out, "    wait\n") {
		t.Fatalf("status:\n%s", out)
	}
}

// `omo mcp stdio` answers an MCP client on its standard input and output
// and ends with its input.
func TestMCPStdioServesItsInput(t *testing.T) {
	startAI(t)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	cmd := Root("test")
	cmd.SetIn(inR)
	cmd.SetOut(outW)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"mcp", "stdio"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute(); outW.Close() }()
	lines := bufio.NewScanner(outR)
	send := func(msg string) string {
		t.Helper()
		if _, err := io.WriteString(inW, msg+"\n"); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(msg, `"id"`) {
			return ""
		}
		if !lines.Scan() {
			t.Fatalf("no answer to %s", msg)
		}
		return lines.Text()
	}
	contains(t, send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`),
		"not tied to an omo agent session")
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	contains(t, send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"done","arguments":{}}}`),
		"use `omo ai done` in the terminal")
	inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// `omo mcp token` creates a token through the running omo, prints its
// secret once, lists it without the secret and revokes it; an agent
// session may do none of it.
func TestMCPTokenCommands(t *testing.T) {
	e := startAI(t)
	out := mustExecute(t, "mcp", "token", "create", "laptop", "--expires", "30d")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	secret := lines[len(lines)-1]
	if !strings.HasPrefix(secret, "omo_pat_") || !strings.Contains(out, "Token laptop created") {
		t.Fatalf("create:\n%s", out)
	}
	list := mustExecute(t, "mcp", "token", "list")
	contains(t, list, "pat")
	contains(t, list, "laptop")
	if strings.Contains(list, secret) || strings.Contains(list, "expires never") {
		t.Fatalf("list:\n%s", list)
	}
	id := strings.Fields(list)[0]
	contains(t, mustExecute(t, "mcp", "token", "revoke", id), "Revoked "+id)
	if _, err := execute(context.Background(), "mcp", "token", "revoke", id); err == nil {
		t.Fatal("a second revoke succeeded")
	}
	if _, err := execute(context.Background(), "mcp", "token", "create", "x", "--expires", "soon"); err == nil {
		t.Fatal("a bad --expires was accepted")
	}

	s, err := e.board.CreateSession(context.Background(), board.NewSession{Role: api.RoleCEO, Name: "Ada", Model: "m/x", Workdir: t.TempDir(), LogPath: t.TempDir() + "/l"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_AGENT_ID", s.ID)
	if out, err := execute(context.Background(), "mcp", "token", "create", "agent"); err == nil {
		t.Fatalf("an agent session created a token:\n%s", out)
	}
}

func TestParseExpiry(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.Local)
	for in, want := range map[string]time.Time{
		"90d":        now.AddDate(0, 0, 90),
		"12h":        now.Add(12 * time.Hour),
		"2027-01-31": time.Date(2027, 1, 31, 0, 0, 0, 0, time.Local),
	} {
		if got, err := parseExpiry(in, now); err != nil || !got.Equal(want) {
			t.Errorf("%s: %v %v, want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "0d", "-1h", "tomorrow", "31.01.2027"} {
		if _, err := parseExpiry(bad, now); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// fakeClientsOmo answers the agent program routes on a socket of its own,
// so no test reaches real agent programs; it records the applied plans.
type fakeClientsOmo struct {
	mu      sync.Mutex
	applied []string
}

func startFakeClientsOmo(t *testing.T) *fakeClientsOmo {
	t.Helper()
	isolate(t)
	asUser(t)
	f := &fakeClientsOmo{}
	dir, err := os.MkdirTemp("", "omo-mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	actual, _, cleanup, err := transport.Endpoint(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := transport.Listen(actual)
	if err != nil {
		t.Fatal(err)
	}
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
	targets := []api.MCPClientTarget{
		{ID: "claude", CLI: api.CLIClaude, ConfigPath: "/home/u/.claude.json"},
		{ID: "claude@work", CLI: api.CLIClaude, Account: "work", ConfigPath: "/home/u/work/.claude.json"},
		{ID: "codex", CLI: api.CLICodex, ConfigPath: "/home/u/.codex/config.toml"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/mcp/clients", func(w http.ResponseWriter, r *http.Request) {
		var out []api.MCPClientState
		for _, tg := range targets {
			out = append(out, api.MCPClientState{Target: tg, State: api.MCPClientAbsent})
		}
		reply(w, out)
	})
	mux.HandleFunc("POST /api/mcp/clients/{target}/plan", func(w http.ResponseWriter, r *http.Request) {
		var req api.MCPClientPlanRequest
		json.NewDecoder(r.Body).Decode(&req)
		id := r.PathValue("target")
		p := api.MCPClientPlan{Target: api.MCPClientTarget{ID: id}, Action: req.Action, File: "/home/u/" + id, Hash: "hash-" + req.Action + "-" + id}
		if id == "codex" && req.Action == api.MCPClientRemove {
			p.Message = "omo is not registered here."
		} else {
			p.Commands = [][]string{{"claude", "mcp", req.Action, "omo", "--", "/usr/bin/omo mcp", "stdio"}}
		}
		reply(w, p)
	})
	mux.HandleFunc("POST /api/mcp/clients/{target}/apply", func(w http.ResponseWriter, r *http.Request) {
		var req api.MCPClientApply
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.applied = append(f.applied, req.Plan)
		f.mu.Unlock()
		reply(w, api.MCPClientApplied{State: api.MCPClientState{State: api.MCPClientInstalled}, Backup: "/home/u/backup"})
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close(); cleanup() })
	t.Setenv("OMO_SOCKET", actual)
	return f
}

func (f *fakeClientsOmo) plans() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.applied)
}

// `omo mcp install` and `uninstall` show the plans of the chosen targets
// and apply them only with --yes.
func TestMCPInstallCommands(t *testing.T) {
	f := startFakeClientsOmo(t)
	out := mustExecute(t, "mcp", "install", "claude")
	contains(t, out, "claude (/home/u/claude):")
	contains(t, out, "claude@work (/home/u/claude@work):")
	contains(t, out, `runs: claude mcp install omo -- "/usr/bin/omo mcp" stdio`)
	contains(t, out, "Run again with --yes")
	if strings.Contains(out, "codex") || len(f.plans()) != 0 {
		t.Fatalf("install claude:\n%s\napplied %v", out, f.plans())
	}
	out = mustExecute(t, "mcp", "install", "claude@work", "--yes")
	contains(t, out, "done: installed")
	if got := f.plans(); !slices.Equal(got, []string{"hash-install-claude@work"}) {
		t.Fatalf("applied %v", got)
	}
	out = mustExecute(t, "mcp", "uninstall", "--yes")
	contains(t, out, "omo is not registered here.")
	if got := f.plans(); !slices.Equal(got, []string{"hash-install-claude@work", "hash-remove-claude", "hash-remove-claude@work"}) {
		t.Fatalf("applied %v", got)
	}
	if _, err := execute(context.Background(), "mcp", "install", "gemini"); err == nil || !strings.Contains(err.Error(), "claude, claude@work, codex") {
		t.Fatalf("unknown target: %v", err)
	}
}
