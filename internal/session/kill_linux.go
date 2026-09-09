//go:build linux

package session

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// killSessionProcess terminates every process that inherited this session's
// unguessable environment marker. Unlike parent/child traversal, the marker
// still identifies background commands after their agent exits and they are
// reparented. This is lifecycle cleanup, not a sandbox: a process can
// deliberately discard its environment before detaching.
func killSessionProcess(process *os.Process, marker string) error {
	rootPID := 0
	if process != nil {
		rootPID = process.Pid
		_ = syscall.Kill(rootPID, syscall.SIGSTOP)
		_ = syscall.Kill(-rootPID, syscall.SIGSTOP)
	}

	pids := markedSessionPIDs(marker)
	if rootPID > 0 {
		pids[rootPID] = struct{}{}
	}
	for pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGSTOP)
		_ = syscall.Kill(-pid, syscall.SIGSTOP)
	}
	// Frozen marked processes cannot spawn again. A second pass catches a
	// child that appeared while the first /proc scan was in progress.
	for pid := range markedSessionPIDs(marker) {
		pids[pid] = struct{}{}
		_ = syscall.Kill(pid, syscall.SIGSTOP)
		_ = syscall.Kill(-pid, syscall.SIGSTOP)
	}

	var firstErr error
	for pid := range pids {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && firstErr == nil {
			firstErr = err
		}
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func markedSessionPIDs(marker string) map[int]struct{} {
	pids := make(map[int]struct{})
	if marker == "" {
		return pids
	}
	want := []byte(sessionMarkerEnv + "=" + marker)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return pids
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.IndexFunc(entry.Name(), func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			continue
		}
		environ, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil {
			continue
		}
		for _, value := range bytes.Split(environ, []byte{0}) {
			if bytes.Equal(value, want) {
				if pid, err := strconv.Atoi(entry.Name()); err == nil && pid > 1 {
					pids[pid] = struct{}{}
				}
				break
			}
		}
	}
	return pids
}
