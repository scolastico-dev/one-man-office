package supervisor

import (
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func TestPluginSnapshotAndNudgeUseSafeSupervisorBoundaries(t *testing.T) {
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
	if err := o.Sup.PluginNudge("developer-ada", "remember the workflow"); err != nil {
		t.Fatal(err)
	}
	inbox, err := o.Sup.Mail.Inbox("developer-ada")
	if err != nil || len(inbox) != 1 {
		t.Fatalf("plugin nudge inbox=%#v err=%v", inbox, err)
	}
	if inbox[0].From != bus.SystemSender || inbox[0].Subject != "Workflow reminder" || inbox[0].Body != "remember the workflow" {
		t.Fatalf("plugin nudge = %#v", inbox[0])
	}
}
