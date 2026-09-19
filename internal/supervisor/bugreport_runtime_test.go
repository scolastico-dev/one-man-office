package supervisor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func installBugreportRuntime(t *testing.T, o *office, config map[string]any) *plugins.Manager {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	source := filepath.Join(filepath.Dir(sourceFile), "..", "..", "plugins", "bugreport")
	target := filepath.Join(o.Dir, plugins.Dir, "bugreport")
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
	manager, err := plugins.LoadConfigured(o.Dir, o.DB, map[string]plugins.Settings{
		"bugreport": {Enabled: true, Config: config},
	})
	if err != nil {
		t.Fatal(err)
	}
	o.Sup.Plugins = manager
	t.Cleanup(func() { _ = manager.Close() })
	if err := os.WriteFile(filepath.Join(o.Dir, ".omo", "omo.lock"), []byte(o.Sup.SocketPath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(o.Dir, ".omo", "omo.lock")) })
	return manager
}

func TestBugreportRuntimeSocketPermittedResultSystemMailAndSmokealarmDenial(t *testing.T) {
	o := newOffice(t, nil)
	ceo := "ceo-bugreport"
	if err := db.InsertAgent(o.DB, db.Agent{Name: ceo, Role: "ceo", Profile: "ceo"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, ceo, "working"); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertAgent(o.DB, db.Agent{Name: "smokealarm-bugreport", Role: "smokealarm", Profile: "smokealarm"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, "smokealarm-bugreport", "working"); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	installBugreportRuntime(t, o, map[string]any{"mode": "local", "local_dir": directory})
	body := filepath.Join(t.TempDir(), "report.md")
	if err := os.WriteFile(body, []byte("## Summary\nSummary.\n\n## Observed behavior\nObserved.\n\n## Expected behavior\nExpected.\n\n## Steps or evidence\nSteps.\n\n## Anonymization check\nChecked.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var response proto.PluginTriggerResponse
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", proto.PluginTriggerArgs{Name: "bugreport", Action: "report", Args: []string{"body=" + body, "Runtime report"}}, &response); err != nil {
		t.Fatal(err)
	}
	line, ok := response.Result.(string)
	if !ok || !strings.HasPrefix(line, "file: ") {
		t.Fatalf("runtime result = %#v", response.Result)
	}
	for _, recipient := range []string{"user", ceo} {
		mail, err := o.Sup.Mail.Inbox(recipient)
		if err != nil || len(mail) != 1 || mail[0].Body != line || mail[0].From != bus.SystemSender {
			t.Fatalf("runtime mail for %s = %#v, err=%v", recipient, mail, err)
		}
	}
	if err := sockc.Call(o.Sup.SocketPath, "smokealarm-bugreport", "plugin.trigger", proto.PluginTriggerArgs{Name: "bugreport", Action: "report", Args: []string{"body=" + body, "Denied report"}}, &response); err == nil || !strings.Contains(err.Error(), `role "smokealarm" may not trigger plugin`) {
		t.Fatalf("smokealarm trigger error = %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("smokealarm side effects created %d reports", len(entries))
	}
}

func TestBugreportRuntimeNoticeTypesLiveCEOAndFailsWithoutCEO(t *testing.T) {
	o := newOffice(t, map[string]string{"ceo": "ready\nsleep|10s\n"})
	installBugreportRuntime(t, o, nil)
	bin := t.TempDir()
	logPath := filepath.Join(bin, "omo-wrapper.log")
	wrapper := filepath.Join(bin, "omo")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"" + logPath + "\"\nexec \"" + omoBin + "\" \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	name, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "bug report")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "CEO working", func() bool { return agentState(t, o, name) == "working" })
	message := "first line\nsecond line ; $(not-shell) `literal`"
	var response proto.PluginTriggerResponse
	if err := sockc.Call(o.Sup.SocketPath, "user", "plugin.trigger", proto.PluginTriggerArgs{Name: "bugreport", Action: "notice", Args: []string{message}}, &response); err != nil {
		t.Fatal(err)
	}
	if response.Result != "notice: typed to "+name {
		t.Fatalf("notice result = %#v", response.Result)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "agent list") || !strings.Contains(string(raw), "type "+name) || !strings.Contains(string(raw), message) || !strings.Contains(string(raw), "--key enter") {
		t.Fatalf("notice wrapper log = %q", raw)
	}
	if err := o.Sup.KillAgent(name, true); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "CEO dead", func() bool { return agentState(t, o, name) == "dead" })
	if _, err := o.Sup.TriggerPluginResult("user", "bugreport", "notice", []string{"no CEO now"}); err == nil || !strings.Contains(err.Error(), "no living CEO is available") {
		t.Fatalf("no-CEO notice error = %v", err)
	}
	after, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(after), "type "+name) != 1 {
		t.Fatalf("no-CEO notice typed unexpectedly: %q", after)
	}
}
