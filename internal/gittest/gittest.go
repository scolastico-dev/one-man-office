// Package gittest keeps tests away from the developer's git configuration.
package gittest

import (
	"fmt"
	"os"
	"strconv"
)

// Isolate makes every git process the test binary starts from now on, and
// every process those start (omo's own git, agents), read no global and no
// system git configuration and none passed in the environment: a test never
// depends on the developer's commit signing, hooks, aliases or identity
// defaults, and never fails because a signing key is locked. Call it in
// TestMain before m.Run, after any branch that runs the binary as a fake
// agent (an agent gets its environment from omo, which may pass git
// configuration on purpose).
func Isolate() {
	if n, err := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT")); err == nil {
		for i := range n {
			os.Unsetenv(fmt.Sprintf("GIT_CONFIG_KEY_%d", i))
			os.Unsetenv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", i))
		}
	}
	for _, k := range []string{"GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_CONFIG_SYSTEM"} {
		os.Unsetenv(k)
	}
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}
