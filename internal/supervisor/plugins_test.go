package supervisor

import (
	"reflect"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
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
	if got, ok := snapshot["shutdown_in_progress"].(bool); !ok || got {
		t.Fatalf("shutdown_in_progress = %#v, want false", snapshot["shutdown_in_progress"])
	}
	agents, ok := snapshot["agents"].([]any)
	if !ok || len(agents) != 1 {
		t.Fatalf("plugin snapshot = %#v", snapshot)
	}
	row := agents[0].(map[string]any)
	if row["name"] != "developer-ada" || row["job_state"] != string(queue.StateQueued) || row["unread_messages"] != 0 {
		t.Fatalf("plugin agent row = %#v", row)
	}
}

func TestPluginSnapshotIncludesUnreadUserInboxMetadataOnly(t *testing.T) {
	o := newOffice(t, nil)
	for _, agent := range []db.Agent{
		{Name: "ceo-ada", Role: "ceo", Profile: "ceo"},
		{Name: "developer-bea", Role: "developer", Profile: "developer"},
	} {
		if err := db.InsertAgent(o.DB, agent); err != nil {
			t.Fatal(err)
		}
		if err := db.SetAgentState(o.DB, agent.Name, "working"); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := o.Sup.Mail.Send("ceo-ada", "user", "normal", "normal secret", bus.PrioNormal)
	if err != nil {
		t.Fatal(err)
	}
	urgentIDs, err := o.Sup.Mail.Send("ceo-ada", "user", "urgent", "urgent secret", bus.PrioUrgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.Mail.Send("ceo-ada", "developer-bea", "agent-only", "not for user", bus.PrioHigh); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	if _, err := o.DB.Exec(`UPDATE messages SET created_at = ? WHERE id = ?`, created.Format("2006-01-02 15:04:05"), ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DB.Exec(`UPDATE messages SET created_at = ? WHERE id = ?`, created.Add(time.Minute).Format("2006-01-02 15:04:05"), urgentIDs[0]); err != nil {
		t.Fatal(err)
	}

	snapshot := o.Sup.PluginSnapshot()
	want := []any{
		map[string]any{"id": urgentIDs[0], "from": "ceo-ada", "subject": "urgent", "priority": "urgent", "created_at_unix": created.Add(time.Minute).Unix()},
		map[string]any{"id": ids[0], "from": "ceo-ada", "subject": "normal", "priority": "normal", "created_at_unix": created.Unix()},
	}
	if got := snapshot["user_inbox"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("user inbox = %#v, want %#v", got, want)
	}
	for _, entry := range snapshot["user_inbox"].([]any) {
		if _, leaked := entry.(map[string]any)["body"]; leaked {
			t.Fatal("user inbox snapshot leaked a message body")
		}
	}
	if _, err := o.Sup.Mail.Read("user", urgentIDs[0]); err != nil {
		t.Fatal(err)
	}
	updated := o.Sup.PluginSnapshot()["user_inbox"]
	wantAfterRead := []any{want[1]}
	if !reflect.DeepEqual(updated, wantAfterRead) {
		t.Fatalf("user inbox after read = %#v, want %#v", updated, wantAfterRead)
	}
}

func TestPluginSnapshotIncludesOfficeMetadata(t *testing.T) {
	o := newOffice(t, nil)
	started := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	o.Sup.mu.Lock()
	o.Sup.sessionStarted = started
	o.Sup.mu.Unlock()

	snapshot := o.Sup.PluginSnapshot()
	if got := snapshot["office_path"]; got != o.Dir {
		t.Fatalf("office path = %#v, want %q", got, o.Dir)
	}
	if got := snapshot["office_started_at_unix"]; got != started.Unix() {
		t.Fatalf("office started timestamp = %#v, want %d", got, started.Unix())
	}
}

func TestPluginSnapshotPreservesAllDownstreamFieldsOnSuccessAndFailure(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo-ada", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "ceo-ada", "working"); err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	o.Sup.mu.Lock()
	o.Sup.sessionStarted = started
	o.Sup.mu.Unlock()
	o.Sup.RecordUserInput("ceo-ada")
	snapshot := o.Sup.PluginSnapshot()
	for _, key := range []string{"user_inbox", "ceo_activity_at_unix", "office_path", "office_started_at_unix", "shutdown_in_progress"} {
		if _, ok := snapshot[key]; !ok {
			t.Fatalf("successful snapshot missing %q: %#v", key, snapshot)
		}
	}
	if snapshot["shutdown_in_progress"] != false || snapshot["office_path"] != o.Dir || snapshot["office_started_at_unix"] != started.Unix() {
		t.Fatalf("successful downstream fields = %#v", snapshot)
	}
	if activity, ok := snapshot["ceo_activity_at_unix"].(int64); !ok || activity == 0 {
		t.Fatalf("successful CEO activity = %#v", snapshot["ceo_activity_at_unix"])
	}

	failing := &Supervisor{OfficeDir: "/tmp/failed-office", shutdownInProgress: true, sessionStarted: started}
	failure := failing.PluginSnapshot()
	for _, key := range []string{"user_inbox", "ceo_activity_at_unix", "office_path", "office_started_at_unix", "shutdown_in_progress"} {
		if _, ok := failure[key]; !ok {
			t.Fatalf("fail-soft snapshot missing %q: %#v", key, failure)
		}
	}
	if failure["shutdown_in_progress"] != true || failure["office_path"] != failing.OfficeDir || failure["office_started_at_unix"] != started.Unix() {
		t.Fatalf("fail-soft downstream fields = %#v", failure)
	}
	if failure["ceo_activity_at_unix"] != int64(0) {
		t.Fatalf("fail-soft CEO activity = %#v", failure["ceo_activity_at_unix"])
	}
	if _, ok := failure["snapshot_error"].(string); !ok {
		t.Fatalf("fail-soft snapshot error = %#v", failure["snapshot_error"])
	}
}
