package company

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/office"
)

func TestEstopPreservesUnreadyOfficeLock(t *testing.T) {
	for _, state := range []string{"starting", "stale"} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, ".omo"), 0700); err != nil {
				t.Fatal(err)
			}
			endpoint := ""
			if state == "stale" {
				endpoint = filepath.Join(dir, "missing-socket")
			}
			lock := filepath.Join(dir, office.LockPath)
			if err := os.WriteFile(lock, []byte(endpoint), 0600); err != nil {
				t.Fatal(err)
			}
			i := &Instance{info: InstanceInfo{Path: dir, Mode: "omo", State: "running"}}
			if err := estopInstance(i); err == nil || !strings.Contains(err.Error(), "not ready") {
				t.Fatalf("unready office accepted estop: %v", err)
			}
			if raw, err := os.ReadFile(lock); err != nil || string(raw) != endpoint {
				t.Fatalf("probing changed another process's lock: %q: %v", raw, err)
			}
		})
	}
}

func TestLaunchWaitsForStartingOfficeWithoutRemovingLock(t *testing.T) {
	dir := projectHome(t)
	p, err := CreateProject(context.Background(), filepath.Join(dir, "office"), "")
	if err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(p.Path, office.LockPath)
	if err := os.WriteFile(lock, nil, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{MaxAgents: 2, Mock: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.start(p.Path, "omo"); err == nil || !strings.Contains(err.Error(), "starting") {
		t.Fatalf("launched over another process's startup: %v", err)
	}
	if raw, err := os.ReadFile(lock); err != nil || len(raw) != 0 {
		t.Fatalf("launch changed startup lock: %q: %v", raw, err)
	}
}

func TestProbeLeavesStaleLocksForOfficeClaimLifecycle(t *testing.T) {
	for _, endpoint := range []string{"", "missing-endpoint"} {
		t.Run(endpoint, func(t *testing.T) {
			dir := t.TempDir()
			if endpoint != "" {
				endpoint = filepath.Join(dir, endpoint)
			}
			if err := os.Mkdir(filepath.Join(dir, ".omo"), 0700); err != nil {
				t.Fatal(err)
			}
			lock := filepath.Join(dir, office.LockPath)
			if err := os.WriteFile(lock, []byte(endpoint), 0600); err != nil {
				t.Fatal(err)
			}
			past := time.Now().Add(-time.Hour)
			if err := os.Chtimes(lock, past, past); err != nil {
				t.Fatal(err)
			}
			if got, running, err := probeOffice(dir); err != nil || running || got != endpoint {
				t.Fatalf("stale lock blocked office claim lifecycle: %q, %v, %v", got, running, err)
			}
			if raw, err := os.ReadFile(lock); err != nil || string(raw) != endpoint {
				t.Fatalf("probe reclaimed lock itself: %q, %v", raw, err)
			}
		})
	}
}
