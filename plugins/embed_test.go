package bundledplugins

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/db"
	internalplugins "github.com/scolastico-dev/one-man-office/internal/plugins"
)

type toolsCommandRecord struct {
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
}

func TestBundledToolsCommandHelper(t *testing.T) {
	capture := os.Getenv("OMO_TOOLS_CAPTURE")
	if capture == "" || os.Getenv("OMO_PLUGIN_NAME") != ToolsName {
		return
	}
	record := toolsCommandRecord{Args: os.Args[1:], Env: map[string]string{}}
	for _, key := range []string{"OMO_AGENT_ID", "OMO_SOCKET", "OMO_OFFICE_DIR", "OMO_PLUGIN_NAME", "OMO_PLUGIN_EVENT"} {
		record.Env[key] = os.Getenv(key)
	}
	raw, err := json.Marshal(record)
	if err != nil {
		os.Exit(2)
	}
	f, err := os.OpenFile(capture, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(2)
	}
	_, err = f.Write(append(raw, '\n'))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestEnsureNudgeInstallsOnceAndPreservesEdits(t *testing.T) {
	office := t.TempDir()
	installed, err := EnsureNudge(office)
	if err != nil || !installed {
		t.Fatalf("first ensure installed=%v err=%v", installed, err)
	}
	script := filepath.Join(office, ".omo", "plugins", "nudge", "nudge.lua")
	if _, err := os.Stat(filepath.Join(office, ".omo", "plugins", "nudge", "plugin.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("-- customized"), 0o644); err != nil {
		t.Fatal(err)
	}
	installed, err = EnsureNudge(office)
	if err != nil || installed {
		t.Fatalf("second ensure installed=%v err=%v", installed, err)
	}
	raw, err := os.ReadFile(script)
	if err != nil || string(raw) != "-- customized" {
		t.Fatalf("existing plugin was overwritten: %q err=%v", raw, err)
	}
}

func TestBundledToolsPluginRunsManualCommandsAsSystemSender(t *testing.T) {
	office := t.TempDir()
	if installed, err := EnsureTools(office); err != nil || !installed {
		t.Fatalf("ensure tools installed=%v err=%v", installed, err)
	}
	database, err := db.Open(filepath.Join(office, ".omo", "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })

	binDir := t.TempDir()
	tool := filepath.Join(binDir, "omo")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	from, err := os.Open(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	to, err := os.OpenFile(tool, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		from.Close()
		t.Fatal(err)
	}
	_, err = io.Copy(to, from)
	if closeErr := from.Close(); err == nil {
		err = closeErr
	}
	if closeErr := to.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(t.TempDir(), "tools-commands.jsonl")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OMO_TOOLS_CAPTURE", capture)
	t.Setenv("OMO_AGENT_ID", "developer-ada")
	t.Setenv("OMO_SOCKET", "/tmp/developer.sock")

	manager, err := internalplugins.Load(office, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	wantActions := []string{"repository-cleanup", "storage-cleanup", "security-audit", "dependency-audit", "quality-audit"}
	actions := manager.ManualActions(ToolsName)
	if len(actions) != len(wantActions) {
		t.Fatalf("tools actions = %+v", actions)
	}
	for i, want := range wantActions {
		if actions[i].Name != want || actions[i].Description == "" || actions[i].ManualArgs {
			t.Fatalf("tools action %d = %+v, want %q with a description and no arguments", i, actions[i], want)
		}
		if err := manager.TriggerManual(ToolsName, want, "user", nil); err != nil {
			t.Fatalf("trigger %s: %v", want, err)
		}
	}

	f, err := os.Open(capture)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var records []toolsCommandRecord
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var record toolsCommandRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(records) != len(wantActions) {
		t.Fatalf("command records = %d, want %d", len(records), len(wantActions))
	}
	for i, record := range records {
		if len(record.Args) != 8 || record.Args[0] != "send" || record.Args[1] != "-t" || record.Args[2] != "ceo" || record.Args[3] != "-s" || record.Args[5] != "-p" || record.Args[6] != "normal" {
			t.Fatalf("command %d arguments = %q", i, record.Args)
		}
		for _, phrase := range []string{"After current work", "queue and delegate", "Inspect before deleting", "Preserve user work", "Avoid destructive shortcuts"} {
			if !strings.Contains(record.Args[7], phrase) {
				t.Fatalf("command %d prompt missing %q: %q", i, phrase, record.Args[7])
			}
		}
		if record.Env["OMO_AGENT_ID"] != "" || record.Env["OMO_SOCKET"] != "" || record.Env["OMO_OFFICE_DIR"] != office || record.Env["OMO_PLUGIN_NAME"] != ToolsName || record.Env["OMO_PLUGIN_EVENT"] != "manual" {
			t.Fatalf("command %d environment = %#v", i, record.Env)
		}
	}
}
