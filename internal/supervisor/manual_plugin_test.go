package supervisor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	var trigger proto.PluginTriggerResponse
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", map[string]any{"name": "manual", "action": "run"}, &trigger); err != nil {
		t.Fatal(err)
	}
	if trigger.RequestID < 1 {
		t.Fatalf("socket trigger response = %+v", trigger)
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

func TestManualPluginRecordsPullRequestForTrustedPMAndSuppressesCompletionNotice(t *testing.T) {
	repo := devRepo(t)
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["api"] = config.Repository{Path: repo, MergeTarget: config.MergeTargetAsIs}

	dir := filepath.Join(o.Dir, plugins.Dir, "pullrequest-record")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"pullrequest-record","hooks":[{"event":"manual","name":"create","description":"Create pull request","roles":["product_manager"],"command":["sh","-c","printf '{\"repo\":\"api\",\"url\":\"https://forge.example/pulls/42\",\"state\":\"existing\"}'"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })

	pm := &queue.Job{Title: "PR", Goal: "g", Role: "product_manager", Assignee: "pm-record"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "ceo", Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetIntegrationBranch(pm.ID, "api", queue.IntegrationBranch{Branch: "omo/pm-record", Base: "main", Worktree: filepath.Join(o.Dir, ".omo", "worktrees", "pm-record")}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking} {
		if err := o.Sup.Jobs.Transition(pm.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	pm, err = o.Sup.Jobs.Get(pm.ID)
	if err != nil {
		t.Fatal(err)
	}
	worktree := pm.IntegrationBranches["api"].Worktree
	if err := o.Sup.Git.AddWorktree(repo, worktree, pm.IntegrationBranches["api"].Branch); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetAssignee(pm.ID, pm.Assignee); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: pm.Assignee, Role: "product_manager", Profile: "product_manager", JobID: pm.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, pm.Assignee, "working"); err != nil {
		t.Fatal(err)
	}

	if _, err := o.Sup.TriggerPluginResult(pm.Assignee, "pullrequest-record", "create", nil); err != nil {
		t.Fatal(err)
	}
	record, ok, err := db.JobPullRequestForRepo(o.DB, pm.ID, "api")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("successful manual pull request was not recorded")
	}
	if record.URL != "https://forge.example/pulls/42" || record.State != "existing" || record.Plugin != "pullrequest-record" || record.Action != "create" {
		t.Fatalf("pull request record = %+v", record)
	}
	var events int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='job_pull_request' AND job_id=?`, pm.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("job_pull_request events = %d, want 1", events)
	}

	if err := o.Sup.finishTopLevelJob(pm, "The request was handled and the branch is ready."); err != nil {
		t.Fatal(err)
	}
	for _, recipient := range []string{"user", "ceo"} {
		mail, err := o.Sup.Mail.Inbox(recipient)
		if err != nil {
			t.Fatal(err)
		}
		if len(mail) != 0 {
			t.Fatalf("%s received pull request notice despite durable record: %+v", recipient, mail)
		}
	}
}

func TestManualPluginSocketAsyncReturnsDurableRequestBeforeHookCompletes(t *testing.T) {
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, plugins.Dir, "blocking")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"blocking","hooks":[{"event":"manual","name":"run","description":"Blocking action","lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`omo.local_set("entered", true); while not omo.global_get("release") do end`), 0o644); err != nil {
		t.Fatal(err)
	}
	var err error
	o.Sup.Plugins, err = plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Sup.Plugins.Close() })

	started := time.Now()
	var response proto.PluginTriggerResponse
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", proto.PluginTriggerArgs{Name: "blocking", Action: "run", Async: true}, &response); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("async trigger waited %s for the hook", elapsed)
	}
	if response.RequestID < 1 {
		t.Fatalf("async trigger response = %+v", response)
	}
	waitFor(t, time.Second, "async hook entry", func() bool {
		var count int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE plugin='blocking' AND key='entered'`).Scan(&count) == nil && count == 1
	})
	var completed int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_completed' AND detail LIKE ?`, "%\"request_id\":"+fmt.Sprint(response.RequestID)+"%").Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 0 {
		t.Fatal("async trigger completed before the hook was released")
	}
	if _, err := o.DB.Exec(`INSERT INTO plugin_storage(scope, plugin, key, value) VALUES('global', '', 'release', 'true')`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "async hook completion", func() bool {
		var count int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_completed' AND detail LIKE ?`, "%\"request_id\":"+fmt.Sprint(response.RequestID)+"%").Scan(&count) == nil && count == 1
	})
}

