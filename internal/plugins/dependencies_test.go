package plugins

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOrderedUsesDependencyFirstOrderWithLexicalTies(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "z-independent"), Manifest{Name: "z-independent"}, "")
	writePlugin(t, filepath.Join(office, Dir, "app"), Manifest{Name: "app", Requires: []Dependency{{Name: "middle", Source: "https://example.test/middle"}}}, "")
	writePlugin(t, filepath.Join(office, Dir, "middle"), Manifest{Name: "middle", Requires: []Dependency{{Name: "base", Source: "https://example.test/base"}}}, "")
	writePlugin(t, filepath.Join(office, Dir, "base"), Manifest{Name: "base"}, "")

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	want := []string{"base", "middle", "app", "z-independent"}
	if got := manager.Ordered(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Ordered() = %v, want %v", got, want)
	}
	ordered := manager.Ordered()
	ordered[0] = "mutated"
	if got := manager.Ordered(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Ordered() returned aliased storage: %v", got)
	}
}

func TestOrderedResolvesInstallationAndManifestAliases(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "installed-base"), Manifest{Name: "manifest-base"}, "")
	writePlugin(t, filepath.Join(office, Dir, "by-installation"), Manifest{Name: "by-installation", Requires: []Dependency{{Name: "installed-base", Source: "https://example.test/base"}}}, "")
	writePlugin(t, filepath.Join(office, Dir, "by-manifest"), Manifest{Name: "by-manifest", Requires: []Dependency{{Name: "manifest-base", Source: "https://example.test/base"}}}, "")

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	want := []string{"installed-base", "by-installation", "by-manifest"}
	if got := manager.Ordered(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Ordered() = %v, want %v", got, want)
	}
}

func TestLoadRejectsDeterministicDependencyCycle(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "a"), Manifest{Name: "a", Requires: []Dependency{{Name: "b", Source: "https://example.test/b"}}}, "")
	writePlugin(t, filepath.Join(office, Dir, "b"), Manifest{Name: "b", Requires: []Dependency{{Name: "a", Source: "https://example.test/a"}}}, "")

	_, err := Load(office, database)
	var cycle *DependencyCycleError
	if !errors.As(err, &cycle) || !reflect.DeepEqual(cycle.Cycle, []string{"a", "b", "a"}) {
		t.Fatalf("cycle error = %#v, want [a b a]", err)
	}
}

func TestLoadRejectsAmbiguousInstallationAndManifestAliases(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "a"), Manifest{Name: "b"}, "")
	writePlugin(t, filepath.Join(office, Dir, "b"), Manifest{Name: "c"}, "")

	if _, err := Load(office, database); err == nil {
		t.Fatal("Load() accepted an ambiguous installation/manifest alias")
	}
}

func TestOrdinaryEventsUseDependencyFirstHookOrder(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "dependent"), Manifest{Name: "dependent", Requires: []Dependency{{Name: "base", Source: "https://example.test/base"}}, Hooks: []Hook{{Event: EventJobCreate, Lua: "hook.lua"}}}, `event.data.order = event.data.order .. "-dependent"`)
	writePlugin(t, filepath.Join(office, Dir, "base"), Manifest{Name: "base", Hooks: []Hook{{Event: EventJobCreate, Lua: "hook.lua"}}}, `event.data.order = event.data.order .. "-base"`)
	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	event, err := manager.Emit(context.Background(), Event{Name: EventJobCreate, Mutable: true, Data: map[string]any{"order": "start"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := event.Data["order"], "start-base-dependent"; got != want {
		t.Fatalf("event order = %v, want %v", got, want)
	}
}

func TestLoadReportsMissingPluginDependencies(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "reporter"), Manifest{
		Name: "reporter",
		Requires: []Dependency{{
			Name: "collector", Source: "https://example.test/plugins.git", Subpath: "collector", Branch: "feature/collector",
		}},
	}, "-- no-op")

	_, err := Load(office, database)
	var missing *MissingDependenciesError
	if !errors.As(err, &missing) {
		t.Fatalf("Load() error = %v, want MissingDependenciesError", err)
	}
	if len(missing.Dependencies) != 1 {
		t.Fatalf("missing dependencies = %+v", missing.Dependencies)
	}
	got := missing.Dependencies[0]
	if got.Name != "collector" || got.Source != "https://example.test/plugins.git" || got.Subpath != "collector" || got.Branch != "feature/collector" || len(got.RequiredBy) != 1 || got.RequiredBy[0] != "reporter" {
		t.Fatalf("missing dependency = %+v", got)
	}
}

