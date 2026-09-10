package companyservice

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAutostartRegisterReplaceUnregisterPreservesLiteralArguments(t *testing.T) {
	dir := isolatedHome(t)
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	unrelated := filepath.Join(root, "autostart", "unrelated.desktop")
	if err := os.MkdirAll(filepath.Dir(unrelated), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"company", "--listen=127.0.0.1:0", "--basic-auth=user:p a'ss\"$HOME`id`%&\\word", "--mock=false"}
	location, err := RegisterAutostart(context.Background(), "/opt/omo tools/omo", args)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "autostart.json")
	saved, err := LoadAutostart(config)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(saved.Args, args) {
		t.Fatalf("args changed: %#v", saved.Args)
	}
	cwd, _ := os.Getwd()
	if saved.Home != os.Getenv("OMO_HOME") || saved.Path != os.Getenv("PATH") || saved.Directory != cwd {
		t.Fatalf("launch context changed: %+v", saved)
	}
	info, err := os.Stat(config)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("settings permissions: %v %v", info, err)
	}
	data, err := os.ReadFile(location)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "basic-auth") || strings.Contains(string(data), "p a'ss") {
		t.Fatal("credentials leaked into desktop entry")
	}
	if tool, err := exec.LookPath("desktop-file-validate"); err == nil {
		if output, err := exec.Command(tool, location).CombinedOutput(); err != nil {
			t.Fatalf("invalid desktop entry: %s %v", output, err)
		}
	}
	updated := []string{"company", "--listen=127.0.0.1:8099"}
	second, err := RegisterAutostart(context.Background(), "/opt/new omo", updated)
	if err != nil || second != location {
		t.Fatalf("replace: %s %v", second, err)
	}
	saved, err = LoadAutostart(config)
	if err != nil || !reflect.DeepEqual(saved.Args, updated) {
		t.Fatalf("updated settings: %+v %v", saved, err)
	}
	// A failed native entry update restores the previous settings.
	if _, err := RegisterAutostart(context.Background(), "/bad\npath", args); err == nil {
		t.Fatal("accepted invalid executable")
	}
	saved, err = LoadAutostart(config)
	if err != nil || !reflect.DeepEqual(saved.Args, updated) {
		t.Fatalf("rollback lost settings: %+v %v", saved, err)
	}
	for range 2 {
		if err := UnregisterAutostart(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{location, config} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("retained %s: %v", path, err)
		}
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "keep" {
		t.Fatal("changed unrelated autostart entry")
	}
}

func TestDesktopLaunchPassesLiteralPathsThroughNativeParser(t *testing.T) {
	gio, err := exec.LookPath("gio")
	if err != nil {
		t.Skip("gio is not installed")
	}
	if err := exec.Command(gio, "version").Run(); err != nil {
		t.Skipf("gio cannot run: %v", err)
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "argv.json")
	t.Setenv("OMO_TEST_AUTOSTART_ARGV", output)
	executable := filepath.Join(dir, "omo $` '\"&\\ executable")
	source := "#!" + python + "\nimport json, os, sys\nwith open(os.environ['OMO_TEST_AUTOSTART_ARGV'], 'w') as f: json.dump(sys.argv[1:], f)\n"
	if err := os.WriteFile(executable, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "%$HOME`id` '\"&\\ settings.json")
	data, err := desktopEntry(executable, config)
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "test.desktop")
	if err := os.WriteFile(entry, data, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(gio, "launch", entry)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native desktop parser: %s %v; entry: %s", output, err, data)
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("desktop helper did not write its argv")
		case <-ticker.C:
			data, err := os.ReadFile(output)
			if err != nil {
				continue
			}
			var args []string
			if err := json.Unmarshal(data, &args); err != nil {
				continue
			}
			if !reflect.DeepEqual(args, []string{"company", "autostart-run", config}) {
				t.Fatalf("native desktop parser changed arguments: %#v", args)
			}
			return
		}
	}
}
