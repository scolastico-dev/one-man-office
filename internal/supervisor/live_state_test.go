package supervisor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/company/controlplane"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

func TestLiveStateIncludesLivingAgentsAndTUIState(t *testing.T) {
	o := newOffice(t, nil)
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-ada", Role: "developer", Profile: "test", JobID: 9}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "developer-ada", "working"); err != nil {
		t.Fatal(err)
	}
	o.Sup.SetTUIState("peek", "developer-ada")
	got, err := o.Sup.LiveState()
	if err != nil {
		t.Fatal(err)
	}
	want := controlplane.LiveState{
		Agents:  []controlplane.AgentState{{Name: "developer-ada", Role: "developer", State: "working", JobID: 9, Step: ""}},
		TUI:     controlplane.TUIState{Mode: "peek", Peek: "developer-ada"},
		Actions: []controlplane.ActionState{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("live state = %#v, want %#v", got, want)
	}
}

func TestLiveStateIncludesOnlyUserManualActions(t *testing.T) {
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, plugins.Dir, "manual")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"manual","hooks":[{"event":"manual","name":"user-only","description":"User only","lua":"hook.lua"},{"event":"manual","name":"ceo-only","description":"CEO only","roles":["ceo"],"lua":"hook.lua"},{"event":"manual","name":"shared","description":"Shared","roles":["user","ceo"],"manual_args":true,"lua":"hook.lua"}]}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte("-- no-op"), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })
	state, err := o.Sup.LiveState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Actions) != 2 || state.Actions[0].Action != "user-only" || state.Actions[1].Action != "shared" || !state.Actions[1].Args {
		t.Fatalf("user actions = %#v", state.Actions)
	}
}
