package company

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestBrowserTerminalInputQueue(t *testing.T) {
	runBrowserNodeTest(t, "terminal_input.test.cjs", "browser input tests")
}

func TestBrowserPluginAPI(t *testing.T) {
	runBrowserNodeTest(t, "assets/app.test.cjs", "browser plugin API tests")
}

func runBrowserNodeTest(t *testing.T, script, label string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("Node.js is not installed; run node --test %s to check the browser tests", script)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, node, "--test", script).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", label, err, output)
	}
}

func runBrowserNodeTestWithEnv(t *testing.T, script, label string, environment ...string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("Node.js is not installed; run node --test %s to check the browser tests", script)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--test", script)
	cmd.Env = append(os.Environ(), environment...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", label, err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}
