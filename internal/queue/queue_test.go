package queue

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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
	j := &Job{Title: "build api", Goal: "implement /health", Role: "developer", Repo: "api", Model: "codex-luna", ForceModel: true}
	if err := s.Create(j); err != nil {
		t.Fatal(err)
	}
	if j.ID == 0 {
		t.Fatal("Create must fill ID")
	}
	got, err := s.Get(j.ID)
	if err != nil || got.State != StateQueued || got.Title != "build api" || !got.ForceModel {
		t.Fatalf("got %+v err %v", got, err)
	}
}

func TestIntegrationBranchesRoundTripAndMergeEntries(t *testing.T) {
	s := store(t)
	j := &Job{Title: "pm", Goal: "g", Role: "product_manager"}
	if err := s.Create(j); err != nil {
		t.Fatal(err)
	}
	wantA := IntegrationBranch{Branch: "omo/pm-1", Base: "main", Worktree: "/office/.omo/worktrees/api-pm-1"}
	wantB := IntegrationBranch{Branch: "omo/pm-1", Base: "main", Worktree: "/office/.omo/worktrees/web-pm-1"}
	if err := s.SetIntegrationBranch(j.ID, "api", wantA); err != nil {
		t.Fatal(err)
	}
	if err := s.SetIntegrationBranch(j.ID, "web", wantB); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.IntegrationBranches["api"] != wantA || got.IntegrationBranches["web"] != wantB {
		t.Fatalf("integration branches = %#v", got.IntegrationBranches)
	}
	replacement := IntegrationBranch{Branch: "omo/pm-1-renamed", Base: "develop", Worktree: wantA.Worktree}
	if err := s.SetIntegrationBranch(j.ID, "api", replacement); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(j.ID)
	if err != nil || got.IntegrationBranches["api"] != replacement || got.IntegrationBranches["web"] != wantB {
		t.Fatalf("replacement lost entries: %#v, %v", got.IntegrationBranches, err)
	}
}

func TestIntegrationBranchesLegacyDefault(t *testing.T) {
	s := store(t)
	var raw string
	j := &Job{Title: "legacy", Goal: "g", Role: "developer"}
	if err := s.Create(j); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT integration_branches FROM jobs WHERE id = ?`, j.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != "{}" {
		t.Fatalf("new job integration_branches = %q, want {}", raw)
	}
	got, err := s.Get(j.ID)
	if err != nil || len(got.IntegrationBranches) != 0 {
		t.Fatalf("legacy/default map = %#v, %v", got.IntegrationBranches, err)
	}
}

func TestIntegrationBranchesDecodeErrorIdentifiesJob(t *testing.T) {
	s := store(t)
	j := &Job{Title: "bad", Goal: "g", Role: "developer"}
	if err := s.Create(j); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE jobs SET integration_branches = ? WHERE id = ?`, "{bad", j.ID); err != nil {
		t.Fatal(err)
	}
	_, err := s.Get(j.ID)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("job %d", j.ID)) {
		t.Fatalf("error = %v, want job identifier", err)
	}
}

func TestIntegrationBranchesConcurrentUpdatesPreserveEntries(t *testing.T) {
	s := store(t)
	j := &Job{Title: "pm", Goal: "g", Role: "product_manager"}
	if err := s.Create(j); err != nil {
		t.Fatal(err)
	}
	entries := map[string]IntegrationBranch{
		"api": {Branch: "pm-api", Base: "main", Worktree: "/api"},
		"web": {Branch: "pm-web", Base: "main", Worktree: "/web"},
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(entries))
	for repo, entry := range entries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.SetIntegrationBranch(j.ID, repo, entry)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Get(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	for repo, want := range entries {
		if got.IntegrationBranches[repo] != want {
			t.Fatalf("entry %q lost: %#v", repo, got.IntegrationBranches)
		}
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
