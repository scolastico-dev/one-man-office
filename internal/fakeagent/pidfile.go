package fakeagent

import (
	"os"
	"path/filepath"
	"strconv"
)

// EnvPIDDir names a folder in which the fake agent leaves one empty file named
// by its process id when it starts. A browser test then knows exactly which
// processes the agents of its server were, and asks the operating system
// whether each still exists: a proof of ended agents that reads no other
// process's environment and so works on every platform.
const EnvPIDDir = "OMO_FAKE_PID_DIR"

// recordPID leaves the process's id in the folder EnvPIDDir names, if set.
func recordPID() error {
	dir := os.Getenv(EnvPIDDir)
	if dir == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), nil, 0o600)
}
