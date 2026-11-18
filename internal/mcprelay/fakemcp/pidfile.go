package fakemcp

import (
	"os"
	"path/filepath"
	"strconv"
)

// EnvPIDDir names a folder in which the fake server leaves one empty file
// named by its process id when it starts, so a browser test asks the operating
// system about exactly those processes on every platform.
const EnvPIDDir = "FAKEMCP_PID_DIR"

// recordPID leaves the process's id in the folder EnvPIDDir names, if set.
func recordPID() error {
	dir := os.Getenv(EnvPIDDir)
	if dir == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), nil, 0o600)
}
