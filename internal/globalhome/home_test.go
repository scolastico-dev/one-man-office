package globalhome

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestOpenCreatesIndependentHome(t *testing.T) {
	root := filepath.Join(t.TempDir(), "omo")
	t.Setenv("OMO_HOME", root)
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if h.Dir != root || !h.Config.Plugins.UpdateOnStart || len(h.Config.TrustedOffices) != 0 || h.Config.Template.Enabled || h.Config.Template.AutoSync {
		t.Fatalf("home = %+v", h)
	}
	for _, name := range []string{"plugins", "extensions", "template"} {
		entries, err := os.ReadDir(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("%s = %v, %v", name, entries, err)
		}
		if name == "plugins" {
			visible := make([]string, 0, len(entries))
			for _, entry := range entries {
				if !strings.HasPrefix(entry.Name(), ".") {
					visible = append(visible, entry.Name())
				}
			}
			if len(visible) != 1 || visible[0] != "filebrowser" {
				t.Fatalf("%s visible entries = %v", name, visible)
			}
		} else if len(entries) != 0 {
			t.Fatalf("%s = %v, want empty", name, entries)
		}
	}
	for _, name := range []string{"known_plugins.json", "known_plugins.example.json"} {
		catalog, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		assertOfficialPluginCatalog(t, catalog)
	}
	for _, name := range []string{"messages", "prompts", "omo.yaml"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("trusted_offices: []\nunknown: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err == nil {
		t.Fatal("unknown global field accepted")
	}
}

func TestOpenPreservesPreExistingKnownPluginCatalog(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	custom := []byte("[\n  {\"name\":\"custom\",\"description\":\"Custom plugin\",\"source\":\"https://example.com/custom.git\"}\n]\n")
	path := filepath.Join(root, "known_plugins.json")
	if err := os.WriteFile(path, custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, custom) {
		t.Fatalf("pre-existing known plugin catalog changed: got %q, want %q", got, custom)
	}
}

func assertOfficialPluginCatalog(t *testing.T, raw []byte) {
	t.Helper()
	type entry struct {
		Name     string `json:"name"`
		Official bool   `json:"official"`
		Source   string `json:"source"`
		Subpath  string `json:"subpath"`
		Branch   string `json:"branch"`
	}
	var got []entry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode generated plugin catalog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("generated plugin catalog entries = %d, want 2: %s", len(got), raw)
	}
	want := map[string]entry{
		"pushover": {
			Name: "pushover", Official: true,
			Source: "https://github.com/scolastico-dev/one-man-office.git", Subpath: "plugins/pushover", Branch: "main",
		},
		"autoshutdown": {
			Name: "autoshutdown", Official: true,
			Source: "https://github.com/scolastico-dev/one-man-office.git", Subpath: "plugins/autoshutdown", Branch: "main",
		},
	}
	for _, plugin := range got {
		wantPlugin, ok := want[plugin.Name]
		if !ok || plugin != wantPlugin {
			t.Fatalf("generated plugin catalog entry = %#v, want one of %#v", plugin, want)
		}
		delete(want, plugin.Name)
	}
	if len(want) != 0 {
		t.Fatalf("generated plugin catalog missing entries: %#v", want)
	}
}

