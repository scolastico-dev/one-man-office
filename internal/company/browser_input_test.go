package company

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestBrowserTerminalInputQueue(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is not installed; run node --test terminal_input.test.cjs to check the browser queue")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(ctx, node, "--test", "terminal_input.test.cjs").CombinedOutput(); err != nil {
		t.Fatalf("browser input tests: %v\n%s", err, output)
	}
}
