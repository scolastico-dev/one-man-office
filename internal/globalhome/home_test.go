package globalhome

import (
	"os"
	"path/filepath"
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
		if err != nil || len(entries) != 0 {
			t.Fatalf("%s = %v, %v", name, entries, err)
		}
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
