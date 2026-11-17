package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

func build(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "fakemcp")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return exe
}

// environment is the test's environment without the fake server's switch.
func environment(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if bytes.HasPrefix([]byte(kv), []byte(fakemcp.EnvEnable+"=")) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func TestRefusesWithoutEnable(t *testing.T) {
	cmd := exec.Command(build(t))
	cmd.Env = environment()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 2 {
		t.Fatalf("want exit code 2, got %v", err)
	}
	if !bytes.Contains(stderr.Bytes(), []byte(fakemcp.EnvEnable+"=1")) {
		t.Fatalf("the message does not say why: %q", stderr.String())
	}
}

func TestServesItsToolsWhenEnabled(t *testing.T) {
	cmd := exec.Command(build(t))
	cmd.Env = environment(fakemcp.EnvEnable + "=1")
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := c.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"text": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	if tc, ok := res.Content[0].(*mcp.TextContent); !ok || tc.Text != "echo: hi" {
		t.Fatalf("echo answered %#v", res.Content)
	}
}
