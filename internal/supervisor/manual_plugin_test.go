package supervisor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestManualPluginSocketAuthorizesOnlyUserAndReturnsErrors(t *testing.T) {
	o := newOffice(t, nil)
	dir := filepath.Join(o.Dir, plugins.Dir, "manual")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"name":"manual","manual_args":true,"hooks":[{"event":"manual","lua":"hook.lua"}]}`), 0o644); err != nil {
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
		if err := sockc.Call(o.Sup.SocketPath, caller, "plugin.trigger", map[string]any{"name": "manual"}, nil); err == nil {
			t.Errorf("accepted %s", caller)
		}
	}
	var count int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='plugin_manual_requested'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("unauthorized caller reached plugin")
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", map[string]any{"name": "manual"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", map[string]any{"name": "manual", "args": []string{"fail"}}, nil); err == nil || !strings.Contains(err.Error(), "requested failure") {
		t.Fatalf("hook error = %v", err)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", map[string]any{"name": "missing"}, nil); err == nil {
		t.Fatal("accepted missing plugin")
	}
}
