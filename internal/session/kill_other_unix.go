//go:build !windows && !linux

package session

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Non-Linux Unix systems do not expose descendant environments through /proc.
// Freeze the session leader, snapshot its descendants, then kill every observed
// process and process group before the leader can spawn more work.
func killSessionProcess(process *os.Process, _ string) error {
	if process == nil {
		return nil
	}
	if err := process.Signal(syscall.SIGSTOP); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	_ = syscall.Kill(-process.Pid, syscall.SIGSTOP)
	children := map[int][]int{}
	if output, err := exec.Command("ps", "-A", "-o", "pid=", "-o", "ppid=").Output(); err == nil {
		for _, line := range strings.Split(string(output), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			pid, pidErr := strconv.Atoi(fields[0])
			parent, parentErr := strconv.Atoi(fields[1])
			if pidErr == nil && parentErr == nil {
				children[parent] = append(children[parent], pid)
			}
		}
	}
	var freeze func(int)
	freeze = func(pid int) {
		_ = syscall.Kill(-pid, syscall.SIGSTOP)
		_ = syscall.Kill(pid, syscall.SIGSTOP)
		for _, child := range children[pid] {
			freeze(child)
		}
	}
	var kill func(int)
	kill = func(pid int) {
		for _, child := range children[pid] {
			kill(child)
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	for _, child := range children[process.Pid] {
		freeze(child)
	}
	for _, child := range children[process.Pid] {
		kill(child)
	}
	_ = syscall.Kill(-process.Pid, syscall.SIGKILL)
	err := process.Kill()
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
