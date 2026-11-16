package server

import (
	"os"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/fakeagent"
	"github.com/scolastico-dev/one-man-office/internal/gittest"
)

// TestMain lets the test binary run as the scripted fake agent when a test
// starts it as an agent process.
func TestMain(m *testing.M) {
	if fakeagent.Enabled() {
		os.Exit(fakeagent.Main())
	}
	gittest.Isolate()
	os.Exit(m.Run())
}
