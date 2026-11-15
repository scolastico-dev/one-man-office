package bundledplugins

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

// TestOnlyPushoverEmbedded pins the bundled set: pushover and nothing else
// is in the binary or offered; bugreport stays a source in the repository.
func TestOnlyPushoverEmbedded(t *testing.T) {
	if got := Names(); !reflect.DeepEqual(got, []string{Pushover}) {
		t.Fatalf("Names() = %v, want [pushover]", got)
	}
	top, err := fs.ReadDir(files, ".")
	if err != nil {
		t.Fatal(err)
	}
	var embedded []string
	for _, e := range top {
		embedded = append(embedded, e.Name())
	}
	if !reflect.DeepEqual(embedded, []string{Pushover}) {
		t.Fatalf("embedded folders = %v, want [pushover]", embedded)
	}
	for _, name := range []string{"bugreport", "nudge", "tools", "filebrowser", "pullrequest", "autoshutdown", "", "../pushover"} {
		if Has(name) {
			t.Errorf("Has(%q) = true", name)
		}
		if _, err := Files(name); err == nil {
			t.Errorf("Files(%q) gave no error", name)
		}
	}
	got, err := Files(Pushover)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"README.md", "notify.lua", "plugin.json", "test.lua"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Files(pushover) = %v, want %v", got, want)
	}
}

// TestBundledManifestsAreValid checks every bundled manifest with the
// runtime's own parser.
func TestBundledManifestsAreValid(t *testing.T) {
	for _, name := range Names() {
		raw, err := ReadFile(name, plugins.ManifestFile)
		if err != nil {
			t.Fatal(err)
		}
		m, err := plugins.ParseManifest(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if m.Name != name || m.Description == "" || m.Version == "" {
			t.Fatalf("%s: manifest name %q, version %q, description %q", name, m.Name, m.Version, m.Description)
		}
		for _, h := range m.Hooks {
			if h.Lua != "" {
				if _, err := ReadFile(name, h.Lua); err != nil {
					t.Errorf("%s: hook %s names %s: %v", name, h.Event, h.Lua, err)
				}
			}
		}
	}
}

func TestPushoverManifestHooks(t *testing.T) {
	raw, err := ReadFile(Pushover, plugins.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	m, err := plugins.ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, h := range m.Hooks {
		events = append(events, h.Event)
		if h.Event == plugins.EventManual && (h.Name != "test" || h.Scope != plugins.ScopeGlobal) {
			t.Errorf("manual hook %q scope %q, want test, global", h.Name, h.Scope)
		}
	}
	want := []string{plugins.EventNeedsAttention, plugins.EventQuestionAsked, plugins.EventManual}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for _, key := range []string{"user_key", "app_token", "api_url", "priority", "sound"} {
		if _, ok := m.DefaultConfig[key]; !ok {
			t.Errorf("default_config has no %s", key)
		}
	}
	if m.DefaultConfig["api_url"] != "https://api.pushover.net/1/messages.json" {
		t.Errorf("api_url default = %v", m.DefaultConfig["api_url"])
	}
}

func TestDigestIsStableAndNamed(t *testing.T) {
	a, err := Digest(Pushover)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Digest(Pushover)
	if a != b || len(a) != 64 {
		t.Fatalf("digest %q then %q", a, b)
	}
	if _, err := Digest("bugreport"); err == nil {
		t.Fatal("Digest(bugreport) gave no error")
	}
}

func TestCopyToWritesTheFiles(t *testing.T) {
	dir := t.TempDir()
	if err := CopyTo(Pushover, dir); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"plugin.json", "notify.lua", "test.lua", "README.md"} {
		info, err := os.Lstat(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s: mode %v", rel, info.Mode())
		}
	}
	if _, err := plugins.ReadManifest(dir); err != nil {
		t.Fatal(err)
	}
	if err := CopyTo("bugreport", t.TempDir()); err == nil {
		t.Fatal("CopyTo(bugreport) gave no error")
	}
}
