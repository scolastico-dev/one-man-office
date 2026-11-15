//go:build !windows

package agents

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/childenv"
)

// EndLeftovers ends agent processes that a previous omo run started and
// did not end (omo crashed or was killed), using the records PTYRunner
// wrote to runDir (see PTYRunner.RunDir). Call it at start, before any
// agent is started with the same run directory.
//
// For each record it checks that a process with the recorded id and the
// recorded start time still exists; only then it sends SIGTERM to the
// process group and, if the group is still there after grace (<= 0:
// DefaultLeftoverGrace), SIGKILL. A record whose process is gone or whose
// id now belongs to another process (different start time) is only
// removed. It returns how many process groups it signalled.
func EndLeftovers(runDir string, grace time.Duration) (int, error) {
	if grace <= 0 {
		grace = DefaultLeftoverGrace
	}
	records, err := readRecords(runDir)
	if err != nil {
		return 0, err
	}
	ended := 0
	var firstErr error
	for path, rec := range records {
		start, err := processStartTime(rec.PID)
		if err == nil && start == rec.Start {
			ended++
			if err := endGroup(rec.PGID, grace); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("end leftover agent %s: %w", rec.Key, err)
				continue
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && firstErr == nil {
			firstErr = err
		}
	}
	return ended, firstErr
}

// endGroup sends SIGTERM to a process group, then SIGKILL if it still
// exists after grace.
func endGroup(pgid int, grace time.Duration) error {
	if err := syscall.Kill(-pgid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(-pgid, 0), syscall.ESRCH) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// recordProcess writes the record of a started agent process; on Unix its
// process group id is its own id.
func recordProcess(runDir, key string, pid int) error {
	start, err := processStartTime(pid)
	if err != nil {
		return err
	}
	return writeRecord(runDir, processRecord{Key: key, PID: pid, PGID: pid, Start: start})
}

// processStartTime is the start time of pid as the operating system reports
// it: on Linux the start time in clock ticks after boot from /proc, elsewhere
// what ps prints. It fails if no such process exists.
func processStartTime(pid int) (string, error) {
	if runtime.GOOS == "linux" {
		stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		if err != nil {
			return "", err
		}
		// The command name may contain spaces and parentheses; the fields
		// after the last ")" are fixed: state is field 3, start time 22.
		end := strings.LastIndexByte(string(stat), ')')
		if end < 0 {
			return "", fmt.Errorf("unexpected /proc stat format")
		}
		fields := strings.Fields(string(stat)[end+1:])
		if len(fields) < 20 {
			return "", fmt.Errorf("unexpected /proc stat format")
		}
		if fields[0] == "Z" || fields[0] == "X" {
			return "", os.ErrProcessDone
		}
		return "linux:" + fields[19], nil
	}
	ps := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	ps.Env = childenv.Inherited()
	out, err := ps.Output()
	start := strings.TrimSpace(string(out))
	if err != nil || start == "" {
		return "", os.ErrProcessDone
	}
	return "ps:" + start, nil
}