func TestReadManifestNormalizesDependencyBranch(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, Manifest{Name: "reporter", Requires: []Dependency{{
		Name: "collector", Source: "https://example.test/collector.git", Branch: " feature/preview ", Version: "^1.2.3",
	}}}, "-- no-op")
	manifest, err := ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := manifest.Requires[0]
	if got.Branch != "feature/preview" || got.Version != "^1.2.3" {
		t.Fatalf("dependency = %+v", got)
	}
}

func TestReadManifestRejectsInvalidDependencyBranches(t *testing.T) {
	for _, branch := range []string{"bad name", "-option", "HEAD", "refs/heads/main", "feature/\x00preview", "feature/.hidden"} {
		t.Run(branch, func(t *testing.T) {
			dir := t.TempDir()
			writePlugin(t, dir, Manifest{Name: "reporter", Requires: []Dependency{{
				Name: "collector", Source: "https://example.test/collector.git", Branch: branch,
			}}}, "-- no-op")
			if _, err := ReadManifest(dir); err == nil {
				t.Fatalf("ReadManifest() accepted branch %q", branch)
			}
		})
	}
}

func TestReadManifestRejectsMalformedManifestVersionAndConstraint(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, Manifest{Name: "reporter", Version: "1.2"}, "-- no-op")
	if _, err := ReadManifest(dir); err == nil {
		t.Fatal("ReadManifest() accepted malformed manifest version")
	}

	dir = t.TempDir()
	writePlugin(t, dir, Manifest{Name: "reporter", Requires: []Dependency{{Name: "collector", Source: "https://example.test/collector.git", Version: "^1.2"}}}, "-- no-op")
	if _, err := ReadManifest(dir); err == nil {
		t.Fatal("ReadManifest() accepted malformed dependency constraint")
	}
}

func TestLoadRejectsDependencyVersionMismatchWithSortedRequiringPlugins(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "collector"), Manifest{Name: "collector", Version: "1.0.0"}, "-- no-op")
	writePlugin(t, filepath.Join(office, Dir, "z-reporter"), Manifest{Name: "z-reporter", Requires: []Dependency{{Name: "collector", Source: "https://example.test/collector.git", Version: ">=2.0.0"}}}, "-- no-op")
	writePlugin(t, filepath.Join(office, Dir, "a-reporter"), Manifest{Name: "a-reporter", Requires: []Dependency{{Name: "collector", Source: "https://example.test/collector.git", Version: ">=2.0.0"}}}, "-- no-op")

	_, err := Load(office, database)
	var mismatch *DependencyVersionMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("Load() error = %v, want DependencyVersionMismatchError", err)
	}
	if len(mismatch.Mismatches) != 1 {
		t.Fatalf("mismatches = %+v", mismatch.Mismatches)
	}
	got := mismatch.Mismatches[0]
	if got.Name != "collector" || got.Required != ">=2.0.0" || got.Found != "1.0.0" || got.InstallationName != "collector" {
		t.Fatalf("mismatch = %+v", got)
	}
	if got.RequiredBy[0] != "a-reporter" || got.RequiredBy[1] != "z-reporter" {
		t.Fatalf("requiring plugins = %v", got.RequiredBy)
	}
}

func TestLoadRejectsConstraintWhenDependencyVersionIsMissing(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "collector"), Manifest{Name: "collector"}, "-- no-op")
	writePlugin(t, filepath.Join(office, Dir, "reporter"), Manifest{Name: "reporter", Requires: []Dependency{{Name: "collector", Source: "https://example.test/collector.git", Version: "1.2.3"}}}, "-- no-op")

	_, err := Load(office, database)
	var mismatch *DependencyVersionMismatchError
	if !errors.As(err, &mismatch) || len(mismatch.Mismatches) != 1 || mismatch.Mismatches[0].Found != "" {
		t.Fatalf("Load() error = %v, mismatch = %+v", err, mismatch)
	}
}

func TestLoadChecksDependencyVersionByInstallationName(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "collector-installed"), Manifest{Name: "collector", Version: "1.0.0"}, "-- no-op")
	writePlugin(t, filepath.Join(office, Dir, "reporter"), Manifest{Name: "reporter", Requires: []Dependency{{Name: "collector-installed", Source: "https://example.test/collector.git", Version: "^2.0.0"}}}, "-- no-op")

	_, err := Load(office, database)
	var mismatch *DependencyVersionMismatchError
	if !errors.As(err, &mismatch) || len(mismatch.Mismatches) != 1 || mismatch.Mismatches[0].InstallationName != "collector-installed" {
		t.Fatalf("Load() error = %v, mismatch = %+v", err, mismatch)
	}
}

