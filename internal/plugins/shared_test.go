package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/filelock"
)

func TestSharedCommandRuntimeRetainsAdjacentFiles(t *testing.T) {
	office, database := newPluginOffice(t)
	shared := t.TempDir()
	active := filepath.Join(shared, "shared")
	writePlugin(t, active, Manifest{Name: "shared", Hooks: []Hook{{Event: EventJobCreate, Command: []string{os.Args[0], "-test.run=^TestSharedCommandChildProcess$"}}}}, "")
	dataPath := filepath.Join(active, "resource.txt")
	if err := os.WriteFile(dataPath, []byte("one"), 0644); err != nil {
		t.Fatal(err)
	}
	manager, err := LoadSources(office, database, Source{Root: shared, Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if err := os.WriteFile(dataPath, []byte("two"), 0644); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Emit(context.Background(), Event{Name: EventJobCreate, Mutable: true})
	if err != nil || result.Data["title"] != "one" {
		t.Fatalf("command resource changed generation: %v %v", result.Data, err)
	}
}

func TestSharedCommandChildProcess(t *testing.T) {
	if os.Getenv("OMO_PLUGIN_NAME") != "shared" {
		return
	}
	raw, err := os.ReadFile("resource.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]string{"title": string(raw)}); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestSharedRuntimeKeepsManifestAndSourceGeneration(t *testing.T) {
	office, database := newPluginOffice(t)
	shared := t.TempDir()
	active := filepath.Join(shared, "shared")
	writePlugin(t, active, Manifest{Name: "shared", Version: "one", Hooks: []Hook{{Event: EventJobCreate, Lua: "one.lua"}}}, "")
	if err := os.WriteFile(filepath.Join(active, "one.lua"), []byte(`event.data.title="one"`), 0644); err != nil {
		t.Fatal(err)
	}
	manager, err := LoadSources(office, database, Source{Root: shared, Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	snapshot := manager.hooks[0].dir
	// Simulate a complete managed activation, including removing the old entry
	// point. An already-running office must never reopen the mutable active tree.
	if err := os.RemoveAll(active); err != nil {
		t.Fatal(err)
	}
	writePlugin(t, active, Manifest{Name: "shared", Version: "two", Hooks: []Hook{{Event: EventJobCreate, Lua: "hook.lua"}}}, `event.data.title="two"`)
	event, err := manager.Emit(context.Background(), Event{Name: EventJobCreate, Mutable: true})
	if err != nil {
		t.Fatal(err)
	}
	if event.Data["title"] != "one" {
		t.Fatalf("old runtime switched generation: %v", event.Data)
	}
	next, err := LoadSources(office, database, Source{Root: shared, Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	event, err = next.Emit(context.Background(), Event{Name: EventJobCreate, Mutable: true})
	if err != nil || event.Data["title"] != "two" {
		t.Fatalf("new runtime missed update: %v %v", event.Data, err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
		t.Fatalf("snapshot not cleaned up: %v", err)
	}
}

func TestSharedLoadCannotObserveActivationGap(t *testing.T) {
	shared := t.TempDir()
	active := filepath.Join(shared, "shared")
	writePlugin(t, active, Manifest{Name: "shared", Hooks: []Hook{{Event: EventJobCreate, Lua: "hook.lua"}}}, `event.data.title="one"`)
	held, err := filelock.Acquire(context.Background(), filepath.Join(shared, ".update.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := os.Rename(active, filepath.Join(shared, ".old")); err != nil {
		t.Fatal(err)
	}
	// The helper runs in a different process while the active directory is
	// missing, exactly between the installer's two renames.
	cmd := exec.Command(os.Args[0], "-test.run=^TestSharedLoadChildProcess$")
	cmd.Env = append(os.Environ(), "OMO_PLUGIN_LOAD_TEST_ROOT="+shared)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child load: %v %s", err, output)
	}
	writePlugin(t, active, Manifest{Name: "shared", Hooks: []Hook{{Event: EventJobCreate, Lua: "hook.lua"}}}, `event.data.title="two"`)
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	office, database := newPluginOffice(t)
	manager, err := LoadSources(office, database, Source{Root: shared, Shared: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	result, err := manager.Emit(context.Background(), Event{Name: EventJobCreate, Mutable: true})
	if err != nil || result.Data["title"] != "two" {
		t.Fatalf("post-activation plugin missing: %v %v", result.Data, err)
	}
}

func TestSharedLoadChildProcess(t *testing.T) {
	shared := os.Getenv("OMO_PLUGIN_LOAD_TEST_ROOT")
	if shared == "" {
		return
	}
	office, database := newPluginOffice(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	manager, err := LoadSourcesContext(ctx, office, database, Source{Root: shared, Shared: true})
	if manager != nil {
		defer manager.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("loader observed unlocked activation gap: %v", err)
	}
}
