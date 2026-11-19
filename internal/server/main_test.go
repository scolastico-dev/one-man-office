package server

import (
	"os"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"github.com/scolastico-dev/one-man-office/internal/fakeagent"
	"github.com/scolastico-dev/one-man-office/internal/gittest"
	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

// TestMain lets the test binary run as the scripted fake agent when a test
// starts it as an agent process, and as the fake MCP server when the relay
// starts it as a stdio server.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "plugin-task-gate" {
		for {
			if _, err := os.Stat(os.Args[2]); err == nil {
				os.Exit(0)
			}
			time.Sleep(time.Millisecond)
		}
	}
	if len(os.Args) > 2 && os.Args[1] == "plugin-task-output" {
		os.Exit(taskOutputHelper())
	}
	if len(os.Args) > 2 && os.Args[1] == "plugin-task-probe" {
		os.Exit(taskSocketProbe())
	}
	if len(os.Args) > 1 && os.Args[1] == "plugin-task-sleeper" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if fakeagent.Enabled() {
		os.Exit(fakeagent.Main())
	}
	if fakemcp.Enabled() {
		os.Exit(fakemcp.Main())
	}
	gittest.Isolate()
	os.Exit(childenvtest.Run(m))
}