func TestLoadAcceptsDependencyProvidedByManifestName(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "installed-as-rules"), Manifest{Name: "collector"}, "-- no-op")
	writePlugin(t, filepath.Join(office, Dir, "reporter"), Manifest{
		Name: "reporter", Requires: []Dependency{{Name: "collector", Source: "https://example.test/collector"}},
	}, "-- no-op")

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
}

func TestLoadAcceptsDependencyProvidedByGlobalPlugin(t *testing.T) {
	office, database := newPluginOffice(t)
	global := t.TempDir()
	writePlugin(t, filepath.Join(global, "collector"), Manifest{Name: "collector"}, "-- no-op")
	writePlugin(t, filepath.Join(office, Dir, "reporter"), Manifest{
		Name: "reporter", Requires: []Dependency{{Name: "collector", Source: "https://example.test/collector"}},
	}, "-- no-op")

	manager, err := LoadSources(office, database, Source{Root: global}, Source{Root: filepath.Join(office, Dir)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
}

func TestLoadAcceptsDependencyProvidedByInstallationName(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "collector"), Manifest{Name: "renamed-collector"}, "-- no-op")
	writePlugin(t, filepath.Join(office, Dir, "reporter"), Manifest{
		Name:     "reporter",
		Requires: []Dependency{{Name: "collector", Source: "https://example.test/collector.git"}},
	}, "-- no-op")

	manager, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
}

func TestLoadRejectsConflictingDependencySources(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "alpha"), Manifest{Name: "alpha", Requires: []Dependency{{Name: "shared", Source: "https://example.test/one.git"}}}, "-- no-op")
	writePlugin(t, filepath.Join(office, Dir, "beta"), Manifest{Name: "beta", Requires: []Dependency{{Name: "shared", Source: "https://example.test/two.git"}}}, "-- no-op")

	if _, err := Load(office, database); err == nil {
		t.Fatal("Load() accepted conflicting dependency sources")
	}
}

func TestLoadRejectsConflictingDependencyBranches(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "alpha"), Manifest{Name: "alpha", Requires: []Dependency{{
		Name: "shared", Source: "https://example.test/shared.git", Branch: "stable",
	}}}, "-- no-op")
	writePlugin(t, filepath.Join(office, Dir, "beta"), Manifest{Name: "beta", Requires: []Dependency{{
		Name: "shared", Source: "https://example.test/shared.git", Branch: "next",
	}}}, "-- no-op")

	if _, err := Load(office, database); err == nil || !strings.Contains(err.Error(), "conflicting installation sources") {
		t.Fatalf("Load() error = %v, want conflicting branch metadata", err)
	}
}

func TestLoadRejectsUnsafeDependencyMetadata(t *testing.T) {
	for _, dependency := range []Dependency{
		{Name: "..", Source: "https://example.test/plugin.git"},
		{Name: "collector", Source: "https://example.test/plugin.git\x1b[2J"},
		{Name: "collector", Source: "javascript:alert(1)"},
		{Name: "collector", Source: "https://example.test/plugin.git", Subpath: "../outside"},
	} {
		t.Run(dependency.Name+dependency.Source+dependency.Subpath, func(t *testing.T) {
			office, database := newPluginOffice(t)
			writePlugin(t, filepath.Join(office, Dir, "reporter"), Manifest{Name: "reporter", Requires: []Dependency{dependency}}, "-- no-op")
			if _, err := Load(office, database); err == nil {
				t.Fatalf("Load() accepted dependency %+v", dependency)
			}
		})
	}
}

func TestReadManifestRejectsUnsafeManifestName(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, Manifest{Name: "reporter\x1b[2J"}, "-- no-op")
	if _, err := ReadManifest(dir); err == nil {
		t.Fatal("ReadManifest() accepted a control character in manifest name")
	}
}

func TestLoadRejectsUnsafeFallbackDirectoryName(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "unsafe\x1b[2J"), Manifest{
		Requires: []Dependency{{Name: "collector", Source: "https://example.test/collector.git"}},
	}, "-- no-op")
	if _, err := Load(office, database); err == nil {
		t.Fatal("Load() accepted an unsafe fallback manifest name")
	}
}

func TestEquivalentRootDependencySubpathsDoNotConflict(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "alpha"), Manifest{Name: "alpha", Requires: []Dependency{{Name: "shared", Source: "https://example.test/shared.git"}}}, "-- no-op")
	writePlugin(t, filepath.Join(office, Dir, "beta"), Manifest{Name: "beta", Requires: []Dependency{{Name: "shared", Source: "https://example.test/shared.git", Subpath: "."}}}, "-- no-op")
	_, err := Load(office, database)
	var missing *MissingDependenciesError
	if !errors.As(err, &missing) || len(missing.Dependencies) != 1 {
		t.Fatalf("Load() error = %v, missing=%+v", err, missing)
	}
}
