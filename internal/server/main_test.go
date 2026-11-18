package server

import (
	"os"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"github.com/scolastico-dev/one-man-office/internal/fakeagent"
	"github.com/scolastico-dev/one-man-office/internal/gittest"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

// TestMain lets the test binary run as the scripted fake agent when a test
// starts it as an agent process, and as the fake MCP server when the relay
// starts it as a stdio server.
func TestMain(m *testing.M) {
	if fakeagent.Enabled() {
		os.Exit(fakeagent.Main())
	}
	if fakemcp.Enabled() {
		os.Exit(fakemcp.Main())
	}
	gittest.Isolate()
	os.Exit(childenvtest.Run(m))
}
