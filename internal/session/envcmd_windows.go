//go:build windows

package session

import "os/exec"

// ownProcessGroup keeps the default cancellation on Windows: the command's
// process is terminated. Windows has no process group to signal; a
// grandchild it started is not waited for (see WaitDelay).
func ownProcessGroup(*exec.Cmd) {}
