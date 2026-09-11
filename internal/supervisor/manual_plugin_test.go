package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestManualPluginSocketAuthorizesOnlyUserAndReturnsErrors(t *testing.T) {
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, plugins.Dir, "manual")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"manual","hooks":[{"event":"manual","name":"run","description":"Run action","manual_args":true,"lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`if event.data.args[1] == "fail" then error("requested failure") end; omo.local_set("ran", true)`), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	o.Sup.Plugins, err = plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"ceo", "developer", "firefighter"} {
		if err := db.InsertAgent(o.DB, db.Agent{Name: role, Role: role, Profile: "test"}); err != nil {
			t.Fatal(err)
		}
		if err := db.SetAgentState(o.DB, role, "working"); err != nil {
			t.Fatal(err)
		}
	}
	for _, caller := range []string{"ceo", "developer", "firefighter", "omo", "unknown"} {
		if err := sockc.Call(o.Sup.SocketPath, caller, "plugin.trigger", map[string]any{"name": "manual", "action": "run"}, nil); err == nil {
			t.Errorf("accepted %s", caller)
		}
		if err := sockc.Call(o.Sup.SocketPath, caller, "plugin.actions", proto.AgentNameArgs{}, nil); err == nil {
			t.Errorf("allowed discovery as %s", caller)
		}
	}
	var actions []proto.PluginAction
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.actions", proto.AgentNameArgs{Name: "manual"}, &actions); err != nil {
		t.Fatal(err)
	}
	if len(actions) != 1 || actions[0].Plugin != "manual" || actions[0].Name != "run" || actions[0].Description != "Run action" || !actions[0].ManualArgs {
		t.Fatalf("discovery = %+v", actions)
	}
	var count int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_requested'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unauthorized caller reached plugin")
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", map[string]any{"name": "manual", "action": "run"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", map[string]any{"name": "manual", "action": "run", "args": []string{"fail"}}, nil); err == nil || !strings.Contains(err.Error(), "requested failure") {
		t.Fatalf("hook error = %v", err)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", map[string]any{"name": "missing"}, nil); err == nil {
		t.Fatal("accepted missing plugin")
	}
}

func TestManualPluginSocketReturnsHookResult(t *testing.T) {
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, plugins.Dir, "result")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"result","hooks":[{"event":"manual","name":"run","description":"Run action","lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`event.data.result = "https://forge.example/pulls/59"`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })
	var response proto.PluginTriggerResponse
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", proto.PluginTriggerArgs{Name: "result", Action: "run"}, &response); err != nil {
		t.Fatal(err)
	}
	if response.Result != "https://forge.example/pulls/59" {
		t.Fatalf("socket plugin result = %q", response.Result)
	}
}

