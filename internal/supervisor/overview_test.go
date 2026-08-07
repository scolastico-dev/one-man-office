package supervisor

import (
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func TestOverviewListsAgentsWithJobTitles(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|30s\n",
	})
	startDispatch(t, o)
	j := &queue.Job{Title: "research kafka", Goal: "g", Role: "freelancer"}
	o.Sup.Jobs.Create(j)
	o.Sup.kickDispatch()
	waitFor(t, 15*time.Second, "agent visible in overview", func() bool {
		rows := o.Sup.Overview()
		for _, r := range rows {
			if r.Role == "freelancer" && r.JobTitle == "research kafka" && r.State == "working" {
				return true
			}
		}
		return false
	})
	name := onlyAgentOfRole(t, o, "freelancer")
	if err := db.SetAgentStep(o.DB, name, "comparing Kafka clients"); err != nil {
		t.Fatal(err)
	}
	rows := o.Sup.Overview()
	foundStep := false
	for _, r := range rows {
		if r.Name == name && r.Step == "comparing Kafka clients" {
			foundStep = true
		}
	}
	if !foundStep {
		t.Fatalf("overview did not expose current step: %+v", rows)
	}
	queued, running := o.Sup.QueueStats()
	if queued != 0 || running != 1 {
		t.Fatalf("stats = %d queued %d running", queued, running)
	}
}
