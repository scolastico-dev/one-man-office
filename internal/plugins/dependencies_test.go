package plugins

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestLoadReportsMissingPluginDependencies(t *testing.T) {
	office, database := newPluginOffice(t)
	writePlugin(t, filepath.Join(office, Dir, "reporter"), Manifest{
		Name: "reporter",
		Requires: []Dependency{{
			Name: "collector", Source: "https://example.test/plugins.git", Subpath: "collector",
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
	if got.Name != "collector" || got.Source != "https://example.test/plugins.git" || got.Subpath != "collector" || len(got.RequiredBy) != 1 || got.RequiredBy[0] != "reporter" {
		t.Fatalf("missing dependency = %+v", got)
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
