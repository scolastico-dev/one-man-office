package tui

import (
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func TestJobsOverviewFiltersAndSearches(t *testing.T) {
	m := testModel(t)
	for _, job := range []*queue.Job{
		{Title: "active api", Goal: "build endpoint", Role: "developer", State: queue.StateWorking},
		{Title: "completed ui", Goal: "finish page", Role: "developer", State: queue.StateDone},
		{Title: "failed worker", Goal: "repair crash", Role: "developer", State: queue.StateFailed},
	} {
		desired := job.State
		if err := m.o.Sup.Jobs.Create(job); err != nil {
			t.Fatal(err)
		}
		if desired == queue.StateWorking {
			if err := m.o.Sup.Jobs.Transition(job.ID, queue.StateAssigned); err != nil {
				t.Fatal(err)
			}
			if err := m.o.Sup.Jobs.Transition(job.ID, queue.StateWorking); err != nil {
				t.Fatal(err)
			}
		} else if desired == queue.StateDone {
			for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking, queue.StateReview, queue.StateMerging, queue.StateDone} {
				if err := m.o.Sup.Jobs.Transition(job.ID, state); err != nil {
					t.Fatal(err)
				}
			}
		} else if desired == queue.StateFailed {
			if err := m.o.Sup.Jobs.Transition(job.ID, queue.StateFailed); err != nil {
				t.Fatal(err)
			}
		}
	}
	m.tab = tabJobs
	m.jobFilter = jobFilterCompleted
	if got := len(m.overviewJobs()); got != 1 || m.overviewJobs()[0].Title != "completed ui" {
		t.Fatalf("completed filter returned %d jobs: %#v", got, m.overviewJobs())
	}
	m.jobFilter = jobFilterAll
	m.jobSearch = "worker"
	if got := len(m.overviewJobs()); got != 1 || m.overviewJobs()[0].Title != "failed worker" {
		t.Fatalf("search returned %d jobs: %#v", got, m.overviewJobs())
	}
	if view := m.viewOverview(); !strings.Contains(view, "search: worker") || !strings.Contains(view, "failed worker") {
		t.Fatalf("job filter/search not visible:\n%s", view)
	}
}
