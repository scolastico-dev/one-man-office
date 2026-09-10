package bundledplugins

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	userHalt := len(os.Args) == 3 && os.Args[1] == "office" && os.Args[2] == "halt-spawns"
	if capture == "" || (os.Getenv("OMO_PLUGIN_NAME") != ToolsName && !userHalt) {
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

func TestDefaultFilesIncludeGlobalFilebrowserSeed(t *testing.T) {
	files, err := DefaultFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"filebrowser/plugin.json", "filebrowser/browser.js"} {
		found := false
		for _, path := range files {
			if path == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("bundled files do not include %q: %v", want, files)
		}
	}
}

func TestBundledPluginsDeclareInstallationScopes(t *testing.T) {
	for name, want := range map[string]Scope{
		NudgeName:       OfficeScope,
		ToolsName:       OfficeScope,
		FilebrowserName: GlobalScope,
	} {
		definition, ok := DefinitionFor(name)
		if !ok || definition.Name != name || definition.Scope != want {
			t.Fatalf("definition %q = %+v, present=%v; want scope %q", name, definition, ok, want)
		}
	}
}

func TestOfficeFilesExcludeGlobalPlugins(t *testing.T) {
	files, err := OfficeFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasPrefix(path, "filebrowser/") {
			t.Fatalf("global filebrowser asset included in office files: %q", path)
		}
	}
}

func TestGlobalFilebrowserManifestIsValid(t *testing.T) {
	manifest, err := internalplugins.ReadManifest(filepath.Join("filebrowser"))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "filebrowser" || len(manifest.Hooks) != 1 || manifest.Hooks[0].Event != internalplugins.EventCompanyLoad {
		t.Fatalf("manifest = %+v", manifest)
	}
	if manifest.Hooks[0].Javascript != "browser.js" {
		t.Fatalf("company_load hook = %+v", manifest.Hooks[0])
	}
	for key, want := range map[string]any{
		"download_warn_bytes": int64(52428800),
		"download_max_bytes":  int64(1073741824),
		"upload_warn_bytes":   int64(52428800),
		"upload_max_bytes":    int64(1073741824),
	} {
		if got := manifest.DefaultConfig[key]; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("default_config[%q] = %#v, want %#v", key, got, want)
		}
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
	wantActions := []string{"freeze-office", "repository-cleanup", "storage-cleanup", "security-audit", "dependency-audit", "quality-audit"}
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
	if len(records) != len(wantActions)+1 {
		t.Fatalf("command records = %d, want %d", len(records), len(wantActions)+1)
	}
	for i, record := range records {
		if i == 0 {
			if len(record.Args) != 2 || record.Args[0] != "office" || record.Args[1] != "halt-spawns" {
				t.Fatalf("freeze halt arguments = %q", record.Args)
			}
			if record.Env["OMO_AGENT_ID"] != "" || record.Env["OMO_SOCKET"] != "" || record.Env["OMO_OFFICE_DIR"] != office || record.Env["OMO_PLUGIN_NAME"] != "" || record.Env["OMO_PLUGIN_EVENT"] != "" {
				t.Fatalf("freeze halt environment = %#v", record.Env)
			}
			continue
		}
		if record.Env["OMO_AGENT_ID"] != "" || record.Env["OMO_SOCKET"] != "" || record.Env["OMO_OFFICE_DIR"] != office || record.Env["OMO_PLUGIN_NAME"] != ToolsName || record.Env["OMO_PLUGIN_EVENT"] != "manual" {
			t.Fatalf("command %d environment = %#v", i, record.Env)
		}
		if i == 1 {
			if len(record.Args) != 6 || record.Args[0] != "send" || record.Args[1] != "-s" || record.Args[2] != "Office frozen" || record.Args[3] != "-p" || record.Args[4] != "urgent" {
				t.Fatalf("freeze broadcast arguments = %q", record.Args)
			}
			for _, phrase := range []string{"Spawning has already been halted", "run `omo wait`", "If you are the CEO", "global wake-up mail", "omo office resume-spawns"} {
				if !strings.Contains(record.Args[5], phrase) {
					t.Fatalf("freeze broadcast missing %q: %q", phrase, record.Args[5])
				}
			}
			continue
		}
		if len(record.Args) != 8 || record.Args[0] != "send" || record.Args[1] != "-t" || record.Args[2] != "ceo" || record.Args[3] != "-s" || record.Args[5] != "-p" || record.Args[6] != "normal" {
			t.Fatalf("command %d arguments = %q", i, record.Args)
		}
		for _, phrase := range []string{"After current work", "queue and delegate", "Inspect before deleting", "Preserve user work", "Avoid destructive shortcuts"} {
			if !strings.Contains(record.Args[7], phrase) {
				t.Fatalf("command %d prompt missing %q: %q", i, phrase, record.Args[7])
			}
		}
	}
}
