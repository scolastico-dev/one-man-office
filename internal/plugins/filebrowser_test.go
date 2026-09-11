package plugins

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFilebrowserManualDownloadPublishesServedLinkAndShutdownSweep(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX link behavior is covered on Unix; Windows is exercised by the PowerShell path tests")
	}
	office, database := newPluginOffice(t)
	pluginDir := filepath.Join(office, Dir, "filebrowser")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lua, err := os.ReadFile(filepath.Join("..", "..", "plugins", "filebrowser", "company.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "company.lua"), lua, 0o644); err != nil {
		t.Fatal(err)
	}
	writePlugin(t, pluginDir, Manifest{Name: "filebrowser", Hooks: []Hook{
		{Event: EventManual, Name: "download", Description: "Serve a file", Roles: []string{"user"}, ManualArgs: true, Lua: "company.lua"},
		{Event: EventCompanyStartup, Lua: "company.lua"},
		{Event: EventCompanyShutdown, Lua: "company.lua"},
	}}, "")
	if _, err := os.Stat(filepath.Join(pluginDir, "company.lua")); err != nil {
		t.Fatal(err)
	}
	m, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	home := office
	if err := os.MkdirAll(filepath.Join(home, "company", "http"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EmitLifecycle(context.Background(), Event{Name: EventCompanyStartup, Data: map[string]any{"home_path": home}}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(office, "source [special] #1.txt")
	if err := os.WriteFile(target, []byte("served"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := m.TriggerManualContextWithRoleResult(context.Background(), "filebrowser", "download", "user", "user", []string{target})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := result.Value.(map[string]any)
	if !ok || !strings.HasPrefix(value["url"].(string), "/filebrowser/") {
		t.Fatalf("download result = %#v", result.Value)
	}
	servedPath, err := url.PathUnescape(strings.TrimPrefix(value["url"].(string), "/"))
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "company", "http", filepath.FromSlash(servedPath))
	data, err := os.ReadFile(link)
	if err != nil || string(data) != "served" {
		t.Fatalf("served link %q: %q: %v", link, data, err)
	}
	unrelated := filepath.Join(home, "company", "http", "overlay.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EmitLifecycle(context.Background(), Event{Name: EventCompanyShutdown, Data: map[string]any{"home_path": home}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(link); !os.IsNotExist(err) {
		t.Fatalf("served link survived shutdown: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("link target was removed: %v", err)
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "keep" {
		t.Fatalf("unrelated overlay changed: %q: %v", data, err)
	}
}

func TestFilebrowserLifecycleRejectsSymlinkedServeRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink root behavior is covered on Unix")
	}
	office, database := newPluginOffice(t)
	pluginDir := filepath.Join(office, Dir, "filebrowser")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lua, err := os.ReadFile(filepath.Join("..", "..", "plugins", "filebrowser", "company.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "company.lua"), lua, 0o644); err != nil {
		t.Fatal(err)
	}
	writePlugin(t, pluginDir, Manifest{Name: "filebrowser", Hooks: []Hook{{Event: EventCompanyStartup, Lua: "company.lua"}, {Event: EventCompanyShutdown, Lua: "company.lua"}}}, "")
	m, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	home := office
	httpRoot := filepath.Join(home, "company", "http")
	if err := os.MkdirAll(httpRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	sentinel := filepath.Join(target, "must-survive.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	serveRoot := filepath.Join(httpRoot, "filebrowser")
	if err := os.Symlink(target, serveRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EmitLifecycle(context.Background(), Event{Name: EventCompanyStartup, Data: map[string]any{"home_path": home}}); err == nil {
		t.Fatal("startup accepted a symlinked filebrowser root")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "keep" {
		t.Fatalf("symlink target changed: %q: %v", data, err)
	}
	info, err := os.Lstat(serveRoot)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("startup replaced the symlink instead of rejecting it")
	}
}

func TestFilebrowserWindowsDownloadScriptUsesPowerShell5AndLinkFallbacks(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "plugins", "filebrowser", "company.lua"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"RandomNumberGenerator]::Create()",
		"$rng.GetBytes($bytes)",
		"$rng.Dispose()",
		"New-Item -ItemType HardLink",
		"New-Item -ItemType SymbolicLink",
		"Copy-Item -LiteralPath $Target -Destination $Link",
		"FileAttributes]::ReparsePoint",
		"for _ = 1, 8 do",
		"-ErrorAction Stop",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("company.lua missing %q", required)
		}
	}
	if strings.Contains(text, "RandomNumberGenerator]::Fill") {
		t.Fatal("company.lua still uses the unavailable .NET Framework RandomNumberGenerator.Fill API")
	}
}

func TestFilebrowserDownloadRetriesCollidingWindowsDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test harness supplies fake PowerShell executables")
	}
	office, database := newPluginOffice(t)
	pluginDir := filepath.Join(office, Dir, "filebrowser")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lua, err := os.ReadFile(filepath.Join("..", "..", "plugins", "filebrowser", "company.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "company.lua"), lua, 0o644); err != nil {
		t.Fatal(err)
	}
	writePlugin(t, pluginDir, Manifest{Name: "filebrowser", Hooks: []Hook{{Event: EventManual, Name: "download", Description: "Serve a file", Roles: []string{"user"}, ManualArgs: true, Lua: "company.lua"}}}, "")
	bin := t.TempDir()
	state := t.TempDir()
	t.Setenv("FILEBROWSER_FAKE_STATE", state)
	for name, body := range map[string]string{
		"uname": `#!/bin/sh
printf 'Windows_NT\n'`,
		"pwsh": `#!/bin/sh
state=${FILEBROWSER_FAKE_STATE:?}
script=$4
arg=$5
printf '%s|%s\n' "$script" "$arg" >> "$state/log"
case "$script" in
  *RandomNumberGenerator*)
    if [ ! -e "$state/random" ]; then touch "$state/random"; printf '11111111111111111111111111111111\n'; else printf '22222222222222222222222222222222\n'; fi
    exit 0
    ;;
  *'New-Item -ItemType Directory'*)
    if [ -d "$arg" ]; then exit 0; fi
    if [ ! -e "$state/root" ]; then mkdir -p "$arg"; touch "$state/root"; exit 0; fi
    if [ ! -e "$state/collision" ]; then touch "$state/collision"; exit 1; fi
    mkdir -p "$arg"; exit 0
    ;;
  *'Get-Item -LiteralPath'*) exit 0 ;;
  *'HardLink'*|*'SymbolicLink'*|*'Copy-Item'*) exit 0 ;;
  *) exit 0 ;;
esac`,
	} {
		path := filepath.Join(bin, name)
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	m, err := Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	target := filepath.Join(office, "source.txt")
	if err := os.WriteFile(target, []byte("served"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := m.TriggerManualContextWithRoleResult(context.Background(), "filebrowser", "download", "user", "user", []string{target})
	if err != nil {
		t.Fatal(err)
	}
	value, ok := result.Value.(map[string]any)
	if !ok || !strings.Contains(value["url"].(string), "22222222222222222222222222222222") {
		log, _ := os.ReadFile(filepath.Join(state, "log"))
		t.Fatalf("collision retry result = %#v; fake calls = %s", result.Value, log)
	}
	if _, err := os.Stat(filepath.Join(state, "collision")); err != nil {
		t.Fatalf("fake PowerShell did not exercise the colliding allocation: %v", err)
	}
}
