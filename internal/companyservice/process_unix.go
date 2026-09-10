//go:build !windows

package companyservice

import (
	"os"
	"os/exec"
	"syscall"
)

func configureDetached(cmd *exec.Cmd)       { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func terminateDetached(process *os.Process) { _ = syscall.Kill(-process.Pid, syscall.SIGKILL) }
func replaceFile(from, to string) error     { return os.Rename(from, to) }

func secureDirectory(path string) error { return os.Chmod(path, 0700) }

func interruptDetached(process *os.Process) { _ = process.Signal(syscall.SIGTERM) }
