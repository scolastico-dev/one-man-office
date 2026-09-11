package supervisor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/proto"
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
