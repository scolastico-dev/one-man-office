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

func TestPromptRenderContextUsesExactKnownKeys(t *testing.T) {
	o := newPromptContextOffice(t)
	readKeys := func(t *testing.T) string {
		t.Helper()
		var encoded string
		if err := o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='prompt-context' AND key='keys'`).Scan(&encoded); err != nil {
			t.Fatal(err)
		}
		var keys string
		if err := json.Unmarshal([]byte(encoded), &keys); err != nil {
			t.Fatal(err)
		}
		return keys
	}
	repo := devRepo(t)
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo, MergeTarget: config.MergeTargetAsIs}

	pm := &queue.Job{Title: "PM before repo", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	pmAgent := &db.Agent{Name: "pm-context", Role: "product_manager", JobID: pm.ID}
	if _, err := o.Sup.renderPromptPlugins("pm", pmAgent); err != nil {
		t.Fatal(err)
	}
	if got := readKeys(t); got != "merge_target" {
		t.Fatalf("PM-before-repo prompt keys = %q, want merge_target", got)
	}

	developer := &queue.Job{Title: "repo override", Goal: "g", Role: "developer", Repo: "api"}
	if err := o.Sup.Jobs.Create(developer); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetWorktree(developer.ID, "/trusted/worktree", "omo/job-prompt"); err != nil {
		t.Fatal(err)
	}
	developer, err := o.Sup.Jobs.Get(developer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.renderPromptPlugins("developer", &db.Agent{Name: "developer-context", Role: "developer", JobID: developer.ID}); err != nil {
		t.Fatal(err)
	}
	if got := readKeys(t); got != "base_branch,branch,merge_target,repo" {
		t.Fatalf("repo prompt keys = %q, want base_branch,branch,merge_target,repo", got)
	}

	if err := o.Sup.Jobs.SetIntegrationBranch(pm.ID, "api", queue.IntegrationBranch{Branch: "omo/pm-context", Base: "integration-base", Worktree: "/trusted/pm-worktree"}); err != nil {
		t.Fatal(err)
	}
	child := &queue.Job{Title: "PM child", Goal: "g", Role: "developer", Repo: "api", ParentJob: pm.ID}
	if err := o.Sup.Jobs.Create(child); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetWorktree(child.ID, "/trusted/child-worktree", "omo/job-child-prompt"); err != nil {
		t.Fatal(err)
	}
	child, err = o.Sup.Jobs.Get(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.renderPromptPlugins("child", &db.Agent{Name: "child-context", Role: "developer", JobID: child.ID}); err != nil {
		t.Fatal(err)
	}
	if got := readKeys(t); got != "base_branch,branch,merge_target,repo" {
		t.Fatalf("PM-child prompt keys = %q, want base_branch,branch,merge_target,repo", got)
	}
	var encoded string
	if err := o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='prompt-context' AND key='base'`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var base string
	if err := json.Unmarshal([]byte(encoded), &base); err != nil {
		t.Fatal(err)
	}
	if base != "omo/pm-context" {
		t.Fatalf("PM-child base_branch = %q, want parent integration branch", base)
	}
	if err := o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='prompt-context' AND key='target'`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var target string
	if err := json.Unmarshal([]byte(encoded), &target); err != nil {
		t.Fatal(err)
	}
	if target != config.MergeTargetAutoMerge {
		t.Fatalf("PM-child merge_target = %q, want %q", target, config.MergeTargetAutoMerge)
	}
	rolePrompt, err := o.Sup.renderRolePrompt("child-context", "developer", "ship it", child.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rolePrompt, "merged automatically") || strings.Contains(rolePrompt, "left for a pull request") {
		t.Fatalf("PM-child role prompt exposed the repository policy: %s", rolePrompt)
	}
}

func TestReadyIgnoresPromptRenderHookFailure(t *testing.T) {
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, ".omo", "plugins", "prompt-failure")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(plugins.Manifest{Name: "prompt-failure", Hooks: []plugins.Hook{{Event: plugins.EventPromptRender, Lua: "hook.lua"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`error("optional prompt hook failed")`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-failure", Role: "developer", Profile: "developer", Goal: "ship it"}); err != nil {
		t.Fatal(err)
	}
	response, err := o.Sup.ready("developer-failure")
	if err != nil {
		t.Fatal(err)
	}
	if response.Prompt == "" {
		t.Fatal("ready returned an empty prompt after optional hook failure")
	}
	var failures int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_error' AND detail LIKE '%prompt-failure%'`).Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures == 0 {
		t.Fatal("prompt hook failure was not logged")
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

func newPromptContextOffice(t *testing.T) *office {
	t.Helper()
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, ".omo", "plugins", "prompt-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(plugins.Manifest{Name: "prompt-context", Hooks: []plugins.Hook{{Event: plugins.EventPromptRender, Lua: "hook.lua"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	hook := `local keys = {}; for key, _ in pairs(event.data) do if key ~= "role" and key ~= "agent" and key ~= "job_id" and key ~= "text" and key ~= "at" and key ~= "at_unix" then table.insert(keys, key) end end; table.sort(keys); omo.local_set("keys", table.concat(keys, ",")); omo.local_set("base", tostring(event.data.base_branch)); omo.local_set("target", tostring(event.data.merge_target))`
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(hook), 0o644); err != nil {
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
