//go:build windows

package agents

import "time"

// EndLeftovers does nothing on Windows and returns 0: every agent runs in a
// job object that kills all its processes when omo's handle to it closes,
// which includes omo crashing, so no agent outlives omo there.
func EndLeftovers(runDir string, grace time.Duration) (int, error) { return 0, nil }

// recordProcess writes no record on Windows (see EndLeftovers).
func recordProcess(runDir, key string, pid int) error { return nil }