func TestManualPluginAsyncRecordsPullRequestOnlyAfterSuccessfulHookCompletion(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["api"] = config.Repository{MergeTarget: config.MergeTargetAsIs}
	dir := filepath.Join(o.Dir, plugins.Dir, "async-record")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"async-record","hooks":[{"event":"manual","name":"create","description":"Create pull request","roles":["product_manager"],"lua":"hook.lua"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.lua"), []byte(`omo.local_set("entered", true); while not omo.global_get("release") do end; return {repo="api", url="https://forge.example/pulls/async", state="created"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })

	pm := &queue.Job{Title: "PR", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pm); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetIntegrationBranch(pm.ID, "api", queue.IntegrationBranch{Branch: "omo/pm-async", Base: "main", Worktree: "/trusted/pm-async"}); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.Transition(pm.ID, queue.StateAssigned); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.Transition(pm.ID, queue.StateWorking); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetAssignee(pm.ID, "pm-async"); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "pm-async", Role: "product_manager", Profile: "product_manager", JobID: pm.ID}); err != nil {
		t.Fatal(err)
	}

	requestID, err := o.Sup.TriggerPluginAsync("pm-async", "async-record", "create", nil)
	if err != nil {
		t.Fatal(err)
	}
	if requestID < 1 {
		t.Fatalf("request ID = %d", requestID)
	}
	waitFor(t, time.Second, "blocking hook entry", func() bool {
		var count int
		return o.DB.QueryRow(`SELECT COUNT(*) FROM plugin_storage WHERE plugin='async-record' AND key='entered'`).Scan(&count) == nil && count == 1
	})
	if _, ok, err := db.JobPullRequestForRepo(o.DB, pm.ID, "api"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("async hook recorded pull request before release")
	}
	var completed int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_completed' AND detail LIKE ?`, "%\"request_id\":"+fmt.Sprint(requestID)+"%").Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 0 {
		t.Fatal("async hook completed before release")
	}
	if _, err := o.DB.Exec(`INSERT INTO plugin_storage(scope, plugin, key, value) VALUES('global', '', 'release', 'true')`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "async pull request record", func() bool {
		_, ok, err := db.JobPullRequestForRepo(o.DB, pm.ID, "api")
		return err == nil && ok
	})
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_completed' AND detail LIKE ?`, "%\"request_id\":"+fmt.Sprint(requestID)+"%").Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatalf("plugin completion events = %d, want 1", completed)
	}
	var completedID, recordID int64
	if err := o.DB.QueryRow(`SELECT id FROM events WHERE kind='plugin_manual_completed' AND detail LIKE ?`, "%\"request_id\":"+fmt.Sprint(requestID)+"%").Scan(&completedID); err != nil {
		t.Fatal(err)
	}
	if err := o.DB.QueryRow(`SELECT id FROM events WHERE kind='job_pull_request' AND job_id=?`, pm.ID).Scan(&recordID); err != nil {
		t.Fatal(err)
	}
	if completedID >= recordID {
		t.Fatalf("completion event id=%d, pull request event id=%d; completion must remain durable first", completedID, recordID)
	}
}

