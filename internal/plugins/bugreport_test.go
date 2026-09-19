package plugins

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

func bugreportSourceDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", "plugins", "bugreport")
}

func loadBugreport(t *testing.T, config map[string]any) (*Manager, func()) {
	t.Helper()
	office, database := newPluginOffice(t)
	source := bugreportSourceDir(t)
	target := filepath.Join(office, Dir, "bugreport")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plugin.json", "report.lua", "notice.lua", "prompt.lua"} {
		raw, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := LoadConfigured(office, database, map[string]Settings{
		"bugreport": {Enabled: true, Config: config},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager, func() {
		_ = manager.Close()
		_ = database.Close()
	}
}

func bugreportManualHook(t *testing.T, manager *Manager, action string) loadedHook {
	t.Helper()
	for _, hook := range manager.hooks {
		if hook.plugin == "bugreport" && hook.hook.Event == EventManual && hook.hook.Name == action {
			return hook
		}
	}
	t.Fatalf("bugreport %s hook not loaded", action)
	return loadedHook{}
}

func runBugreportManual(t *testing.T, manager *Manager, action string, data map[string]any) error {
	t.Helper()
	hook := bugreportManualHook(t, manager, action)
	_, err := manager.runHook(context.Background(), hook, Event{Name: EventManual, Data: data}, nil)
	return err
}

func bugreportBodyFile(t *testing.T, contents []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func bugreportValidBody() []byte {
	return []byte("## Summary\nA concise summary.\n\n## Observed behavior\nOmo behaved unexpectedly.\n\n## Expected behavior\nOmo should have behaved differently.\n\n## Steps or evidence\n1. Reproduced the issue.\n\n## Anonymization check\nNo project or customer data is included.\n")
}

func bugreportBodyWithSize(size int) []byte {
	body := bugreportValidBody()
	if len(body) > size {
		panic("bugreport fixture is larger than requested size")
	}
	return append(body, bytes.Repeat([]byte("x"), size-len(body))...)
}

func bugreportEvent(args ...string) map[string]any {
	return map[string]any{
		"plugin":      "bugreport",
		"action":      "report",
		"caller":      "developer-secret-name",
		"caller_role": "developer",
		"args":        args,
		"request_id":  int64(1),
		"home_path":   "/secret/office/path",
		"repo":        "secret/repository",
		"branch":      "secret/branch",
	}
}

func assertBugreportUsageGuidance(t *testing.T, err error, fault string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected bugreport usage error")
	}
	message := err.Error()
	for _, want := range []string{
		fault,
		`omo plugin trigger bugreport report -- body=<absolute-path> "<title>"`,
		"Required headings: ## Summary, ## Observed behavior, ## Expected behavior, ## Steps or evidence, ## Anonymization check",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("usage error %q does not contain %q", message, want)
		}
	}
}

func TestBugreportManifestIsRealOptionalPlugin(t *testing.T) {
	manifest, err := ReadManifest(bugreportSourceDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "bugreport" || manifest.Version != "1.0.0" {
		t.Fatalf("manifest identity = %q %q", manifest.Name, manifest.Version)
	}
	if manifest.DefaultConfig == nil {
		t.Fatal("manifest has no default config")
	}
	for key, want := range map[string]any{
		"mode": "github", "repository": "scolastico-dev/one-man-office", "local_dir": "", "fallback_local": true, "review_before_publish": false, "instruct": true,
	} {
		if got := manifest.DefaultConfig[key]; got != want {
			t.Fatalf("default config %q = %#v, want %#v", key, got, want)
		}
	}
	labels, ok := manifest.DefaultConfig["labels"].([]any)
	if !ok || len(labels) != 2 || labels[0] != "bug" || labels[1] != "omo-report" {
		t.Fatalf("default labels = %#v", manifest.DefaultConfig["labels"])
	}
	var report, notice []string
	for _, hook := range manifest.Hooks {
		if hook.Event != EventManual {
			continue
		}
		if hook.Name == "report" {
			report = hook.Roles
			if !hook.ManualArgs || hook.Lua != "report.lua" {
				t.Fatalf("report hook = %+v", hook)
			}
		}
		if hook.Name == "notice" {
			notice = hook.Roles
			if !hook.ManualArgs || hook.Lua != "notice.lua" {
				t.Fatalf("notice hook = %+v", hook)
			}
		}
	}
	if strings.Join(report, ",") != "user,ceo,product_manager,developer,reviewer,freelancer,firefighter" {
		t.Fatalf("report roles = %v", report)
	}
	if strings.Join(notice, ",") != "user" {
		t.Fatalf("notice roles = %v", notice)
	}
}

func TestBugreportReportUsageValidationPrecedesSideEffects(t *testing.T) {
	cases := []struct {
		name  string
		args  func(*testing.T) []string
		fault string
	}{
		{name: "missing title", args: func(t *testing.T) []string { return []string{"body=" + bugreportBodyFile(t, bugreportValidBody())} }, fault: "exactly one non-empty title is required"},
		{name: "empty title", args: func(t *testing.T) []string {
			return []string{"body=" + bugreportBodyFile(t, bugreportValidBody()), "   "}
		}, fault: "title must not be empty"},
		{name: "multiple titles", args: func(t *testing.T) []string {
			return []string{"body=" + bugreportBodyFile(t, bugreportValidBody()), "one", "two"}
		}, fault: "exactly one title argument is allowed"},
		{name: "missing body", args: func(*testing.T) []string { return []string{"A title"} }, fault: "body=<absolute-path> is required"},
		{name: "empty body", args: func(t *testing.T) []string { return []string{"body=", "A title"} }, fault: "body= must name an absolute Markdown file"},
		{name: "relative body", args: func(*testing.T) []string { return []string{"body=report.md", "A title"} }, fault: "body path must be absolute"},
		{name: "windows drive body", args: func(*testing.T) []string { return []string{`body=C:\\report.md`, "A title"} }, fault: "report body could not be read"},
		{name: "windows UNC body", args: func(*testing.T) []string { return []string{`body=\\server\share\report.md`, "A title"} }, fault: "report body could not be read"},
		{name: "missing file", args: func(t *testing.T) []string {
			return []string{"body=" + filepath.Join(t.TempDir(), "missing.md"), "A title"}
		}, fault: "report body could not be read"},
		{name: "unreadable path", args: func(t *testing.T) []string { return []string{"body=" + t.TempDir(), "A title"} }, fault: "report body could not be read"},
		{name: "invalid utf8", args: func(t *testing.T) []string {
			return []string{"body=" + bugreportBodyFile(t, []byte("## Summary\n\xff")), "A title"}
		}, fault: "report body is not valid UTF-8"},
		{name: "empty after trim", args: func(t *testing.T) []string {
			return []string{"body=" + bugreportBodyFile(t, []byte(" \t\r\n")), "A title"}
		}, fault: "report body is empty after trimming"},
		{name: "too large", args: func(t *testing.T) []string {
			return []string{"body=" + bugreportBodyFile(t, []byte(strings.Repeat("x", 61441))), "A title"}
		}, fault: "report body exceeds 61440 bytes"},
		{name: "missing headings", args: func(t *testing.T) []string {
			return []string{"body=" + bugreportBodyFile(t, []byte("## Summary\nsummary\n")), "A title"}
		}, fault: "missing required headings"},
		{name: "duplicate body", args: func(t *testing.T) []string {
			path := bugreportBodyFile(t, bugreportValidBody())
			return []string{"body=" + path, "body=" + path, "A title"}
		}, fault: "body= may be specified only once"},
		{name: "unknown key", args: func(t *testing.T) []string {
			return []string{"body=" + bugreportBodyFile(t, bugreportValidBody()), "unknown=value", "A title"}
		}, fault: "unknown key-like argument unknown=value"},
		{name: "malformed body key", args: func(*testing.T) []string { return []string{"body", "A title"} }, fault: "body must use body=<absolute-path>"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bin := t.TempDir()
			ghLog := filepath.Join(bin, "gh.log")
			gh := filepath.Join(bin, "gh")
			if err := os.WriteFile(gh, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+ghLog+"\"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			reportDir := filepath.Join(t.TempDir(), "bugreports")
			manager, cleanup := loadBugreport(t, map[string]any{"mode": "github", "local_dir": reportDir})
			defer cleanup()
			err := runBugreportManual(t, manager, "report", bugreportEvent(test.args(t)...))
			assertBugreportUsageGuidance(t, err, test.fault)
			if entries, readErr := os.ReadDir(reportDir); readErr == nil && len(entries) != 0 {
				t.Fatalf("invalid input wrote reports: %v", entries)
			} else if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if raw, readErr := os.ReadFile(ghLog); readErr == nil && len(raw) != 0 {
				t.Fatalf("invalid input invoked gh: %q", raw)
			}
		})
	}
}

func TestBugreportUsageGuidanceMailsOnlyNonUserCaller(t *testing.T) {
	for _, caller := range []string{"developer-secret-name", "user"} {
		t.Run(caller, func(t *testing.T) {
			commandLog := bugreportCommandStubs(t, "success", "")
			manager, cleanup := loadBugreport(t, map[string]any{"mode": "github"})
			defer cleanup()
			data := bugreportEvent("A title")
			data["caller"] = caller
			data["caller_role"] = map[string]string{"user": "user", "developer-secret-name": "developer"}[caller]
			err := runBugreportManual(t, manager, "report", data)
			assertBugreportUsageGuidance(t, err, "body=<absolute-path> is required")
			raw, readErr := os.ReadFile(commandLog)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			output := string(raw)
			if caller == "user" {
				if strings.Contains(output, "send -t user") {
					t.Fatalf("user received usage guidance mail: %q", output)
				}
			} else if !strings.Contains(output, "send -t "+caller) || !strings.Contains(output, "Required headings:") {
				t.Fatalf("agent usage guidance mail = %q", output)
			}
		})
	}
}

func bugreportCommandStubs(t *testing.T, ghMode string, ceoListing string) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(bin, "commands.log")
	omo := filepath.Join(bin, "omo")
	omoScript := `#!/bin/sh
printf 'omo %s\n' "$*" >> "$BUGREPORT_COMMAND_LOG"
if [ "$1" = "send" ] && [ "$3" = "$BUGREPORT_SEND_FAIL_TARGET" ]; then exit 1; fi
if [ "$1" = "--version" ]; then
  if [ "$BUGREPORT_OMO_VERSION_FAILURE" = "1" ]; then exit 1; fi
  printf 'omo test-version\n'
  exit 0
fi
if [ "$1" = "agent" ] && [ "$2" = "list" ]; then printf '%s' "$BUGREPORT_CEO_LIST"; exit 0; fi
exit 0
`
	if err := os.WriteFile(omo, []byte(omoScript), 0o755); err != nil {
		t.Fatal(err)
	}
	gh := filepath.Join(bin, "gh")
	ghScript := `#!/bin/sh
printf 'gh %s\n' "$*" >> "$BUGREPORT_COMMAND_LOG"
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  if [ "$BUGREPORT_GH_MODE" = "auth-fail" ]; then exit 1; fi
  exit 0
fi
if [ "$1" = "issue" ] && [ "$2" = "create" ]; then
  if [ "$BUGREPORT_GH_MODE" = "create-fail" ]; then exit 1; fi
  previous=""
  for arg in "$@"; do
    if [ "$previous" = "--body-file" ]; then
      printf 'gh-body-file=%s\n' "$arg" >> "$BUGREPORT_COMMAND_LOG"
      cat "$arg" >> "$BUGREPORT_COMMAND_LOG"
      printf '\n' >> "$BUGREPORT_COMMAND_LOG"
    fi
    previous="$arg"
  done
  printf 'https://github.example/issues/104\n'
  exit 0
fi
exit 1
`
	if err := os.WriteFile(gh, []byte(ghScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BUGREPORT_COMMAND_LOG", logPath)
	t.Setenv("BUGREPORT_GH_MODE", ghMode)
	t.Setenv("BUGREPORT_CEO_LIST", ceoListing)
	t.Setenv("BUGREPORT_SEND_FAIL_TARGET", "")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func bugreportReportArgs(t *testing.T, title string, body []byte) []string {
	t.Helper()
	return []string{"body=" + bugreportBodyFile(t, body), title}
}

func bugreportOutputText(t *testing.T, manager *Manager) string {
	t.Helper()
	events, err := db.AllEvents(manager.DB)
	if err != nil {
		t.Fatal(err)
	}
	var parts []string
	for _, event := range events {
		parts = append(parts, event.Detail)
	}
	logs, err := db.PluginLogs(manager.DB, "bugreport")
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range logs {
		parts = append(parts, log.Message)
	}
	return strings.Join(parts, "\n")
}

func TestBugreportGitHubCreationUsesFinishedBodyAndLabels(t *testing.T) {
	commandLog := bugreportCommandStubs(t, "success", "")
	manager, cleanup := loadBugreport(t, map[string]any{
		"mode": "github", "repository": "acme/omo", "labels": []any{"bug", "omo-report"},
	})
	defer cleanup()
	data := bugreportEvent(bugreportReportArgs(t, "Wrong routing", bugreportValidBody())...)
	data["caller_role"] = "user"
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "bugreport", "report", "user", "user", data["args"].([]string), data)
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "issue: https://github.example/issues/104 (created)" {
		t.Fatalf("result = %#v", result.Value)
	}
	raw, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(raw)
	if !strings.Contains(commands, "gh auth status") || !strings.Contains(commands, "gh issue create -R acme/omo") || !strings.Contains(commands, "--body-file ") || strings.Contains(commands, "--body ##") || !strings.Contains(commands, "--label bug") || !strings.Contains(commands, "--label omo-report") {
		t.Fatalf("GitHub commands = %q", commands)
	}
	if !strings.Contains(commands, "## Environment") || !strings.Contains(commands, "caller_role: user") || !strings.Contains(commands, "omo test-version") {
		t.Fatalf("finished body missing environment = %q", commands)
	}
	for _, sentinel := range []string{"developer-secret-name", "secret/office/path", "secret/repository", "secret/branch"} {
		if strings.Contains(bugreportOutputText(t, manager), sentinel) {
			t.Fatalf("privacy sentinel %q leaked into durable output", sentinel)
		}
	}
}

func TestBugreportVersionFailureUsesUnknownAndContinues(t *testing.T) {
	bugreportCommandStubs(t, "success", "")
	t.Setenv("BUGREPORT_OMO_VERSION_FAILURE", "1")
	directory := t.TempDir()
	manager, cleanup := loadBugreport(t, map[string]any{"mode": "local", "local_dir": directory})
	defer cleanup()
	args := bugreportReportArgs(t, "Unknown version", bugreportValidBody())
	data := bugreportEvent(args...)
	data["caller_role"] = "user"
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "bugreport", "report", "user", "user", args, data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(result.Value.(string), "file: ") {
		t.Fatalf("result = %#v", result.Value)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("report entries = %v, err=%v", entries, err)
	}
	report, err := os.ReadFile(filepath.Join(directory, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(report), "- omo version: unknown") {
		t.Fatalf("report missing unknown version = %q", report)
	}
	if got := strings.Count(bugreportOutputText(t, manager), "omo version lookup failed; using unknown"); got != 1 {
		t.Fatalf("version lookup log count = %d, want 1", got)
	}
}

func TestBugreportGitHubCreationUsesBodyFileForMaximumBody(t *testing.T) {
	commandLog := bugreportCommandStubs(t, "success", "")
	manager, cleanup := loadBugreport(t, map[string]any{
		"mode": "github", "repository": "acme/omo", "labels": []any{"bug"},
	})
	defer cleanup()
	body := bugreportBodyWithSize(61440)
	args := bugreportReportArgs(t, "Maximum body", body)
	data := bugreportEvent(args...)
	data["caller_role"] = "user"
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "bugreport", "report", "user", "user", args, data)
	if err != nil || result.Value != "issue: https://github.example/issues/104 (created)" {
		t.Fatalf("maximum body result = %#v, err=%v", result.Value, err)
	}
	raw, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(raw)
	if !strings.Contains(commands, "--body-file ") || !strings.Contains(commands, "## Environment") || !strings.Contains(commands, strings.Repeat("x", 128)) {
		t.Fatalf("maximum body was not transported through a file: %q", commands)
	}
}

func TestBugreportMandatoryNotificationFailuresPreserveLocalArtifact(t *testing.T) {
	for _, recipient := range []string{"user", "ceo"} {
		t.Run(recipient, func(t *testing.T) {
			bugreportCommandStubs(t, "success", "")
			t.Setenv("BUGREPORT_SEND_FAIL_TARGET", recipient)
			directory := t.TempDir()
			manager, cleanup := loadBugreport(t, map[string]any{"mode": "local", "local_dir": directory})
			defer cleanup()
			args := bugreportReportArgs(t, "Notification failure", bugreportValidBody())
			data := bugreportEvent(args...)
			data["caller_role"] = "user"
			_, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "bugreport", "report", "user", "user", args, data)
			if err == nil || !strings.Contains(err.Error(), "mandatory notification failed") || !strings.Contains(err.Error(), recipient) {
				t.Fatalf("notification error = %v", err)
			}
			entries, readErr := os.ReadDir(directory)
			if readErr != nil || len(entries) != 1 {
				t.Fatalf("artifact entries = %v, err=%v", entries, readErr)
			}
			if output := bugreportOutputText(t, manager); !strings.Contains(output, "file: ") || !strings.Contains(output, "mandatory notification failed") {
				t.Fatalf("artifact/result facts missing from durable output: %q", output)
			}
		})
	}
}

func TestBugreportFallbackAndDisabledFallback(t *testing.T) {
	for _, test := range []struct {
		name      string
		ghMode    string
		fallback  bool
		wantError string
	}{
		{name: "auth fallback", ghMode: "auth-fail", fallback: true},
		{name: "create fallback", ghMode: "create-fail", fallback: true},
		{name: "auth disabled", ghMode: "auth-fail", fallback: false, wantError: "GitHub authentication failed and local fallback is disabled"},
		{name: "create disabled", ghMode: "create-fail", fallback: false, wantError: "GitHub issue creation failed and local fallback is disabled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			commandLog := bugreportCommandStubs(t, test.ghMode, "")
			directory := t.TempDir()
			manager, cleanup := loadBugreport(t, map[string]any{
				"mode": "github", "repository": "acme/omo", "local_dir": directory, "fallback_local": test.fallback,
			})
			defer cleanup()
			args := bugreportReportArgs(t, "Fallback title", bugreportValidBody())
			data := bugreportEvent(args...)
			result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "bugreport", "report", "user", "user", args, data)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				entries, readErr := os.ReadDir(directory)
				if readErr != nil && !os.IsNotExist(readErr) {
					t.Fatal(readErr)
				}
				if len(entries) != 0 {
					t.Fatalf("disabled fallback wrote files: %v", entries)
				}
				return
			}
			line, ok := result.Value.(string)
			if err != nil || !ok || !strings.HasPrefix(line, "file: ") {
				t.Fatalf("fallback result = %#v, err=%v", result.Value, err)
			}
			if !strings.Contains(bugreportOutputText(t, manager), "local fallback") {
				t.Fatal("fallback context missing from plugin log")
			}
			raw, readErr := os.ReadFile(commandLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !strings.Contains(string(raw), line) || !strings.Contains(string(raw), "bugreport report (local fallback)") {
				t.Fatalf("fallback result was not notified: %q", raw)
			}
		})
	}
}

