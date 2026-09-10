package pluginmanager

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"gopkg.in/yaml.v3"
)

func TestSyncMergesManifestDefaultsOnInstallAndUpdate(t *testing.T) {
	work, remote := pluginRemote(t, "one")
	publishManifest(t, work, remote, `{"name":"nudge","hooks":[],"default_config":{"nested":{"keep":"initial","added":1},"empty":false,"list":[1,2],"large":9007199254740993}}`)
	office, path := configOffice(t, "# office\nplugins:\n  installed: {}\n")
	entry := config.Plugin{Source: remote, Subpath: "examples/nudge", Enabled: true}
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	installed := readPluginConfig(t, path)
	if installed.Source != remote || !installed.Enabled || installed.Config["nested"].(map[string]any)["keep"] != "initial" {
		t.Fatalf("installed config = %#v", installed)
	}
	if installed.Config["large"] != 9007199254740993 {
		t.Fatalf("JSON integer lost precision: %#v", installed.Config["large"])
	}
	custom := "# office\nplugins:\n  installed:\n    nudge:\n      source: " + remote + "\n      subpath: examples/nudge\n      enabled: false # stay disabled\n      config:\n        nested: # user section\n          keep: 'custom' # keep this\n        empty: false\n        zero: 0\n        blank: ''\n        list: []\n        blocked: null\n        scalar: custom\n        extra: mine\n"
	if err := os.WriteFile(path, []byte(custom), 0o640); err != nil {
		t.Fatal(err)
	}
	publishManifest(t, work, remote, `{"name":"nudge","hooks":[],"default_config":{"nested":{"keep":"changed","added":2},"empty":true,"zero":4,"blank":"default","list":[3],"blocked":{"new":true},"scalar":{"new":true},"fresh":{"leaf":"new"}}}`)
	// Deliberately stale caller settings must not replace the current YAML.
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	got := readPluginConfig(t, path)
	want := map[string]any{"nested": map[string]any{"keep": "custom", "added": 2}, "empty": false, "zero": 0, "blank": "", "list": []any{}, "blocked": nil, "scalar": "custom", "extra": "mine", "fresh": map[string]any{"leaf": "new"}}
	if got.Enabled || !reflect.DeepEqual(got.Config, want) {
		t.Fatalf("updated config = %#v, want %#v", got, want)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, comment := range []string{"# office", "# stay disabled", "# user section", "'custom' # keep this"} {
		if !strings.Contains(string(raw), comment) {
			t.Fatalf("lost %q:\n%s", comment, raw)
		}
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("mode: %v, %v", info, err)
	}
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	assertFile(t, path, string(raw))
}

func TestSyncDoesNotWritePluginDefaultsThroughSharedAnchor(t *testing.T) {
	work, remote := pluginRemote(t, "one")
	publishManifest(t, work, remote, `{"name":"a","hooks":[],"default_config":{"added":true}}`)
	office, path := configOffice(t, "plugins:\n  installed:\n    a:\n      source: "+remote+"\n      subpath: examples/nudge\n      enabled: true\n      config: &shared {keep: user}\n    b:\n      source: builtin:b\n      enabled: true\n      config: *shared\n")
	entry := config.Plugin{Source: remote, Subpath: "examples/nudge", Enabled: true}
	if _, err := Sync(context.Background(), office, "a", entry); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Plugins struct {
			Installed map[string]config.Plugin `yaml:"installed"`
		} `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("written config is invalid YAML: %v\n%s", err, raw)
	}
	if got := doc.Plugins.Installed["a"].Config; got["added"] != true {
		t.Fatalf("target plugin defaults = %#v", got)
	}
	if got := doc.Plugins.Installed["b"].Config; len(got) != 1 || got["keep"] != "user" {
		t.Fatalf("shared alias was changed = %#v\n%s", got, raw)
	}
}

func TestSyncRejectsInvalidDefaultsWithoutChangingActivePlugin(t *testing.T) {
	for _, invalid := range []string{`[]`, `"bad"`, `true`, `null`} {
		t.Run(invalid, func(t *testing.T) {
			work, remote := pluginRemote(t, "one")
			office, path := configOffice(t, "plugins:\n  installed: {}\n")
			entry := config.Plugin{Source: remote, Subpath: "examples/nudge", Enabled: true}
			if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			publishManifest(t, work, remote, `{"hooks":[],"default_config":`+invalid+`}`)
			if _, err := Sync(context.Background(), office, "nudge", entry); err == nil {
				t.Fatal("invalid default_config accepted")
			}
			assertFile(t, path, string(before))
			assertFile(t, filepath.Join(office, rootDir, "nudge", "plugin.json"), "{\"name\":\"nudge\",\"hooks\":[]}")
		})
	}
}

func configOffice(t *testing.T, raw string) (string, string) {
	t.Helper()
	office := t.TempDir()
	if err := os.Mkdir(filepath.Join(office, ".omo"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(office, ".omo", "omo.yaml")
	if err := os.WriteFile(path, []byte(raw), 0o640); err != nil {
		t.Fatal(err)
	}
	return office, path
}

func TestInstallTreeRollsBackWhenConfigCommitFails(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "install", true: "update"}[existing], func(t *testing.T) {
			root, source := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(source, "plugin.json"), []byte(`{"hooks":[]}`), 0o644); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(root, "test")
			if existing {
				if err := os.Mkdir(target, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "old"), []byte("original"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			failure := errors.New("config write failed")
			if err := installTree(root, "test", source, func() error { return failure }); !errors.Is(err, failure) {
				t.Fatalf("got %v", err)
			}
			if existing {
				assertFile(t, filepath.Join(target, "old"), "original")
			} else if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("failed install left target: %v", err)
			}
		})
	}
}

func TestSyncBundledManifestAddsDefaultsAndPreservesLocalFiles(t *testing.T) {
	office, path := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: "builtin:nudge", Enabled: true}
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	if readPluginConfig(t, path).Config["check_interval"] != "1m" {
		t.Fatal("bundled install did not populate manifest defaults")
	}
	manifest := filepath.Join(office, rootDir, "nudge", "plugin.json")
	custom := `{"name":"nudge","hooks":[],"default_config":{"custom":{"added":true},"check_interval":"5m"}}`
	if err := os.WriteFile(manifest, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	got := readPluginConfig(t, path).Config
	if got["check_interval"] != "1m" || got["custom"].(map[string]any)["added"] != true {
		t.Fatalf("bundled update = %#v", got)
	}
	assertFile(t, manifest, custom)
}

func publishManifest(t *testing.T, work, remote, raw string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "plugin.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: update defaults")
	u, _ := url.Parse(remote)
	git(t, work, "push", u.Path, "HEAD:main")
}

func readPluginConfig(t *testing.T, path string) config.Plugin {
	return readPluginConfigNamed(t, path, "nudge")
}

func readPluginConfigNamed(t *testing.T, path, name string) config.Plugin {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Plugins config.Plugins `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	return c.Plugins.Installed[name]
}