func TestManualPluginPullRequestRecordingNormalizesListsAndIgnoresInvalidOrUntrustedValues(t *testing.T) {
	o := newOffice(t, nil)
	job := &queue.Job{Title: "record", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(job); err != nil {
		t.Fatal(err)
	}
	contextData := map[string]any{
		"job_id":               job.ID,
		"integration_branches": []map[string]any{{"repo": "api"}, {"repo": "web"}},
	}
	result := plugins.ManualTriggerResult{Value: []any{
		map[string]any{"repo": "api", "url": "https://forge.example/api/1", "state": "created"},
		map[string]any{"repo": "web", "url": "https://forge.example/web/2", "state": "updated"},
		map[string]any{"repo": "other", "url": "https://forge.example/other/3", "state": "created"},
		map[string]any{"repo": "api", "url": "ssh://forge.example/api/4", "state": "created"},
		map[string]any{"repo": 42, "url": "https://forge.example/api/5"},
	}}
	if err := o.Sup.recordManualPullRequests(contextData, result, "pullrequest", "create"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		repo  string
		url   string
		state string
	}{{"api", "https://forge.example/api/1", "created"}, {"web", "https://forge.example/web/2", "updated"}} {
		got, ok, err := db.JobPullRequestForRepo(o.DB, job.ID, want.repo)
		if err != nil {
			t.Fatal(err)
		}
		if !ok || got.URL != want.url || got.State != want.state || got.Plugin != "pullrequest" || got.Action != "create" {
			t.Fatalf("record for %s = %+v, exists=%v", want.repo, got, ok)
		}
	}
	var records int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM job_pull_requests`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if records != 2 {
		t.Fatalf("job pull request rows = %d, want 2", records)
	}
}

func TestManualPluginFailedMalformedNonHTTPAndUntrustedResultsDoNotRecord(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["api"] = config.Repository{MergeTarget: config.MergeTargetAsIs}
	dir := filepath.Join(o.Dir, plugins.Dir, "invalid-records")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"invalid-records","hooks":[{"event":"manual","name":"failed","description":"Failed","roles":["product_manager"],"command":["sh","-c","exit 1"]},{"event":"manual","name":"malformed","description":"Malformed","roles":["product_manager"],"command":["sh","-c","printf '{'"]},{"event":"manual","name":"non-http","description":"Non HTTP","roles":["product_manager"],"command":["sh","-c","printf '{\"repo\":\"api\",\"url\":\"ssh://forge.example/api/1\"}'"]},{"event":"manual","name":"untrusted","description":"Untrusted","roles":["product_manager"],"command":["sh","-c","printf '{\"repo\":\"other\",\"url\":\"https://forge.example/other/1\"}'"]}]}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })
	job := &queue.Job{Title: "invalid", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(job); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetIntegrationBranch(job.ID, "api", queue.IntegrationBranch{Branch: "omo/invalid", Base: "main", Worktree: "/trusted/invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "pm-invalid", Role: "product_manager", Profile: "product_manager", JobID: job.ID}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"failed", "malformed", "non-http", "untrusted"} {
		_, err := o.Sup.TriggerPluginResult("pm-invalid", "invalid-records", action, nil)
		if action == "failed" || action == "malformed" {
			if err == nil {
				t.Fatalf("%s hook unexpectedly succeeded", action)
			}
		} else if err != nil {
			t.Fatalf("%s hook failed: %v", action, err)
		}
	}
	var records int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM job_pull_requests`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if records != 0 {
		t.Fatalf("invalid manual results recorded %d rows", records)
	}
	var pullRequestEvents int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='job_pull_request'`).Scan(&pullRequestEvents); err != nil {
		t.Fatal(err)
	}
	if pullRequestEvents != 0 {
		t.Fatalf("invalid manual results emitted %d pull request events", pullRequestEvents)
	}
}

func TestManualPluginUserContextRequiresOneUnambiguousAsIsJob(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.Cfg.Repos["api"] = config.Repository{MergeTarget: config.MergeTargetAsIs}
	dir := filepath.Join(o.Dir, plugins.Dir, "user-record")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"user-record","hooks":[{"event":"manual","name":"create","description":"Create pull request","roles":["user"],"manual_args":true,"command":["sh","-c","printf '{\"repo\":\"api\",\"url\":\"https://forge.example/user/1\",\"state\":\"created\"}'"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	manager, err := plugins.Load(o.Dir, o.DB)
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })
	first := &queue.Job{Title: "one", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(first); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetIntegrationBranch(first.ID, "api", queue.IntegrationBranch{Branch: "omo/one", Base: "main", Worktree: "/trusted/one"}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking} {
		if err := o.Sup.Jobs.Transition(first.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := o.Sup.TriggerPluginResult("user", "user-record", "create", []string{"repo=api"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.JobPullRequestForRepo(o.DB, first.ID, "api"); err != nil {
		t.Fatal(err)
	} else if !ok {
		t.Fatal("unambiguous user job context did not record pull request")
	}

	second := &queue.Job{Title: "two", Goal: "g", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(second); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.Jobs.SetIntegrationBranch(second.ID, "api", queue.IntegrationBranch{Branch: "omo/two", Base: "main", Worktree: "/trusted/two"}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []queue.State{queue.StateAssigned, queue.StateWorking} {
		if err := o.Sup.Jobs.Transition(second.ID, state); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := o.Sup.TriggerPluginResult("user", "user-record", "create", []string{"repo=api"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.JobPullRequestForRepo(o.DB, second.ID, "api"); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Fatal("ambiguous user job context recorded pull request")
	}
	if _, err := o.Sup.TriggerPluginResult("user", "user-record", "create", []string{"body=/tmp/body"}); err != nil {
		t.Fatal(err)
	}
	var records int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM job_pull_requests`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if records != 1 {
		t.Fatalf("user pull request records = %d, want 1", records)
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
	if decodedKeys != "action,args,at,at_unix,base_branch,branch,caller,caller_role,home_path,job_id,plugin,repo,request_id,worktree" {
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
