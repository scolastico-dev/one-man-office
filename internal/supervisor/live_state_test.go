package supervisor

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/company/controlplane"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/queue"
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

func TestLiveStateMirrorsOverviewAgentTree(t *testing.T) {
	o := newOffice(t, nil)
	pmJob := &queue.Job{Title: "ship feature", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pmJob); err != nil {
		t.Fatal(err)
	}
	devJob := &queue.Job{Title: "implement", Goal: "g", Role: "developer", ParentJob: pmJob.ID}
	if err := o.Sup.Jobs.Create(devJob); err != nil {
		t.Fatal(err)
	}
	freelancerJob := &queue.Job{Title: "research", Goal: "g", Role: "freelancer"}
	if err := o.Sup.Jobs.Create(freelancerJob); err != nil {
		t.Fatal(err)
	}
	for _, a := range []db.Agent{
		{Name: "ceo-ada", Role: "ceo", Profile: "ceo"},
		{Name: "pm-ben", Role: "product_manager", Profile: "product_manager", JobID: pmJob.ID},
		{Name: "freelancer-cam", Role: "freelancer", Profile: "freelancer", JobID: freelancerJob.ID},
		{Name: "developer-dan", Role: "developer", Profile: "developer", JobID: devJob.ID},
		{Name: "reviewer-eve", Role: "reviewer", Profile: "reviewer", JobID: devJob.ID},
	} {
		if err := db.InsertAgent(o.DB, a); err != nil {
			t.Fatal(err)
		}
	}

	overview := o.Sup.Overview()
	state, err := o.Sup.LiveState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Agents) != len(overview) {
		t.Fatalf("live agents = %d, overview rows = %d", len(state.Agents), len(overview))
	}
	wantJobs := map[string]int64{
		"ceo-ada":        0,
		"pm-ben":         pmJob.ID,
		"developer-dan":  devJob.ID,
		"reviewer-eve":   devJob.ID,
		"freelancer-cam": freelancerJob.ID,
	}
	for i, row := range overview {
		got := state.Agents[i]
		if row.JobID != wantJobs[row.Name] {
			t.Fatalf("overview row %d has job_id %d, want %d: %#v", i, row.JobID, wantJobs[row.Name], row)
		}
		if got.Name != row.Name || got.Parent != row.Parent || got.Depth != row.Depth || got.JobID != row.JobID {
			t.Fatalf("live agent %d = %#v, overview row = %#v", i, got, row)
		}
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
