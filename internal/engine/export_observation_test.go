package engine

import (
	"path/filepath"
	"testing"
)

func TestExportObservationCannotReplaceALaterPush(t *testing.T) {
	s := &pinStore{path: filepath.Join(t.TempDir(), pinsFile)}
	current := pushRecord{Commit: "new-reviewed-commit", Round: 2, Branch: "feature", Target: "new-target", Address: "new-destination"}
	if err := s.setPushed(1, "app", current); err != nil {
		t.Fatal(err)
	}
	old := pushRecord{Commit: "old-reviewed-commit", Round: 1, Branch: "feature", Target: "old-target", Address: "old-destination", ObservedCommit: "changed-tip", ObservedStart: "changed-base"}
	if err := s.observeExport(1, "app", old); err != nil {
		t.Fatal(err)
	}
	if got, _, err := s.pushed(1, "app"); err != nil || got != current {
		t.Fatalf("stale observation replaced the new push: %+v, %v", got, err)
	}
	current.ObservedCommit, current.ObservedStart = "new-tip", "new-base"
	if err := s.observeExport(1, "app", current); err != nil {
		t.Fatal(err)
	}
	reloaded := &pinStore{path: s.path}
	if got, _, err := reloaded.pushed(1, "app"); err != nil || got != current {
		t.Fatalf("current evidence did not survive reload: %+v, %v", got, err)
	}
}