func TestManualPluginRolesAuthorizeAgentsAndAuditIdentity(t *testing.T) {
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, plugins.Dir, "manual")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"manual","hooks":[{"event":"manual","name":"user-only","description":"User action","lua":"hook.lua"},{"event":"manual","name":"shared","description":"Shared action","roles":["user","ceo"],"manual_args":true,"lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`omo.local_set("ran", true); omo.local_set("caller_role", event.data.caller_role)`), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	o.Sup.Plugins, err = plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []db.Agent{
		{Name: "ceo-ada", Role: "ceo", Profile: "test"},
		{Name: "developer-ada", Role: "developer", Profile: "test"},
	} {
		if err := db.InsertAgent(o.DB, agent); err != nil {
			t.Fatal(err)
		}
		if err := db.SetAgentState(o.DB, agent.Name, "working"); err != nil {
			t.Fatal(err)
		}
	}

	err = o.Sup.TriggerPlugin("ceo-ada", "manual", "user-only", nil)
	var permissionErr *PluginPermissionError
	if !errors.As(err, &permissionErr) || permissionErr.Role != "ceo" {
		t.Fatalf("CEO denial = %v, typed error = %+v", err, permissionErr)
	}
	var audited int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind LIKE 'plugin_manual_%'`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 0 {
		t.Fatalf("denied trigger created %d audit events", audited)
	}
	if err := o.Sup.TriggerPlugin("user", "manual", "shared", []string{"secret"}); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, "ceo-ada", "plugin.trigger", proto.PluginTriggerArgs{Name: "manual", Action: "shared", Args: []string{"secret"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, "developer-ada", "plugin.trigger", proto.PluginTriggerArgs{Name: "manual", Action: "shared"}, nil); err == nil || !strings.Contains(err.Error(), `role "developer" may not trigger plugin`) {
		t.Fatalf("developer trigger error = %v", err)
	}
	var callerRole string
	if err := o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='manual' AND key='caller_role'`).Scan(&callerRole); err != nil {
		t.Fatal(err)
	}
	if callerRole != `"ceo"` {
		t.Fatalf("caller role = %s, want %q", callerRole, "ceo")
	}
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind LIKE 'plugin_manual_%'`).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 4 {
		t.Fatalf("audit event count = %d, want two request/outcome pairs", audited)
	}

	rows, err := o.DB.Query(`SELECT kind, agent, detail FROM events WHERE kind LIKE 'plugin_manual_%' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, agent, detail string
		if err := rows.Scan(&kind, &agent, &detail); err != nil {
			t.Fatal(err)
		}
		if agent != "user" && agent != "ceo-ada" {
			t.Fatalf("audit agent = %q", agent)
		}
		if strings.Contains(detail, "secret") {
			t.Fatalf("audit detail leaked argument: %s", detail)
		}
		var fields map[string]any
		if err := json.Unmarshal([]byte(detail), &fields); err != nil {
			t.Fatal(err)
		}
		if fields["plugin"] != "manual" || fields["action"] != "shared" || fields["argument_count"] != float64(1) {
			t.Fatalf("audit fields for %s = %#v", kind, fields)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestManualPluginContextUsesTrustedJobMetadataAtSupervisorBoundary(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["demo"] = config.Repository{Path: repo}
	dir := filepath.Join(o.Dir, plugins.Dir, "manual-context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"manual-context","hooks":[{"event":"manual","name":"capture","description":"Capture context","roles":["user","developer"],"lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`local keys = {}; for key, _ in pairs(event.data) do table.insert(keys, key) end; table.sort(keys); omo.local_set("keys", table.concat(keys, ",")); omo.local_set("context", table.concat({tostring(event.data.job_id), tostring(event.data.repo), tostring(event.data.branch), tostring(event.data.base_branch), tostring(event.data.worktree)}, "|"))`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })

	job := &queue.Job{Title: "context", Goal: "g", Role: "developer", Repo: "demo"}
	if err := o.Sup.Jobs.Create(job); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetWorktree(job.ID, "/trusted/worktree", "omo/job-context"); err != nil {
		t.Fatal(err)
	}
	job, err = o.Sup.Jobs.Get(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	base, err := o.Sup.Git.CurrentBranch(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-context", Role: "developer", Profile: "developer", JobID: job.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.TriggerPluginResult("developer-context", "manual-context", "capture", nil); err != nil {
		t.Fatal(err)
	}
	var encoded string
	if err := o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='manual-context' AND key='context'`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%d|demo|omo/job-context|%s|/trusted/worktree", job.ID, base)
	if got != want {
		t.Fatalf("job plugin context = %q, want %q", got, want)
	}
	var keys string
	if err := o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='manual-context' AND key='keys'`).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	var decodedKeys string
	if err := json.Unmarshal([]byte(keys), &decodedKeys); err != nil {
		t.Fatal(err)
	}
	if decodedKeys != "action,args,at,at_unix,base_branch,branch,caller,caller_role,job_id,plugin,repo,request_id,worktree" {
		t.Fatalf("manual event keys = %q, want exact trusted payload keys", decodedKeys)
	}

	if _, err := o.Sup.TriggerPluginResult("user", "manual-context", "capture", nil); err != nil {
		t.Fatal(err)
	}
	if err := o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='manual-context' AND key='context'`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatal(err)
	}
	if got != "nil|nil|nil|nil|nil" {
		t.Fatalf("user plugin context invented job metadata: %q", got)
	}

	if err := db.InsertAgent(o.DB, db.Agent{Name: "developer-no-job", Role: "developer", Profile: "developer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.TriggerPluginResult("developer-no-job", "manual-context", "capture", nil); err != nil {
		t.Fatal(err)
	}
	if err := o.DB.QueryRow(`SELECT value FROM plugin_storage WHERE plugin='manual-context' AND key='context'`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(encoded), &got); err != nil {
		t.Fatal(err)
	}
	if got != "nil|nil|nil|nil|nil" {
		t.Fatalf("no-job plugin context invented metadata: %q", got)
	}
}

func TestManualPluginPMChildUsesIntegrationBranchAsBase(t *testing.T) {
	o := newOffice(t, nil)
	pm := &queue.Job{Title: "PM", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetIntegrationBranch(pm.ID, "api", queue.IntegrationBranch{Branch: "omo/pm-target", Base: "main", Worktree: "/trusted/pm"}); err != nil {
		t.Fatal(err)
	}
	child := &queue.Job{Title: "child", Goal: "g", Role: "developer", Repo: "api", ParentJob: pm.ID}
	if err := o.Sup.Jobs.Create(child); err != nil {
		t.Fatal(err)
	}
	context, err := o.Sup.jobPluginContext(&db.Agent{JobID: child.ID})
	if err != nil {
		t.Fatal(err)
	}
	if context["base_branch"] != "omo/pm-target" {
		t.Fatalf("PM-child manual base_branch = %v, want integration branch", context["base_branch"])
	}
	if _, ok := context["merge_target"]; ok {
		t.Fatal("manual context leaked merge_target")
	}
}

func TestManualPluginPMContextCarriesDeterministicIntegrationBranches(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.Cfg.Branches.MergeTarget = config.MergeTargetAutoMerge
	o.Sup.Cfg.Repos["alpha"] = config.Repository{MergeTarget: config.MergeTargetAsIs}
	o.Sup.Cfg.Repos["zeta"] = config.Repository{MergeTarget: config.MergeTargetAutoMerge}
	pm := &queue.Job{Title: "PM", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetIntegrationBranch(pm.ID, "zeta", queue.IntegrationBranch{Branch: "omo/pm-zeta", Base: "main", Worktree: "/trusted/zeta"}); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetIntegrationBranch(pm.ID, "alpha", queue.IntegrationBranch{Branch: "omo/pm-alpha", Base: "trunk", Worktree: "/trusted/alpha"}); err != nil {
		t.Fatal(err)
	}
	context, err := o.Sup.jobPluginContext(&db.Agent{JobID: pm.ID})
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := context["integration_branches"].([]map[string]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("PM integration_branches = %#v", context["integration_branches"])
	}
	if entries[0]["repo"] != "alpha" {
		t.Fatalf("PM integration branch order = %#v", entries)
	}
	if entries[0]["branch"] != "omo/pm-alpha" || entries[0]["base_branch"] != "trunk" || entries[0]["worktree"] != "/trusted/alpha" {
		t.Fatalf("first PM integration context = %#v", entries[0])
	}
	if _, ok := context["merge_target"]; ok {
		t.Fatal("PM manual context leaked merge_target")
	}
}

func TestManualPluginPMContextReturnsEmptyListForGlobalAutomerge(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.Cfg.Branches.MergeTarget = config.MergeTargetAutoMerge
	o.Sup.Cfg.Repos["api"] = config.Repository{}
	pm := &queue.Job{Title: "PM", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetIntegrationBranch(pm.ID, "api", queue.IntegrationBranch{Branch: "omo/pm-api", Base: "main", Worktree: "/trusted/api"}); err != nil {
		t.Fatal(err)
	}
	context, err := o.Sup.jobPluginContext(&db.Agent{JobID: pm.ID})
	if err != nil {
		t.Fatal(err)
	}
	entries, ok := context["integration_branches"].([]map[string]any)
	if !ok || len(entries) != 0 {
		t.Fatalf("global automerge PM integration_branches = %#v", context["integration_branches"])
	}
}
