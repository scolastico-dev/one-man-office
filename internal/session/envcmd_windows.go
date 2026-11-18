//go:build windows

package session

import "os/exec"

// ownProcessGroup is handled by processtree.Output on Windows. The command
// joins its job before executing, and descendants end with cancellation.
func ownProcessGroup(*exec.Cmd) {}
