package plugins

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestAutoShutdownManifestDeclaresContractDefaults(t *testing.T) {
	manifest, err := ReadManifest(autoShutdownSourceDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "autoshutdown" {
		t.Fatalf("manifest name = %q", manifest.Name)
	}
	if manifest.DefaultConfig["idle_after"] != "30m" || manifest.DefaultConfig["check_interval"] != "30s" {
		t.Fatalf("manifest duration defaults = %#v", manifest.DefaultConfig)
	}
	exempt, ok := manifest.DefaultConfig["exempt_roles"].([]any)
	if !ok || len(exempt) != 2 || exempt[0] != "ceo" || exempt[1] != "smokealarm" {
		t.Fatalf("manifest exempt defaults = %#v", manifest.DefaultConfig["exempt_roles"])
	}
	if len(manifest.Hooks) != 1 || manifest.Hooks[0].Event != EventCron || manifest.Hooks[0].IntervalConfig != "check_interval" || manifest.Hooks[0].Interval != "30s" {
		t.Fatalf("manifest cron hook = %#v", manifest.Hooks)
	}
}

func TestAutoShutdownStartupGraceBlocksIdleOffice(t *testing.T) {
	manager, _, record := newAutoShutdownManager(t, map[string]any{"idle_after": "10s"})
	if _, err := manager.Emit(context.Background(), autoShutdownEvent(105, 100, false)); err != nil {
		t.Fatal(err)
	}
	if fileExists(record) {
		t.Fatal("autoshutdown ran during startup grace")
	}
}

func TestAutoShutdownRequiresFullQuietIntervalAfterFirstIdleTick(t *testing.T) {
	manager, database, record := newAutoShutdownManager(t, map[string]any{"idle_after": "30s"})
	for _, now := range []int64{110, 139} {
		if _, err := manager.Emit(context.Background(), autoShutdownEvent(now, 0, false)); err != nil {
			t.Fatal(err)
		}
	}
	if fileExists(record) {
		t.Fatal("autoshutdown ran before the full quiet interval")
	}
	if got := autoShutdownStorage(t, database, "idle_since"); got != "110" {
		t.Fatalf("idle_since = %q, want first idle tick", got)
	}
	if _, err := manager.Emit(context.Background(), autoShutdownEvent(140, 0, false)); err != nil {
		t.Fatal(err)
	}
	if count := autoShutdownInvocations(t, record); count != 1 {
		t.Fatalf("safe-shutdown invocations = %d, want 1", count)
	}
}

func TestAutoShutdownNonExemptAgentResetsWindowWhileExemptRolesDoNot(t *testing.T) {
	t.Run("non-exempt resets", func(t *testing.T) {
		manager, _, record := newAutoShutdownManager(t, map[string]any{"idle_after": "10s"})
		if _, err := manager.Emit(context.Background(), autoShutdownEventWithAgents(100, 0, false, map[string]string{"dev": "developer"})); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Emit(context.Background(), autoShutdownEventWithAgents(109, 0, false, nil)); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Emit(context.Background(), autoShutdownEventWithAgents(118, 0, false, nil)); err != nil {
			t.Fatal(err)
		}
		if fileExists(record) {
			t.Fatal("autoshutdown ignored non-exempt agent reset")
		}
		if _, err := manager.Emit(context.Background(), autoShutdownEventWithAgents(119, 0, false, nil)); err != nil {
			t.Fatal(err)
		}
		if count := autoShutdownInvocations(t, record); count != 1 {
			t.Fatalf("safe-shutdown invocations = %d, want 1", count)
		}
	})
	t.Run("exempt roles preserve quiet state", func(t *testing.T) {
		manager, _, record := newAutoShutdownManager(t, map[string]any{"idle_after": "10s"})
		if _, err := manager.Emit(context.Background(), autoShutdownEventWithAgents(100, 0, false, map[string]string{"ceo": "ceo"})); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Emit(context.Background(), autoShutdownEventWithAgents(105, 0, false, map[string]string{"alarm": "smokealarm"})); err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Emit(context.Background(), autoShutdownEventWithAgents(110, 0, false, nil)); err != nil {
			t.Fatal(err)
		}
		if count := autoShutdownInvocations(t, record); count != 1 {
			t.Fatalf("safe-shutdown invocations = %d, want exempt roles not to reset", count)
		}
	})
}

func TestAutoShutdownCEOActivityResetsIdleWindow(t *testing.T) {
	manager, _, record := newAutoShutdownManager(t, map[string]any{"idle_after": "10s"})
	if _, err := manager.Emit(context.Background(), autoShutdownEventWithCEOActivity(100, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), autoShutdownEventWithCEOActivity(105, 0, 104)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), autoShutdownEventWithCEOActivity(113, 0, 104)); err != nil {
		t.Fatal(err)
	}
	if fileExists(record) {
		t.Fatal("autoshutdown ignored newer CEO activity")
	}
	if _, err := manager.Emit(context.Background(), autoShutdownEventWithCEOActivity(114, 0, 104)); err != nil {
		t.Fatal(err)
	}
	if count := autoShutdownInvocations(t, record); count != 1 {
		t.Fatalf("safe-shutdown invocations = %d, want 1", count)
	}
}