func TestBugreportLocalModeEnvironmentSlugAndNoOverwrite(t *testing.T) {
	commandLog := bugreportCommandStubs(t, "success", "")
	directory := t.TempDir()
	manager, cleanup := loadBugreport(t, map[string]any{"mode": "local", "local_dir": directory})
	defer cleanup()
	args := bugreportReportArgs(t, "../../ Unsafe title!?", bugreportValidBody())
	data := bugreportEvent(args...)
	data["caller_role"] = "user"
	var line string
	for attempt := 0; attempt < 20; attempt++ {
		stamp := time.Now().Format("20060102-150405")
		candidate := filepath.Join(directory, stamp+"-unsafe-title.md")
		if err := os.WriteFile(candidate, []byte("pre-existing collision\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "bugreport", "report", "user", "user", args, data)
		if err != nil {
			t.Fatal(err)
		}
		line = result.Value.(string)
		path := strings.TrimPrefix(line, "file: ")
		if strings.HasSuffix(filepath.Base(path), "-2.md") {
			break
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.HasPrefix(line, "file: ") || !strings.HasSuffix(filepath.Base(strings.TrimPrefix(line, "file: ")), "-2.md") {
		t.Fatalf("collision result did not use deterministic suffix: %q", line)
	}
	path := strings.TrimPrefix(line, "file: ")
	if filepath.Dir(path) != directory || strings.Contains(filepath.Base(path), "/") || strings.Contains(filepath.Base(path), "..") {
		t.Fatalf("unsafe report path = %q", path)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Contains(raw, []byte("## Environment")) || !bytes.Contains(raw, []byte("- mode: local")) || !bytes.Contains(raw, []byte("- caller_role: user")) || !bytes.Contains(raw, []byte("- plugin: bugreport")) {
		t.Fatalf("environment missing from %q: %q", path, raw)
	}
	if raw, err := os.ReadFile(commandLog); err != nil || !strings.Contains(string(raw), line) {
		t.Fatalf("local notifications = %q, err=%v", raw, err)
	}
}

func TestBugreportDefaultHomeRejectsRelativeEnvironmentValues(t *testing.T) {
	cases := []struct {
		name, env, value, want string
	}{
		{name: "OMO_HOME", env: "OMO_HOME", value: "relative/home", want: "OMO_HOME must be an absolute path"},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, struct {
			name, env, value, want string
		}{name: "HOME", env: "HOME", value: "relative/home", want: "HOME must be an absolute path"})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			bugreportCommandStubs(t, "success", "")
			t.Setenv("OMO_HOME", "")
			t.Setenv("HOME", "")
			t.Setenv(test.env, test.value)
			directory := filepath.Join(t.TempDir(), "reports")
			manager, cleanup := loadBugreport(t, map[string]any{"mode": "local", "local_dir": ""})
			defer cleanup()
			args := bugreportReportArgs(t, "Invalid default home", bugreportValidBody())
			_, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "bugreport", "report", "user", "user", args, bugreportEvent(args...))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if _, statErr := os.Stat(directory); !os.IsNotExist(statErr) {
				t.Fatalf("unexpected report directory state: %v", statErr)
			}
		})
	}
}

func TestBugreportEmptyLocalDirUsesOmoHomeBugreports(t *testing.T) {
	bugreportCommandStubs(t, "success", "")
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	manager, cleanup := loadBugreport(t, map[string]any{"mode": "local", "local_dir": ""})
	defer cleanup()
	args := bugreportReportArgs(t, "Default path", bugreportValidBody())
	data := bugreportEvent(args...)
	data["caller_role"] = "user"
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "bugreport", "report", "user", "user", args, data)
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimPrefix(result.Value.(string), "file: ")
	if filepath.Dir(path) != filepath.Join(home, "bugreports") {
		t.Fatalf("default report directory = %q", filepath.Dir(path))
	}
	if _, err := os.Stat(filepath.Join(home, "bugreports")); err != nil {
		t.Fatal(err)
	}
}

