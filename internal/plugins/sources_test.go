package plugins

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourcesUseLocalPrecedenceAndScopedConfiguration(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "override", true: "disabled"}[disabled], func(t *testing.T) {
			office, database := newPluginOffice(t)
			global := t.TempDir()
			local := filepath.Join(office, Dir)
			for _, p := range []struct{ root, name string }{{global, "a-global"}, {global, "b-shared"}, {local, "b-shared"}, {local, "c-local"}} {
				writePlugin(t, filepath.Join(p.root, p.name), Manifest{Name: p.name, Hooks: []Hook{{Event: EventJobCreate, Lua: "hook.lua"}}}, `event.data.title = event.data.title .. config.suffix`)
			}
			manager, err := LoadSources(office, database, Source{Root: global, Configured: map[string]Settings{"a-global": {Enabled: true, Config: map[string]any{"suffix": "global;"}}, "b-shared": {Enabled: true, Config: map[string]any{"suffix": "WRONG;"}}}}, Source{Root: local, Configured: map[string]Settings{"b-shared": {Enabled: !disabled, Config: map[string]any{"suffix": "override;"}}, "c-local": {Enabled: true, Config: map[string]any{"suffix": "local;"}}}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := manager.Emit(context.Background(), Event{Name: EventJobCreate, Mutable: true, Data: map[string]any{"title": ""}})
			if err != nil {
				t.Fatal(err)
			}
			want := "global;override;local;"
			if disabled {
				want = "global;local;"
			}
			if result.Data["title"] != want {
				t.Fatalf("title=%v", result.Data["title"])
			}
		})
	}
}

func TestSourcesRejectManifestAliasesAcrossScopes(t *testing.T) {
	office, database := newPluginOffice(t)
	global := t.TempDir()
	local := filepath.Join(office, Dir)
	for _, p := range []struct{ root, name string }{{global, "one"}, {local, "two"}} {
		writePlugin(t, filepath.Join(p.root, p.name), Manifest{Name: "same", Hooks: []Hook{{Event: EventJobCreate, Lua: "hook.lua"}}}, `event.data.title="x"`)
	}
	if _, err := LoadSources(office, database, Source{Root: global}, Source{Root: local}); err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("collision error=%v", err)
	}
}
