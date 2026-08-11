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

func TestOverviewOrdersAgentsAsIndentedJobTree(t *testing.T) {
	o := newOffice(t, nil)
	pmJob := &queue.Job{Title: "ship feature", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pmJob); err != nil {
		t.Fatal(err)
	}
	devJob := &queue.Job{Title: "implement", Goal: "g", Role: "developer", ParentJob: pmJob.ID}
	if err := o.Sup.Jobs.Create(devJob); err != nil {
		t.Fatal(err)
	}
	for _, a := range []db.Agent{
		{Name: "ceo-ada", Role: "ceo", Profile: "ceo"},
		{Name: "pm-ben", Role: "product_manager", Profile: "product_manager", JobID: pmJob.ID},
		{Name: "freelancer-cam", Role: "freelancer", Profile: "freelancer"},
		{Name: "developer-dan", Role: "developer", Profile: "developer", JobID: devJob.ID},
		{Name: "reviewer-eve", Role: "reviewer", Profile: "reviewer", JobID: devJob.ID},
	} {
		if err := db.InsertAgent(o.DB, a); err != nil {
			t.Fatal(err)
		}
	}

	rows := o.Sup.Overview()
	wantNames := []string{"ceo-ada", "pm-ben", "developer-dan", "reviewer-eve", "freelancer-cam"}
	wantDepths := []int{0, 1, 2, 3, 1}
	if len(rows) != len(wantNames) {
		t.Fatalf("got %d rows, want %d: %+v", len(rows), len(wantNames), rows)
	}
	for i := range rows {
		if rows[i].Name != wantNames[i] || rows[i].Depth != wantDepths[i] {
			t.Fatalf("row %d = %s depth %d, want %s depth %d; all: %+v", i, rows[i].Name, rows[i].Depth, wantNames[i], wantDepths[i], rows)
		}
	}
}

func TestPersistOverallStatisticsAggregatesOneRowPerModel(t *testing.T) {
	o := newOffice(t, nil)
	for _, name := range []string{"developer-ada", "reviewer-ben"} {
		if _, err := o.DB.Exec(`INSERT INTO agents
			(name, role, profile, state, created_at, ended_at)
			VALUES (?, 'developer', 'shared', 'done', '2026-01-01 00:00:00', '2026-01-01 00:00:04')`, name); err != nil {
			t.Fatal(err)
		}
		for _, event := range []struct{ kind, at string }{
			{"agent_ready", "2026-01-01 00:00:01"},
			{"agent_waiting", "2026-01-01 00:00:03"},
			{"agent_done", "2026-01-01 00:00:04"},
		} {
			if _, err := o.DB.Exec(`INSERT INTO events (kind, agent, created_at) VALUES (?, ?, ?)`, event.kind, name, event.at); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := o.Sup.PersistOverallStatistics(); err != nil {
		t.Fatal(err)
	}
	rows, err := db.OverallStatistics(o.DB)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Model != "shared" || rows[0].AgentsStarted != 2 || rows[0].Active != 4*time.Second || rows[0].Idle != 2*time.Second {
		t.Fatalf("overall model rows = %+v", rows)
	}
}
