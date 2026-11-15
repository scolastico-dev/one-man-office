package cli

import (
	"errors"
	"testing"
)

// No test of this package looks for agent programs on the real PATH: a
// server started by a test detects nothing at its first start.
func init() { agentLookPath = nil }

// TestFirstStartDetectionSwitch pins the switch: the real omo looks for agent
// programs at a first start. Turning that into "button only" changes the
// constant and this test.
func TestFirstStartDetectionSwitch(t *testing.T) {
	if !detectAgentsAtFirstStart {
		t.Fatal("detection at a first start is off")
	}
	if firstStartDetection() != nil {
		t.Fatal("the tests' lookup is not off")
	}
	saved := agentLookPath
	t.Cleanup(func() { agentLookPath = saved })
	var asked []string
	agentLookPath = func(cmd string) (string, error) {
		asked = append(asked, cmd)
		return "", errors.New("not found")
	}
	look := firstStartDetection()
	if look == nil {
		t.Fatal("no lookup with the switch on")
	}
	if _, err := look("claude"); err == nil || len(asked) != 1 || asked[0] != "claude" {
		t.Fatalf("the lookup is not agentLookPath: %v %v", err, asked)
	}
}