func TestOpenInstallsGlobalFilebrowserAndConfiguresDefaults(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	entry, ok := h.Config.Plugins.Installed["filebrowser"]
	if !ok || entry.Source != "builtin:filebrowser" || !entry.Enabled {
		t.Fatalf("filebrowser config = %+v, present=%v", entry, ok)
	}
	for key, want := range map[string]any{
		"download_warn_bytes": 52428800,
		"download_max_bytes":  1073741824,
		"upload_warn_bytes":   52428800,
		"upload_max_bytes":    1073741824,
	} {
		if got := entry.Config[key]; got != want {
			t.Fatalf("filebrowser config[%q] = %#v, want %#v", key, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(h.Dir, "plugins", "filebrowser", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"nudge", "tools"} {
		if _, err := os.Stat(filepath.Join(h.Dir, "plugins", name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected global %s plugin: %v", name, err)
		}
	}
}

func TestOpenGlobalFilebrowserInitializationIsNoOpOnSecondOpen(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(h.Dir, "config.yaml")
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	seedPath := filepath.Join(h.Dir, "plugins", "filebrowser", "web", "main.js")
	seedBefore, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	seedAfter, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !bytes.Equal(seedBefore, seedAfter) {
		t.Fatalf("second open changed initialized home: config changed=%v seed changed=%v", !bytes.Equal(before, after), !bytes.Equal(seedBefore, seedAfter))
	}
}

func TestOpenPreservesCustomizedGlobalFilebrowserFiles(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.Dir, "plugins", "filebrowser", "web", "main.js")
	custom := []byte("// user customization\n")
	if err := os.WriteFile(path, custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, custom) {
		t.Fatalf("customized main.js = %q, err=%v", got, err)
	}
}

func TestOpenRespectsDisabledGlobalFilebrowserConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("trusted_offices: []\nplugins:\n  update_on_start: false\n  installed:\n    filebrowser:\n      source: builtin:filebrowser\n      enabled: false\n      config: {download_warn_bytes: 123}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	entry := h.Config.Plugins.Installed["filebrowser"]
	if entry.Enabled || entry.Config["download_warn_bytes"] != 123 {
		t.Fatalf("disabled filebrowser config changed: %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "filebrowser", "plugin.json")); err != nil {
		t.Fatal(err)
	}
}

func TestOpenDoesNotRecreateDeletedGlobalFilebrowserConfigEntry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	pluginDir := filepath.Join(root, "plugins", "filebrowser")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := []byte("custom plugin\n")
	if err := os.WriteFile(filepath.Join(pluginDir, "browser.js"), custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("trusted_offices: []\nplugins:\n  update_on_start: false\n  installed: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.Config.Plugins.Installed["filebrowser"]; ok {
		t.Fatal("retained unconfigured filebrowser was claimed")
	}
	got, err := os.ReadFile(filepath.Join(pluginDir, "browser.js"))
	if err != nil || !bytes.Equal(got, custom) {
		t.Fatalf("retained filebrowser changed: %q, %v", got, err)
	}
}

func TestOpenRestoresMissingGlobalFilebrowserWithDisabledEntry(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	raw := []byte("trusted_offices: []\nplugins:\n  update_on_start: false\n  installed:\n    filebrowser:\n      source: builtin:filebrowser\n      enabled: false\n      config: {custom: value}\n")
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), raw, 0o640); err != nil {
		t.Fatal(err)
	}
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	entry := h.Config.Plugins.Installed["filebrowser"]
	if entry.Enabled || entry.Config["custom"] != "value" {
		t.Fatalf("disabled retained config changed: %+v", entry)
	}
	if _, err := os.Stat(filepath.Join(root, "plugins", "filebrowser", "plugin.json")); err != nil {
		t.Fatal(err)
	}
}

func TestOpenGlobalFilebrowserPreservesConfigCommentsAndPermissions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	path := filepath.Join(root, "config.yaml")
	raw := []byte("# keep this global comment\ntrusted_offices: []\nplugins:\n  update_on_start: false\n  installed:\n    example:\n      source: https://example.test/plugin.git\n      enabled: true\n")
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "# keep this global comment") || !strings.Contains(string(got), "example.test/plugin.git") {
		t.Fatalf("global config content was not preserved:\n%s", got)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("global config mode = %v, %v", info, err)
	}
}

func TestOpenDoesNotClaimUnrelatedGlobalFilebrowserDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	pluginDir := filepath.Join(root, "plugins", "filebrowser")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte("not bundled\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := h.Config.Plugins.Installed["filebrowser"]; ok {
		t.Fatal("unrelated filebrowser was claimed")
	}
	got, err := os.ReadFile(filepath.Join(pluginDir, "plugin.json"))
	if err != nil || string(got) != "not bundled\n" {
		t.Fatalf("unrelated filebrowser changed: %q, %v", got, err)
	}
}

func TestOpenRejectsMalformedGlobalDocuments(t *testing.T) {
	for _, raw := range []string{"null\n", "[]\n", "\n", "trusted_offices: []\n---\nplugins: {}\n", "plugins: {typo: true}\n", "trusted_offices: [relative]\n"} {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("OMO_HOME", dir)
			if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(); err == nil {
				t.Fatal("invalid global configuration accepted")
			}
			after, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
			if err != nil || string(after) != raw {
				t.Fatalf("invalid user config was overwritten: %q %v", after, err)
			}
		})
	}
}

