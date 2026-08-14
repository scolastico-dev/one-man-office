package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

// runningOfficeCaller uses the injected identity inside an agent session and
// the reserved user identity when invoked from a running office directory.
func runningOfficeCaller() (endpoint, agentID string, err error) {
	if endpoint, agentID, err = sockc.Env(); err == nil {
		return endpoint, agentID, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	endpoint, running, err := office.Running(dir)
	if err != nil {
		return "", "", err
	}
	if !running {
		return "", "", fmt.Errorf("no running omo office found at %s", filepath.Join(dir, office.LockPath))
	}
	return endpoint, "user", nil
}