func TestBugreportReviewBeforePublishAllowsUserAndCEOOnly(t *testing.T) {
	for _, role := range []string{"developer", "user", "ceo"} {
		t.Run(role, func(t *testing.T) {
			commandLog := bugreportCommandStubs(t, "success", "")
			directory := t.TempDir()
			manager, cleanup := loadBugreport(t, map[string]any{"mode": "github", "repository": "acme/omo", "local_dir": directory, "review_before_publish": true})
			defer cleanup()
			args := bugreportReportArgs(t, "Review me", bugreportValidBody())
			data := bugreportEvent(args...)
			data["caller_role"] = role
			result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "bugreport", "report", role, role, args, data)
			if err != nil {
				t.Fatal(err)
			}
			line := result.Value.(string)
			if role == "developer" {
				if !strings.HasPrefix(line, "file: ") {
					t.Fatalf("agent result = %q", line)
				}
				raw, _ := os.ReadFile(commandLog)
				if strings.Contains(string(raw), "gh auth status") || strings.Contains(string(raw), "gh issue create") {
					t.Fatalf("agent invoked GitHub: %q", raw)
				}
			} else if line != "issue: https://github.example/issues/104 (created)" {
				t.Fatalf("%s result = %q", role, line)
			}
		})
	}
}

func TestBugreportPromptRolesConfigAndIdempotence(t *testing.T) {
	manager, cleanup := loadBugreport(t, nil)
	defer cleanup()
	for _, role := range []string{"user", "ceo", "product_manager", "developer", "reviewer", "freelancer", "firefighter"} {
		t.Run(role, func(t *testing.T) {
			data := map[string]any{"role": role, "text": "base"}
			first, err := manager.Emit(context.Background(), Event{Name: EventPromptRender, Mutable: true, Data: data})
			if err != nil {
				t.Fatal(err)
			}
			text := first.Data["text"].(string)
			for _, want := range []string{"bugreport-instructions-v1", "wrong routing", "stuck lifecycle", "bad prompt", "crash", "CLI error", "not for project bugs", "anonymized", "## Summary", "## Observed behavior", "## Expected behavior", "## Steps or evidence", "## Anonymization check", `omo plugin trigger bugreport report -- body=<absolute-path> "<title>"`} {
				if !strings.Contains(text, want) {
					t.Errorf("prompt missing %q: %q", want, text)
				}
			}
			if len(text)-len("base") > 2048 || strings.Count(text, "bugreport-instructions-v1") != 1 {
				t.Fatalf("prompt growth/marker = %d/%d", len(text)-len("base"), strings.Count(text, "bugreport-instructions-v1"))
			}
			data["text"] = text
			second, err := manager.Emit(context.Background(), Event{Name: EventPromptRender, Mutable: true, Data: data})
			if err != nil || second.Data["text"] != text {
				t.Fatalf("prompt not idempotent: %q / %v", second.Data["text"], err)
			}
		})
	}
	for _, role := range []string{"smokealarm", "branch_namer", "unknown"} {
		data := map[string]any{"role": role, "text": "base"}
		result, err := manager.Emit(context.Background(), Event{Name: EventPromptRender, Mutable: true, Data: data})
		if err != nil || result.Data["text"] != "base" {
			t.Fatalf("excluded role %s prompt = %#v, err=%v", role, result.Data["text"], err)
		}
	}
	disabled, cleanup := loadBugreport(t, map[string]any{"instruct": false})
	defer cleanup()
	result, err := disabled.Emit(context.Background(), Event{Name: EventPromptRender, Mutable: true, Data: map[string]any{"role": "developer", "text": "base"}})
	if err != nil || result.Data["text"] != "base" {
		t.Fatalf("disabled prompt = %#v, err=%v", result.Data["text"], err)
	}
}

