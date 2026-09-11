package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestSafeShutdownEmitsShutdownLifecycleBeforeAgentStop(t *testing.T) {
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, plugins.Dir, "shutdown")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"shutdown","hooks":[{"event":"shutdown","lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`omo.local_set("path", event.data.office_path); omo.local_set("reason", event.data.reason); omo.local_set("safe", event.data.safe); omo.local_set("count", (omo.local_get("count") or 0) + 1)`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })
	if err := o.Sup.beginSafeShutdown("user", "  maintenance  "); err != nil {
		t.Fatal(err)
	}
	o.Sup.EmitShutdown(false)
	var path, reason, safe, count string
	for key, target := range map[string]*string{"path": &path, "reason": &reason, "safe": &safe, "count": &count} {
		if err := o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='shutdown' AND key=?`, key).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if path != `"`+o.Dir+`"` || reason != `"maintenance"` || safe != `true` || count != `1` {
		t.Fatalf("shutdown lifecycle payload = path %q reason %q safe %q count %q", path, reason, safe, count)
	}
}

func TestOrdinaryShutdownFallbackUsesPreparedSafeState(t *testing.T) {
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, plugins.Dir, "shutdown")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"shutdown","hooks":[{"event":"shutdown","lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`omo.local_set("reason", event.data.reason); omo.local_set("safe", event.data.safe)`), 0o644); err != nil {
		t.Fatal(err)
	}
	o.Sup.mu.Lock()
	o.Sup.shutdownInProgress = true
	o.Sup.setExitReasonLocked("prepared reason")
	o.Sup.prepareShutdownLifecycleLocked(true, "prepared reason")
	o.Sup.mu.Unlock()
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })
	o.Sup.EmitShutdown(false)
	var reason, safe string
	for key, target := range map[string]*string{"reason": &reason, "safe": &safe} {
		if err := o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='shutdown' AND key=?`, key).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if reason != `"prepared reason"` || safe != `true` {
		t.Fatalf("prepared fallback payload = reason %q safe %q", reason, safe)
	}
}

func TestContextSaveVerbPersistsAuthenticatedRoleAndJob(t *testing.T) {
	o := newOffice(t, nil)
	const name = "developer-context"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "developer", Profile: "developer", JobID: 9}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, name, "working"); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, name, "context.save", proto.ContextSaveArgs{Summary: "tests pass; commit remains"}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetShutdownContext(o.DB, "developer", 9)
	if err != nil || got == nil || got.Agent != name || got.Context != "tests pass; commit remains" {
		t.Fatalf("saved context = %+v, err %v", got, err)
	}
}