func TestTrustPreservesSettingsAndConcurrentApprovals(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	raw := "# global comment\ntrusted_offices: []\nplugins:\n  update_on_start: false\n  installed:\n    example:\n      source: https://example.com/plugin.git\n      enabled: true\n      config: {message: hello}\n"
	if err := os.WriteFile(filepath.Join(h.Dir, "config.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	dirs := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	var wg sync.WaitGroup
	for _, dir := range dirs {
		wg.Go(func() {
			other, err := Open()
			if err == nil {
				err = other.Trust(dir)
			}
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	h, err = Open()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		canonical, err := CanonicalOffice(dir)
		if err != nil || !h.IsTrusted(canonical) {
			t.Fatalf("missing trust %s: %v", dir, err)
		}
	}
	if h.Config.Plugins.UpdateOnStart || h.Config.Plugins.Installed["example"].Config["message"] != "hello" {
		t.Fatalf("settings lost: %+v", h.Config)
	}
	data, err := os.ReadFile(filepath.Join(h.Dir, "config.yaml"))
	if err != nil || !strings.Contains(string(data), "# global comment") {
		t.Fatalf("comment lost: %s, %v", data, err)
	}
	if err := h.Trust(dirs[0]); err != nil {
		t.Fatal(err)
	}
	if len(h.Config.TrustedOffices) != 3 {
		t.Fatalf("duplicate trust: %v", h.Config.TrustedOffices)
	}
}

func TestUntrustRemovesExistingAndStaleOfficesPreservingConfig(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	raw := "# keep this global comment\ntrusted_offices: []\nplugins:\n  update_on_start: false\n  installed:\n    example:\n      source: https://example.com/plugin.git\n      enabled: true\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	office := filepath.Join(t.TempDir(), "office")
	if err := os.Mkdir(office, 0700); err != nil {
		t.Fatal(err)
	}
	if err := h.Trust(office); err != nil {
		t.Fatal(err)
	}
	if err := h.Untrust(office); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(h.Config.TrustedOffices, office) {
		t.Fatalf("office remained trusted: %v", h.Config.TrustedOffices)
	}
	reopened, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(reopened.Config.TrustedOffices, office) {
		t.Fatalf("office remained trusted after reopen: %v", reopened.Config.TrustedOffices)
	}
	data, err := os.ReadFile(filepath.Join(root, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# keep this global comment") || !strings.Contains(string(data), "example.com/plugin.git") || strings.Contains(string(data), office) {
		t.Fatalf("config was not preserved while removing office:\n%s", data)
	}

	stale := filepath.Join(t.TempDir(), "stale-office")
	if err := os.Mkdir(stale, 0700); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Trust(stale); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stale); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Untrust(stale); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(reopened.Config.TrustedOffices, stale) {
		t.Fatalf("stale office remained trusted: %v", reopened.Config.TrustedOffices)
	}
}

func TestUntrustUnknownIsNoOp(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.Dir, "config.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(t.TempDir(), "unknown-office")
	if err := h.Untrust(unknown); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("unknown untrust rewrote config: before %q after %q", before, after)
	}
}

func TestUntrustSerializesWithTrust(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	raw := "# keep this global comment\ntrusted_offices: []\nplugins:\n  update_on_start: false\n  installed:\n    example:\n      source: https://example.com/plugin.git\n      enabled: true\n"
	if err := os.WriteFile(filepath.Join(h.Dir, "config.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	a := t.TempDir()
	b := t.TempDir()
	if err := h.Trust(a); err != nil {
		t.Fatal(err)
	}
	untrustHome, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	trustHome, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := untrustHome.Untrust(a); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := trustHome.Trust(b); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()

	final, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if final.IsTrusted(a) || !final.IsTrusted(b) {
		t.Fatalf("concurrent trust state = %v", final.Config.TrustedOffices)
	}
	data, err := os.ReadFile(filepath.Join(h.Dir, "config.yaml"))
	if err != nil || !strings.Contains(string(data), "# keep this global comment") || !strings.Contains(string(data), "example.com/plugin.git") {
		t.Fatalf("concurrent mutation damaged config: %s, %v", data, err)
	}
}

func TestCanonicalTrustResolvesSymlink(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(target, link); err != nil {
		t.Skip(err)
	}
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Trust(link); err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalOffice(target)
	if err != nil || !h.IsTrusted(canonical) {
		t.Fatalf("alias trust failed: %v", err)
	}
	if _, err := CanonicalOffice(filepath.Join(target, "missing")); err == nil {
		t.Fatal("missing location accepted")
	}
}

func TestResolvePlatformPaths(t *testing.T) {
	user := t.TempDir()
	app := filepath.Join(user, "roaming")
	custom := filepath.Join(user, "custom")
	for _, tt := range []struct {
		platform, user, app, override, want string
		bad                                 bool
	}{
		{platform: "linux", user: user, want: filepath.Join(user, ".local", "omo")},
		{platform: "darwin", user: user, want: filepath.Join(user, ".local", "omo")},
		{platform: "windows", app: app, want: filepath.Join(app, "omo")},
		{platform: "windows", bad: true},
		{platform: "linux", override: "relative", bad: true},
		{platform: "linux", override: custom, want: custom},
	} {
		got, err := resolveDir(tt.platform, tt.user, tt.app, tt.override)
		if (err != nil) != tt.bad || got != filepath.FromSlash(tt.want) {
			t.Errorf("%+v: %q, %v", tt, got, err)
		}
	}
}