func TestAutoShutdownShutdownInProgressPreventsExec(t *testing.T) {
	for _, kind := range []string{"safe", "usage"} {
		t.Run(kind, func(t *testing.T) {
			manager, _, record := newAutoShutdownManager(t, map[string]any{"idle_after": "10s"})
			for _, now := range []int64{100, 110, 120} {
				if _, err := manager.Emit(context.Background(), autoShutdownEvent(now, 0, true)); err != nil {
					t.Fatal(err)
				}
			}
			if fileExists(record) {
				t.Fatal("shutdown-in-progress snapshot still invoked safe-shutdown")
			}
		})
	}
}

func TestAutoShutdownSuccessfulExecFiresOnce(t *testing.T) {
	manager, database, record := newAutoShutdownManager(t, map[string]any{"idle_after": "10s"})
	for _, now := range []int64{100, 110, 120} {
		if _, err := manager.Emit(context.Background(), autoShutdownEvent(now, 0, false)); err != nil {
			t.Fatal(err)
		}
	}
	if count := autoShutdownInvocations(t, record); count != 1 {
		t.Fatalf("safe-shutdown invocations = %d, want one after success", count)
	}
	if got := autoShutdownStorage(t, database, "fired"); got != "true" {
		t.Fatalf("fired = %q, want true", got)
	}
}

func TestAutoShutdownExecFailureRetriesWithoutMarkerLeak(t *testing.T) {
	manager, database, record := newAutoShutdownManager(t, map[string]any{"idle_after": "10s"})
	t.Setenv("OMO_TEST_FAIL", "1")
	for _, now := range []int64{100, 110} {
		if _, err := manager.Emit(context.Background(), autoShutdownEvent(now, 0, false)); err != nil {
			t.Fatal(err)
		}
	}
	if got := autoShutdownStorage(t, database, "fired"); got != "" {
		t.Fatalf("fired after failed exec = %q, want unset", got)
	}
	t.Setenv("OMO_TEST_FAIL", "")
	if _, err := manager.Emit(context.Background(), autoShutdownEvent(111, 0, false)); err != nil {
		t.Fatal(err)
	}
	if count := autoShutdownInvocations(t, record); count != 2 {
		t.Fatalf("safe-shutdown retries = %d, want 2", count)
	}
	invocation := autoShutdownLastInvocation(t, record)
	if invocation.PluginName != "" || invocation.PluginEvent != "" {
		t.Fatalf("plugin marker leaked to safe-shutdown: %#v", invocation)
	}
	if len(invocation.Args) != 3 || invocation.Args[0] != "safe-shutdown" || invocation.Args[1] != "--reason" {
		t.Fatalf("safe-shutdown argv = %#v", invocation.Args)
	}
}

func TestAutoShutdownReconcilesStateForNewOfficeSession(t *testing.T) {
	manager, _, record := newAutoShutdownManager(t, map[string]any{"idle_after": "10s"})
	if _, err := manager.Emit(context.Background(), autoShutdownEvent(100, 0, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), autoShutdownEvent(110, 0, false)); err != nil {
		t.Fatal(err)
	}
	if autoShutdownInvocations(t, record) != 1 {
		t.Fatal("initial office session did not fire")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}

	manager, database, _ := newAutoShutdownManagerInOffice(t, manager.OfficeDir, map[string]any{"idle_after": "10s"})
	if _, err := manager.Emit(context.Background(), autoShutdownEvent(205, 200, false)); err != nil {
		t.Fatal(err)
	}
	if got := autoShutdownStorage(t, database, "fired"); got != "" {
		t.Fatalf("new session inherited fired state = %q", got)
	}
	if _, err := manager.Emit(context.Background(), autoShutdownEvent(210, 200, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), autoShutdownEvent(220, 200, false)); err != nil {
		t.Fatal(err)
	}
	if count := autoShutdownInvocations(t, record); count != 2 {
		t.Fatalf("new session invocations = %d, want 2 total", count)
	}
}

func TestAutoShutdownCountdownLogsAreThrottled(t *testing.T) {
	manager, database, _ := newAutoShutdownManager(t, map[string]any{"idle_after": "1000s"})
	for _, now := range []int64{1000, 1001, 1299, 1300} {
		if _, err := manager.Emit(context.Background(), autoShutdownEvent(now, 0, false)); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := db.PluginLogs(database, "autoshutdown")
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("countdown log count = %d, want 2: %+v", len(logs), logs)
	}
}

