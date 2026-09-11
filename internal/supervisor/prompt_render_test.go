package supervisor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func TestReadyAppliesPromptRenderMutationToOrdinaryRole(t *testing.T) {
	o := newPromptRenderOffice(t)
	const name = "developer-prompt"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "developer", Profile: "developer", Goal: "ship it"}); err != nil {
		t.Fatal(err)
	}

	response, err := o.Sup.ready(name)
	if err != nil {
		t.Fatal(err)
	}
	assertReadyPromptMutation(t, o, name, response.Prompt)
}

func TestReadyAppliesPromptRenderMutationAfterRestoringShutdownContext(t *testing.T) {
	o := newPromptRenderOffice(t)
	if err := db.SaveShutdownContext(o.DB, db.ShutdownContext{Role: "developer", Agent: "developer-old", Context: "resume the handoff"}); err != nil {
		t.Fatal(err)
	}
	const name = "developer-restored"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "developer", Profile: "developer", Goal: "ship it"}); err != nil {
		t.Fatal(err)
	}

	response, err := o.Sup.ready(name)
	if err != nil {
		t.Fatal(err)
	}
	assertReadyPromptMutation(t, o, name, response.Prompt)
	if !strings.Contains(response.Prompt, "resume the handoff") {
		t.Fatalf("restored prompt lost handoff context: %s", response.Prompt)
	}
}

func TestReadyAppliesPromptRenderMutationToBranchNamer(t *testing.T) {
	o := newPromptRenderOffice(t)
	const name = "branch-namer-prompt"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "branch_namer", Profile: "branch_namer", JobID: 9, Goal: "name this branch"}); err != nil {
		t.Fatal(err)
	}

	response, err := o.Sup.ready(name)
	if err != nil {
		t.Fatal(err)
	}
	assertReadyPromptMutation(t, o, name, response.Prompt)
}

func TestPreviewPromptSkipsStatefulPromptRenderHooks(t *testing.T) {
	o := newPromptRenderOffice(t)
	prompt, err := o.Sup.PreviewPrompt("developer", "preview goal")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "[prompt-mutated]") {
		t.Fatalf("preview ran a potentially stateful prompt hook: %q", prompt)
	}
}

func TestRolePromptUsesOfficeDefaultBeforeRepoOverride(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.Cfg.Branches.MergeTarget = config.MergeTargetAsIs
	pmPrompt, err := o.Sup.renderRolePrompt("pm-preview", "product_manager", "choose a repo", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pmPrompt, "left for a pull request") {
		t.Fatalf("PM prompt did not use office default: %s", pmPrompt)
	}
	o.Sup.Cfg.Repos["demo"] = config.Repository{Path: t.TempDir(), MergeTarget: config.MergeTargetAutoMerge}
	job := &queue.Job{Title: "override", Goal: "g", Role: "developer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(job); err != nil {
		t.Fatal(err)
	}
	developerPrompt, err := o.Sup.renderRolePrompt("developer-preview", "developer", "ship it", job.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(developerPrompt, "merged automatically") || strings.Contains(developerPrompt, "left for a pull request") {
		t.Fatalf("repo override was not applied: %s", developerPrompt)
	}
}

func newPromptRenderOffice(t *testing.T) *office {
	t.Helper()
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, ".omo", "plugins", "prompt-render")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(plugins.Manifest{Name: "prompt-render", Hooks: []plugins.Hook{{Event: plugins.EventPromptRender, Lua: "hook.lua"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`event.data.text = event.data.text .. " [prompt-mutated]"`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })
	return o
}

func assertReadyPromptMutation(t *testing.T, o *office, name, prompt string) {
	t.Helper()
	agent, err := db.GetAgent(o.DB, name)
	if err != nil {
		t.Fatal(err)
	}
	if agent.ReadyPrompt != prompt {
		t.Fatalf("ready response and agents.ready_prompt differ: response=%q stored=%q", prompt, agent.ReadyPrompt)
	}
	if strings.Count(prompt, "[prompt-mutated]") != 1 {
		t.Fatalf("prompt mutation count = %d, prompt=%q", strings.Count(prompt, "[prompt-mutated]"), prompt)
	}
}
