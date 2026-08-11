package queue

import (
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

func store(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return &Store{DB: d}
}

func TestCreateAndGet(t *testing.T) {
	s := store(t)
	j := &Job{Title: "build api", Goal: "implement /health", Role: "developer", Repo: "api"}
	if err := s.Create(j); err != nil {
		t.Fatal(err)
	}
	if j.ID == 0 {
		t.Fatal("Create must fill ID")
	}
	got, err := s.Get(j.ID)
	if err != nil || got.State != StateQueued || got.Title != "build api" {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestHappyPathTransitions(t *testing.T) {
	s := store(t)
	j := &Job{Title: "t", Goal: "g", Role: "developer"}
	s.Create(j)
	for _, st := range []State{StateAssigned, StateWorking, StateReview, StateMerging, StateDone} {
		if err := s.Transition(j.ID, st); err != nil {
			t.Fatalf("→%s: %v", st, err)
		}
	}
	// done is terminal
	if err := s.Transition(j.ID, StateQueued); err == nil {
		t.Fatal("done must be terminal")
	}
}

func TestInvalidTransitionRejected(t *testing.T) {
	s := store(t)
	j := &Job{Title: "t", Goal: "g", Role: "developer"}
	s.Create(j)
	if err := s.Transition(j.ID, StateMerging); err == nil {
		t.Fatal("queued→merging must be rejected")
	}
}

func TestReworkLoop(t *testing.T) {
	s := store(t)
	j := &Job{Title: "t", Goal: "g", Role: "developer"}
	s.Create(j)
	for _, st := range []State{StateAssigned, StateWorking, StateReview, StateRework, StateReview, StateMerging, StateDone} {
		if err := s.Transition(j.ID, st); err != nil {
			t.Fatalf("→%s: %v", st, err)
		}
	}
}

func TestTransitionWritesEvent(t *testing.T) {
	s := store(t)
	j := &Job{Title: "t", Goal: "g", Role: "developer"}
	s.Create(j)
	s.Transition(j.ID, StateAssigned)
	evs, err := db.EventsSince(s.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.Kind == "job_state" && e.JobID == j.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no job_state event written: %+v", evs)
	}
}

func TestRetriesAndFields(t *testing.T) {
	s := store(t)
	j := &Job{Title: "t", Goal: "g", Role: "developer"}
	s.Create(j)
	if n, _ := s.IncrementRetries(j.ID); n != 1 {
		t.Fatalf("retries = %d, want 1", n)
	}
	s.SetAssignee(j.ID, "developer-jason")
	s.SetNote(j.ID, RestartNote)
	s.SetWorktree(j.ID, "/tmp/wt", "omo/job-1")
	got, _ := s.Get(j.ID)
	if got.Assignee != "developer-jason" || got.Note != RestartNote || got.Branch != "omo/job-1" {
		t.Fatalf("fields not persisted: %+v", got)
	}
}

func TestRetryAtomicallyRequeuesAndIncrements(t *testing.T) {
	s := store(t)
	j := &Job{Title: "t", Goal: "g", Role: "developer"}
	s.Create(j)
	s.Transition(j.ID, StateCancelled)
	if err := s.Retry(j.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(j.ID)
	if err != nil || got.State != StateQueued || got.Retries != 1 {
		t.Fatalf("retried job = %+v, %v", got, err)
	}
	if err := s.Retry(j.ID); err == nil {
		t.Fatal("queued job must not be retried")
	}
	got, _ = s.Get(j.ID)
	if got.Retries != 1 {
		t.Fatalf("invalid retry changed count to %d", got.Retries)
	}
}