func TestBugreportNoticeUsesOneLiteralTypeArgAndFindsCEO(t *testing.T) {
	commandLog := bugreportCommandStubs(t, "success", "ceo-secret-name             ceo              working   job=0    listening\n")
	manager, cleanup := loadBugreport(t, nil)
	defer cleanup()
	message := "line one\nline two ; $(touch SHOULD_NOT_RUN) `quoted`"
	result, err := manager.TriggerManualContextWithRoleResult(context.Background(), "bugreport", "notice", "user", "user", []string{message})
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "notice: typed to ceo-secret-name" {
		t.Fatalf("notice result = %#v", result.Value)
	}
	raw, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "omo agent list") || !strings.Contains(string(raw), "omo type ceo-secret-name ") || !strings.Contains(string(raw), "--key enter") || !strings.Contains(string(raw), message) {
		t.Fatalf("notice commands = %q", raw)
	}
}

func TestBugreportNoticeRejectsBlankOversizedAndNoCEOWithoutTyping(t *testing.T) {
	for _, test := range []struct {
		name    string
		message string
		listing string
		want    string
	}{
		{name: "blank", message: " \n\t", listing: "ceo-ada ceo working job=0\n", want: "notice message must not be empty"},
		{name: "oversized", message: strings.Repeat("x", 65536), listing: "ceo-ada ceo working job=0\n", want: "notice message is too large"},
		{name: "no CEO", message: "please investigate", listing: "developer-ada developer working job=7\n", want: "no living CEO is available"},
	} {
		t.Run(test.name, func(t *testing.T) {
			commandLog := bugreportCommandStubs(t, "success", test.listing)
			manager, cleanup := loadBugreport(t, nil)
			defer cleanup()
			_, err := manager.TriggerManualContextWithRoleResult(context.Background(), "bugreport", "notice", "user", "user", []string{test.message})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			raw, readErr := os.ReadFile(commandLog)
			if readErr == nil && strings.Contains(string(raw), "omo type") {
				t.Fatalf("invalid notice typed to CEO: %q", raw)
			}
		})
	}
}

