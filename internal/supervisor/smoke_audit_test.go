package supervisor

import (
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestSmokeWritePhrasesCaseInsensitive(t *testing.T) {
	for _, phrase := range []string{"created repository", "pushed", "committed", "merged", "initialized repo", "published", "moved", "deleted", "wrote", "fixed", "implemented", "ran the tests", "running tests"} {
		t.Run(phrase, func(t *testing.T) {
			if !smokeWritePhrase("I " + strings.ToUpper(phrase) + " today") {
				t.Fatalf("missed %q", phrase)
			}
		})
	}
	for _, text := range []string{"checked repository status", "tests are running elsewhere", "found a problem"} {
		if smokeWritePhrase(text) {
			t.Fatalf("false positive for %q", text)
		}
	}
}

func TestSmokeStepViolationPersistsAndStops(t *testing.T) {
	o := newOffice(t, map[string]string{"smokealarm": "ready\nsleep|60s\n"})
	o.Sup.Cfg.SmokeAlarm.Interval = config.Duration(time.Minute)
	name, err := o.Sup.Spawn("smokealarm", "smokealarm", 0, o.Dir, "inspect")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "smoke working", func() bool { return agentState(t, o, name) == "working" })
	const text = "I FiXeD the repository"
	_ = sockc.Call(o.Sup.SocketPath, name, "step", proto.StepArgs{Description: text}, nil)
	a, err := db.GetAgent(o.DB, name)
	if err != nil {
		t.Fatal(err)
	}
	if a.Step != text || a.State != "dead" {
		t.Fatalf("step=%q state=%q", a.Step, a.State)
	}
	assertSmokeViolation(t, o, name, text)
	assertSmokePublished(t, o, name, "agent_step", text)
	if next := o.Sup.runSmokeRound(); len(next) != 0 {
		t.Fatalf("immediate replacement: %v", next)
	}
	if next := o.Sup.restartTimedOutSmokeRound([]string{name}); len(next) != 0 {
		t.Fatalf("timeout restarted violating round: %v", next)
	}
}

func TestSmokeDoneViolationPersistsAndStops(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.Cfg.SmokeAlarm.Interval = config.Duration(time.Minute)
	const name = "smoke-done-audit"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "smokealarm", Profile: "smokealarm"}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAgentState(o.DB, name, "working"); err != nil {
		t.Fatal(err)
	}
	const text = "I RAN THE TESTS"
	if err := o.Sup.done(name, text); err != nil {
		t.Fatal(err)
	}
	a, err := db.GetAgent(o.DB, name)
	if err != nil {
		t.Fatal(err)
	}
	if a.State != "dead" {
		t.Fatalf("state = %q", a.State)
	}
	assertSmokeViolation(t, o, name, text)
	assertSmokePublished(t, o, name, "agent_done", text)
	if next := o.Sup.runSmokeRound(); len(next) != 0 {
		t.Fatalf("immediate replacement: %v", next)
	}
	if _, err := o.Sup.Spawn("smokealarm", "smokealarm", 0, o.Dir, "too soon"); err != ErrSpawningHalted {
		t.Fatalf("direct smoke spawn during cooldown = %v", err)
	}
}

func assertSmokePublished(t *testing.T, o *office, name, kind, text string) {
	t.Helper()
	var count int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND agent=? AND detail=?`, kind, name, text).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("%s event count = %d", kind, count)
	}
}

func assertSmokeViolation(t *testing.T, o *office, name, text string) {
	t.Helper()
	var count int
	if err := o.DB.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='smokealarm_violation' AND agent=? AND detail=?`, name, text).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("violation event count = %d", count)
	}
	inbox, err := o.Sup.Mail.Inbox("user")
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 1 || inbox[0].From != bus.SystemSender || inbox[0].Priority != bus.PrioHigh || !strings.Contains(inbox[0].Body, text) {
		t.Fatalf("user inbox = %+v", inbox)
	}
}
