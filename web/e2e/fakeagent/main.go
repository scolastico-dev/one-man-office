// Command fakeagent is the scripted stand-in for an agent CLI that the browser
// tests start through a configured model. It refuses to run unless
// OMO_FAKEAGENT=1 is set, so it cannot be started by accident.
package main

import (
	"fmt"
	"os"

	"github.com/scolastico-dev/one-man-office/internal/fakeagent"
)

func main() {
	if !fakeagent.Enabled() {
		fmt.Fprintf(os.Stderr, "fakeagent: refusing to run: set %s=1 to run the scripted fake agent\n", fakeagent.EnvEnable)
		os.Exit(2)
	}
	os.Exit(fakeagent.Main())
}
