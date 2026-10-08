package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Exec reads a prepared policy, applies it, and replaces the wrapper process.
func Exec(policyPath, command string, args []string) error {
	p, err := ReadPolicy(policyPath)
	if err != nil {
		return err
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		return fmt.Errorf("resolve sandbox command: %w", err)
	}
	resolved, err = filepath.EvalSymlinks(resolved)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(resolved) {
		return fmt.Errorf("sandbox command is not absolute: %s", resolved)
	}
	return execute(p, resolved, args, os.Environ())
}
