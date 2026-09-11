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