func TestBugreportNoticeSizeBoundary(t *testing.T) {
	const prefix = "[bugreport] The user reports a possible problem with omo itself: "
	const suffix = ". Investigate with `omo job list`, `omo logs`, events, and your own transcript. Then write a detailed report in storage that describes ONLY omo's behaviour: no project names, paths, repository or branch names, customer data, secrets, or mail contents. Use the headings ## Summary, ## Observed behavior, ## Expected behavior, ## Steps or evidence, and ## Anonymization check, and run `omo plugin trigger bugreport report -- body=<absolute path> \"<title>\"`. Reply to the user with the result line."
	for _, test := range []struct {
		name, message, want string
	}{
		{name: "one byte below limit", message: strings.Repeat("x", 65535-len(prefix)-len(suffix)), want: "typed"},
		{name: "at limit", message: strings.Repeat("x", 65536-len(prefix)-len(suffix)), want: "notice message is too large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			commandLog := bugreportCommandStubs(t, "success", "ceo-ada ceo working job=0\n")
			manager, cleanup := loadBugreport(t, nil)
			defer cleanup()
			result, err := manager.TriggerManualContextWithRoleResult(context.Background(), "bugreport", "notice", "user", "user", []string{test.message})
			if test.want == "typed" {
				if err != nil || result.Value != "notice: typed to ceo-ada" {
					t.Fatalf("boundary result = %#v, err=%v", result.Value, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			raw, readErr := os.ReadFile(commandLog)
			if readErr == nil && strings.Contains(string(raw), "omo type") {
				t.Fatalf("at-limit notice typed to CEO")
			}
		})
	}
}
