package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// recordsDir is the folder below the run directory that holds one record
// per running agent.
const recordsDir = "agents"

// DefaultLeftoverGrace is how long EndLeftovers waits after SIGTERM before
// it sends SIGKILL.
const DefaultLeftoverGrace = 5 * time.Second

// processRecord identifies an agent process across an omo restart. It holds
// no session id: Key is the agent's terminal id.
type processRecord struct {
	Key  string `json:"key"`
	PID  int    `json:"pid"`
	PGID int    `json:"pgid"`
	// Start is the process's start time as the operating system reports
	// it; together with PID it tells the process from a later one that got
	// the same id.
	Start string `json:"start"`
}

func recordPath(runDir, key string) string {
	return filepath.Join(runDir, recordsDir, key+".json")
}

// writeRecord stores rec atomically.
func writeRecord(runDir string, rec processRecord) error {
	dir := filepath.Join(runDir, recordsDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".record-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), recordPath(runDir, rec.Key))
}

// readRecords returns the records in runDir with the file each came from.
// Unreadable records are removed.
func readRecords(runDir string) (map[string]processRecord, error) {
	dir := filepath.Join(runDir, recordsDir)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records := map[string]processRecord{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read agent record %s: %w", entry.Name(), err)
		}
		var rec processRecord
		if json.Unmarshal(raw, &rec) != nil || rec.PID <= 1 || rec.PGID <= 1 {
			_ = os.Remove(path)
			continue
		}
		records[path] = rec
	}
	return records, nil
}
