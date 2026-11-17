package cli

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
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
