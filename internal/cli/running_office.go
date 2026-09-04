package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/scolastico-dev/one-man-office/internal/bus"
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
	pluginOffice := os.Getenv("OMO_OFFICE_DIR")
	if pluginOffice != "" {
		dir = pluginOffice
	}
	endpoint, running, err := office.Running(dir)
	if err != nil {
		return "", "", err
	}
	if !running {
		return "", "", fmt.Errorf("no running omo office found at %s", filepath.Join(dir, office.LockPath))
	}
	if pluginOffice != "" && os.Getenv("OMO_PLUGIN_NAME") != "" {
		return endpoint, bus.SystemSender, nil
	}
	return endpoint, "user", nil
}
