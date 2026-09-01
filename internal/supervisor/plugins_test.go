package supervisor

import (
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func TestPluginSnapshotUsesSafeSupervisorBoundary(t *testing.T) {
	o := newOffice(t, nil)
	job := &queue.Job{Title: "plugin task", Goal: "build", Role: "developer"}
	if err := o.Sup.Jobs.Create(job); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{
		Name: "developer-ada", Role: "developer", Profile: "developer", JobID: job.ID, Goal: job.Goal,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "developer-ada", "working"); err != nil {
		t.Fatal(err)
	}
	snapshot := o.Sup.PluginSnapshot()
	agents, ok := snapshot["agents"].([]any)
	if !ok || len(agents) != 1 {
		t.Fatalf("plugin snapshot = %#v", snapshot)
	}
	row := agents[0].(map[string]any)
	if row["name"] != "developer-ada" || row["job_state"] != string(queue.StateQueued) || row["unread_messages"] != 0 {
		t.Fatalf("plugin agent row = %#v", row)
	}
}
