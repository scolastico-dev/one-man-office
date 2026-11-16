// Package gittest keeps tests away from the developer's git configuration.
package gittest

import (
	"os"
	"strings"
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
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "GIT_CONFIG" || k == "GIT_CONFIG_PARAMETERS" || k == "GIT_CONFIG_COUNT" || k == "GIT_CONFIG_SYSTEM" ||
			strings.HasPrefix(k, "GIT_CONFIG_KEY_") || strings.HasPrefix(k, "GIT_CONFIG_VALUE_") {
			os.Unsetenv(k)
		}
	}
	os.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}