func TestAutoShutdownReasonUsesConfiguredDurationAndClearsMarkers(t *testing.T) {
	manager, _, record := newAutoShutdownManager(t, map[string]any{"idle_after": "17m"})
	if _, err := manager.Emit(context.Background(), autoShutdownEvent(1020, 0, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Emit(context.Background(), autoShutdownEvent(2040, 0, false)); err != nil {
		t.Fatal(err)
	}
	invocation := autoShutdownLastInvocation(t, record)
	wantReason := "autoshutdown: office idle for 17m (no active agents, no CEO activity)"
	if len(invocation.Args) != 3 || invocation.Args[2] != wantReason {
		t.Fatalf("safe-shutdown reason = %#v, want %q", invocation.Args, wantReason)
	}
	if invocation.PluginName != "" || invocation.PluginEvent != "" {
		t.Fatalf("plugin markers were not cleared: %#v", invocation)
	}
}

type autoShutdownInvocation struct {
	Args        []string `json:"args"`
	PluginName  string   `json:"plugin_name"`
	PluginEvent string   `json:"plugin_event"`
}

func newAutoShutdownManager(t *testing.T, config map[string]any) (*Manager, *sql.DB, string) {
	t.Helper()
	return newAutoShutdownManagerInOffice(t, t.TempDir(), config)
}

func newAutoShutdownManagerInOffice(t *testing.T, office string, config map[string]any) (*Manager, *sql.DB, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(office, "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	pluginDir := filepath.Join(office, Dir, "autoshutdown")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyAutoShutdownFixture(t, pluginDir)
	record := filepath.Join(office, "autoshutdown-invocations.jsonl")
	stub := buildAutoShutdownOMO(t)
	t.Setenv("OMO_TEST_RECORD", record)
	t.Setenv("PATH", filepath.Dir(stub)+string(os.PathListSeparator)+os.Getenv("PATH"))
	settings := map[string]Settings{"autoshutdown": {Enabled: true, Config: config}}
	manager, err := LoadConfigured(office, database, settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close() })
	return manager, database, record
}

func copyAutoShutdownFixture(t *testing.T, destination string) {
	t.Helper()
	source := autoShutdownSourceDir(t)
	for _, name := range []string{"plugin.json", "autoshutdown.lua"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destination, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func autoShutdownSourceDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locating test source")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "plugins", "autoshutdown")
}

func autoShutdownEvent(now, officeStarted int64, shuttingDown bool) Event {
	return autoShutdownEventWithAgents(now, officeStarted, shuttingDown, nil)
}

func autoShutdownEventWithAgents(now, officeStarted int64, shuttingDown bool, agents map[string]string) Event {
	entries := make([]any, 0, len(agents))
	for name, role := range agents {
		entries = append(entries, map[string]any{"name": name, "role": role})
	}
	return Event{Name: EventCron, Data: map[string]any{
		"at_unix": now, "office_started_at_unix": officeStarted,
		"shutdown_in_progress": shuttingDown, "agents": entries,
	}}
}

func autoShutdownEventWithCEOActivity(now, officeStarted, activity int64) Event {
	event := autoShutdownEvent(now, officeStarted, false)
	if activity != 0 {
		event.Data["ceo_activity_at_unix"] = activity
	}
	return event
}

func autoShutdownStorage(t *testing.T, database *sql.DB, key string) string {
	t.Helper()
	var value string
	if err := database.QueryRow("SELECT value FROM plugin_storage WHERE scope='local' AND plugin='autoshutdown' AND key=?", key).Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return ""
		}
		t.Fatal(err)
	}
	return strings.Trim(value, "\"")
}

func autoShutdownInvocations(t *testing.T, record string) int {
	t.Helper()
	file, err := os.Open(record)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return count
}

func autoShutdownLastInvocation(t *testing.T, record string) autoShutdownInvocation {
	t.Helper()
	file, err := os.Open(record)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var last autoShutdownInvocation
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := json.Unmarshal(scanner.Bytes(), &last); err != nil {
			t.Fatal(err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return last
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func buildAutoShutdownOMO(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	program := `package main
import (
  "encoding/json"
  "os"
)
func main() {
  invocation := map[string]any{
    "args": os.Args[1:],
    "plugin_name": os.Getenv("OMO_PLUGIN_NAME"),
    "plugin_event": os.Getenv("OMO_PLUGIN_EVENT"),
  }
  file, err := os.OpenFile(os.Getenv("OMO_TEST_RECORD"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
  if err != nil { panic(err) }
  defer file.Close()
  if err := json.NewEncoder(file).Encode(invocation); err != nil { panic(err) }
  if os.Getenv("OMO_TEST_FAIL") != "" { os.Exit(1) }
}
`
	if err := os.WriteFile(source, []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "omo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(dir, name)
	if output, err := exec.Command("go", "build", "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("build fake omo: %v\n%s", err, output)
	}
	return binary
}
