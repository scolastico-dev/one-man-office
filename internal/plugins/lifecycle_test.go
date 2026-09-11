package plugins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLifecycleHooksUseTenSecondDefaultAndRemainImmutable(t *testing.T) {
	office, database := newPluginOffice(t)
	dir := filepath.Join(office, Dir, "lifecycle")
	writePlugin(t, dir, Manifest{Name: "lifecycle", Hooks: []Hook{
		{Event: EventLoad, Lua: "hook.lua"},
		{Event: EventStartup, Lua: "hook.lua"},
		{Event: EventUnload, Timeout: "7s", Lua: "hook.lua"},
		{Event: EventJobCreate, Lua: "hook.lua"},
	}}, `event.mutable = true`)

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	for _, hook := range manager.hooks {
		switch hook.hook.Event {
		case EventLoad, EventStartup:
			if hook.timeout != 10*time.Second {
				t.Fatalf("%s timeout = %s, want 10s", hook.hook.Event, hook.timeout)
			}
		case EventUnload:
			if hook.timeout != 7*time.Second {
				t.Fatalf("unload timeout = %s, want 7s", hook.timeout)
			}
		case EventJobCreate:
			if hook.timeout != 30*time.Second {
				t.Fatalf("ordinary timeout = %s, want 30s", hook.timeout)
			}
		}
	}
	event, err := manager.emitLifecycle(context.Background(), EventStartup, map[string]any{"plugin": "lifecycle"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if event.Mutable {
		t.Fatal("lifecycle event became mutable")
	}
}

func TestLoadLifecycleHookRunsAfterManagerLoad(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "installed-loader"), Manifest{Name: "loader", Hooks: []Hook{{Event: EventLoad, Lua: "hook.lua"}}}, `omo.local_set("loaded", event.data.plugin); omo.local_set("scope", event.data.scope)`)

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	assertStored(t, manager, "local", "loader", "loaded", `"installed-loader"`)
	assertStored(t, manager, "local", "loader", "scope", `"office"`)
}

func TestLifecycleHooksTargetPluginsAndReverseShutdownOrder(t *testing.T) {
	office, database := newPluginOffice(t)
	script := `omo.global_set("order", (omo.global_get("order") or "") .. "-" .. event.data.plugin)`
	for _, name := range []string{"a", "b"} {
		writePlugin(t, filepath.Join(office, Dir, name), Manifest{Name: name, Hooks: []Hook{
			{Event: EventStartup, Lua: "hook.lua"},
			{Event: EventShutdown, Lua: "hook.lua"},
		}}, script)
	}
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	if _, err := manager.emitLifecycle(context.Background(), EventStartup, map[string]any{}, false); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "global", "", "order", `"-a-b"`)
	if _, err := manager.emitLifecycle(context.Background(), EventShutdown, map[string]any{}, true); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "global", "", "order", `"-a-b-b-a"`)
}

func TestCloseRunsUnloadInReverseContinuesAfterFailureAndIsIdempotent(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "a-fails"), Manifest{Name: "a-fails", Hooks: []Hook{{Event: EventUnload, Lua: "hook.lua"}}}, `error("unload failed")`)
	writePlugin(t, filepath.Join(office, Dir, "b-runs"), Manifest{Name: "b-runs", Hooks: []Hook{{Event: EventUnload, Lua: "hook.lua"}}}, `omo.global_set("order", (omo.global_get("order") or "") .. "-b")`)
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "global", "", "order", `"-b"`)
}

func TestUnloadSeesSharedSnapshotBeforeDeletion(t *testing.T) {
	office, database := newPluginOffice(t)
	shared := t.TempDir()
	writePlugin(t, filepath.Join(shared, "shared"), Manifest{Name: "shared", Hooks: []Hook{{Event: EventUnload, Lua: "hook.lua"}}}, `local _, err = omo.exec("test", "-f", "plugin.json"); omo.global_set("snapshot", err == "")`)
	manager, err := LoadSources(office, database, Source{Root: shared, Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := manager.snapshotDir
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	assertStored(t, manager, "global", "", "snapshot", `true`)
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("snapshot after unload = %v, want removed", err)
	}
}

func TestLifecycleCommandStdoutIsIgnoredForImmutableEvents(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "command"), Manifest{Name: "command", Hooks: []Hook{{Event: EventStartup, Command: []string{os.Args[0], "-test.run=^TestLifecycleCommandChildProcess$"}}}}, "")
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	event, err := manager.emitLifecycle(context.Background(), EventStartup, map[string]any{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := event.Data["bad"]; ok {
		t.Fatalf("immutable command output changed event: %#v", event.Data)
	}
}

func TestLifecycleCommandChildProcess(t *testing.T) {
	if os.Getenv("OMO_PLUGIN_EVENT") != EventStartup {
		return
	}
	if _, err := fmt.Fprint(os.Stdout, `{"bad":true}`); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestLifecycleTimeoutBoundsClose(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "slow"), Manifest{Name: "slow", Hooks: []Hook{{Event: EventUnload, Timeout: "10ms", Lua: "hook.lua"}}}, `while true do end`)
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Close() took %s after lifecycle timeout", elapsed)
	}
}
