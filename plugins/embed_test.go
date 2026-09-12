package bundledplugins

import (
	"bufio"
	"crypto/sha256"
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
	for _, want := range []string{"filebrowser/plugin.json", "filebrowser/company.lua", "filebrowser/web/main.js", "filebrowser/web/commands.js", "filebrowser/web/helpers.js", "filebrowser/web/style.css"} {
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
	for _, path := range files {
		if path == "filebrowser/browser.js" {
			t.Fatalf("bundled files include obsolete filebrowser entrypoint: %q", path)
		}
	}
}

func TestEnsureAtRefreshesOwnedFilebrowserAndPreservesOnlyEmbeddedTree(t *testing.T) {
	root := t.TempDir()
	created, err := EnsureAt(root, FilebrowserName)
	if err != nil || !created {
		t.Fatalf("first install = %v, %v", created, err)
	}
	markerPath := filepath.Join(root, FilebrowserName, toolsMarker)
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := embeddedPluginDigest(t, FilebrowserName)
	if got, want := string(marker), "source=builtin:filebrowser\ndigest="+wantDigest+"\n"; got != want {
		t.Fatalf("marker = %q, want %q", got, want)
	}

	mutated := filepath.Join(root, FilebrowserName, "web", "main.js")
	if err := os.WriteFile(mutated, []byte("customized\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FilebrowserName, "stale.txt"), []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, []byte("source=builtin:filebrowser\ndigest="+strings.Repeat("0", 64)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	updated, err := EnsureAt(root, FilebrowserName)
	if err != nil || !updated {
		t.Fatalf("stale refresh = %v, %v", updated, err)
	}
	assertEmbeddedPluginTree(t, root, FilebrowserName)
}

func TestEnsureAtLeavesCurrentOwnedFilebrowserUntouched(t *testing.T) {
	root := t.TempDir()
	if created, err := EnsureAt(root, FilebrowserName); err != nil || !created {
		t.Fatalf("first install = %v, %v", created, err)
	}
	path := filepath.Join(root, FilebrowserName, "web", "main.js")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := EnsureAt(root, FilebrowserName)
	if err != nil || updated {
		t.Fatalf("current ensure = %v, %v", updated, err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("current owned filebrowser was rewritten: before=%v after=%v", before.ModTime(), after.ModTime())
	}
}

func TestEnsureAtLeavesUnownedFilebrowserUntouched(t *testing.T) {
	for _, tc := range []struct {
		name   string
		marker string
	}{
		{name: "foreign source", marker: "source=builtin:someone-else\ndigest=" + strings.Repeat("0", 64) + "\n"},
		{name: "malformed marker", marker: "not a marker\n"},
		{name: "no marker"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, FilebrowserName)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(dir, "sentinel.txt")
			if err := os.WriteFile(sentinel, []byte("user-owned\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.marker != "" {
				if err := os.WriteFile(filepath.Join(dir, toolsMarker), []byte(tc.marker), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			updated, err := EnsureAt(root, FilebrowserName)
			if err != nil || updated {
				t.Fatalf("unowned ensure = %v, %v", updated, err)
			}
			got, err := os.ReadFile(sentinel)
			if err != nil || string(got) != "user-owned\n" {
				t.Fatalf("sentinel = %q, %v", got, err)
			}
		})
	}
}

func TestReadMarkerRejectsMalformedMarkers(t *testing.T) {
	for _, raw := range []string{
		"source=builtin:filebrowser\ndigest=\n",
		"source=builtin:filebrowser\ndigest=" + strings.Repeat("0", 63) + "\n",
		"source=builtin:filebrowser\ndigest=" + strings.Repeat("A", 64) + "\n",
		"source=builtin:filebrowser\ndigest=" + strings.Repeat("0", 64),
		"source=builtin:filebrowser\ndigest=" + strings.Repeat("0", 64) + "\nextra\n",
		"source=filebrowser\ndigest=" + strings.Repeat("0", 64) + "\n",
		"source=builtin:filebrowser=other\ndigest=" + strings.Repeat("0", 64) + "\n",
	} {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), toolsMarker)
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadMarker(path); err == nil {
				t.Fatalf("malformed marker accepted: %q", raw)
			}
		})
	}
}

func embeddedPluginDigest(t *testing.T, name string) string {
	t.Helper()
	h := sha256.New()
	paths, err := filesForScope(func(definition Definition) bool { return definition.Name == name })
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		rel := strings.TrimPrefix(path, name+"/")
		raw, err := files.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(h, "%s\x00", rel)
		_, _ = h.Write(raw)
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestPluginDigestUsesRelativePathFraming(t *testing.T) {
	got, err := PluginDigest(FilebrowserName)
	if err != nil {
		t.Fatal(err)
	}
	if want := embeddedPluginDigest(t, FilebrowserName); got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
}

func assertEmbeddedPluginTree(t *testing.T, root, name string) {
	t.Helper()
	paths, err := filesForScope(func(definition Definition) bool { return definition.Name == name })
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		rel := strings.TrimPrefix(path, name+"/")
		want, err := files.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(root, name, filepath.FromSlash(rel)))
		if err != nil || string(got) != string(want) {
			t.Fatalf("embedded %s = %q, want %q; err=%v", rel, got, want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, name, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale file remains: %v", err)
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
	if manifest.Name != "filebrowser" || len(manifest.Hooks) != 4 {
		t.Fatalf("manifest = %+v", manifest)
	}
	var companyLoad internalplugins.Hook
	for _, hook := range manifest.Hooks {
		if hook.Event == internalplugins.EventCompanyLoad {
			companyLoad = hook
		}
	}
	if companyLoad.Javascript != "web/main.js" {
		t.Fatalf("company_load hook = %+v", companyLoad)
	}
	for _, path := range append([]string{companyLoad.Javascript, "company.lua"}, companyLoad.Files...) {
		if _, err := os.Stat(filepath.Join("filebrowser", filepath.FromSlash(path))); err != nil {
			t.Fatalf("declared file %q is missing: %v", path, err)
		}
	}
	for key, want := range map[string]any{
		"download_warn_bytes": int64(52428800),
		"upload_warn_bytes":   int64(52428800),
		"upload_max_bytes":    int64(1073741824),
	} {
		if got := manifest.DefaultConfig[key]; fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("default_config[%q] = %#v, want %#v", key, got, want)
		}
	}
	if _, ok := manifest.DefaultConfig["download_max_bytes"]; ok {
		t.Fatal("download_max_bytes must not be part of the served-link configuration")
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