func TestSafeShutdownTreatsRetainedCompletedFreelancerAsComplete(t *testing.T) {
	o := newOffice(t, nil)
	j := &queue.Job{Title: "research", Goal: "g", Role: "freelancer"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking, queue.StateMerging, queue.StateDone} {
		if err := o.Sup.Jobs.Transition(j.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	if !o.Sup.safeShutdownRoleComplete(db.Agent{Role: "freelancer", JobID: j.ID}) {
		t.Fatal("completed retained freelancer would block safe shutdown or save an unrestorable handoff")
	}
}

func TestSafeShutdownStoresTrimmedReasonBeforeEmergencyStop(t *testing.T) {
	o := newOffice(t, nil)
	if err := o.Sup.beginSafeShutdown("user", "  maintenance window  "); err != nil {
		t.Fatal(err)
	}
	if got := o.Sup.ExitReason(); got != "maintenance window" {
		t.Fatalf("exit reason = %q, want trimmed safe-shutdown reason", got)
	}
}

func TestSafeShutdownWithoutReasonKeepsOrdinaryExitReason(t *testing.T) {
	o := newOffice(t, nil)
	if err := o.Sup.beginSafeShutdown("user", "  "); err != nil {
		t.Fatal(err)
	}
	if got := o.Sup.ExitReason(); got != "" {
		t.Fatalf("exit reason = %q, want blank", got)
	}
}

func TestSafeShutdownDuplicateIsSuccessfulAndKeepsFirstRequest(t *testing.T) {
	oldTimeout := SafeShutdownTimeout
	SafeShutdownTimeout = 100 * time.Millisecond
	t.Cleanup(func() { SafeShutdownTimeout = oldTimeout })
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "freelancer-shutdown", Role: "freelancer", Profile: "freelancer"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "freelancer-shutdown", "working"); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.beginSafeShutdown("user", "first reason"); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.beginSafeShutdown("user", "second reason"); err != nil {
		t.Fatalf("duplicate safe shutdown = %v, want success", err)
	}
	if got := o.Sup.ExitReason(); got != "first reason" {
		t.Fatalf("exit reason = %q, want first reason", got)
	}
	snapshot := o.Sup.PluginSnapshot()
	if got, ok := snapshot["shutdown_in_progress"].(bool); !ok || !got {
		t.Fatalf("shutdown_in_progress = %#v, want true", snapshot["shutdown_in_progress"])
	}
	events, err := db.AllEvents(o.DB)
	if err != nil {
		t.Fatal(err)
	}
	started := 0
	for _, event := range events {
		if event.Kind == "safe_shutdown_started" {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("safe shutdown start events = %d, want 1", started)
	}
	history, err := o.Sup.Mail.History()
	if err != nil {
		t.Fatal(err)
	}
	broadcasts := 0
	for _, message := range history {
		if message.Subject == "safe shutdown requested" {
			broadcasts++
		}
	}
	if broadcasts != 1 {
		t.Fatalf("safe shutdown broadcasts = %d, want 1", broadcasts)
	}
}

func TestSafeShutdownRejectsUnauthorizedAgent(t *testing.T) {
	o := newOffice(t, nil)
	const name = "developer-shutdown"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "developer", Profile: "developer"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, name, "working"); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, name, "office.safe-shutdown", proto.SafeShutdownArgs{Reason: "no"}, nil); err == nil {
		t.Fatal("developer safe-shutdown unexpectedly succeeded")
	}
	if got := o.Sup.PluginSnapshot()["shutdown_in_progress"]; got != false {
		t.Fatalf("shutdown_in_progress = %#v after unauthorized request, want false", got)
	}
}

func TestReadyRestoresThenDeletesShutdownContext(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.SaveShutdownContext(o.DB, db.ShutdownContext{
		Role: "freelancer", JobID: 0, Agent: "freelancer-old", Context: "research gathered; send the report",
	}); err != nil {
		t.Fatal(err)
	}
	const name = "freelancer-new"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "freelancer", Profile: "freelancer", Goal: "continue"}); err != nil {
		t.Fatal(err)
	}
	response, err := o.Sup.ready(name)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := db.GetAgent(o.DB, name)
	if err != nil {
		t.Fatal(err)
	}
	if agent.ReadyPrompt != response.Prompt {
		t.Fatal("recorded ready prompt differs from response")
	}
	for _, want := range []string{"SAFE-SHUTDOWN HANDOFF", "research gathered; send the report"} {
		if !strings.Contains(response.Prompt, want) {
			t.Errorf("restored prompt missing %q:\n%s", want, response.Prompt)
		}
	}
	if got, err := db.GetShutdownContext(o.DB, "freelancer", 0); err != nil || got != nil {
		t.Fatalf("restored context was not deleted: %+v, err %v", got, err)
	}
}

func TestSafeShutdownBroadcastsAndStopsAfterAgentExits(t *testing.T) {
	oldTimeout := SafeShutdownTimeout
	SafeShutdownTimeout = 2 * time.Second
	t.Cleanup(func() { SafeShutdownTimeout = oldTimeout })
	o := newOffice(t, map[string]string{"freelancer": "ready\nwait\n"})
	name, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "wait for mail")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "agent waiting", func() bool { return agentState(t, o, name) == "waiting" })
	if err := o.Sup.BeginSafeShutdown("user"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-o.Sup.EmergencyStop():
	case <-time.After(5 * time.Second):
		t.Fatal("safe shutdown did not stop after target agent exited")
	}
	history, err := o.Sup.Mail.History()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range history {
		if message.To == name && message.Subject == "safe shutdown requested" && strings.Contains(message.Body, "omo context save") {
			found = true
		}
	}
	if !found {
		t.Fatalf("safe-shutdown broadcast missing: %+v", history)
	}
}
