package supervisor

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestFreezeHaltsSpawningAndUnfreezeWakesWithGlobalMail(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":        "ready\nsleep|60s\n",
		"freelancer": "ready\nsleep|60s\n",
	})
	ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	if err != nil {
		t.Fatal(err)
	}
	worker, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "work")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "agents ready", func() bool {
		return agentState(t, o, ceo) == "working" && agentState(t, o, worker) == "working"
	})

	if err := sockc.Call(o.Sup.SocketPath, "user", "office.freeze", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !o.Sup.Frozen() {
		t.Fatal("office did not enter frozen state")
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "blocked"); !errors.Is(err, ErrSpawningHalted) {
		t.Fatalf("spawn while frozen = %v, want ErrSpawningHalted", err)
	}
	if got, _ := o.Sup.Mail.Inbox(worker); len(got) == 0 || got[0].Subject != freezeSubject {
		t.Fatalf("freeze mail = %#v", got)
	}

	if err := sockc.Call(o.Sup.SocketPath, "user", "office.unfreeze", nil, nil); err != nil {
		t.Fatal(err)
	}
	if o.Sup.Frozen() {
		t.Fatal("office remained frozen")
	}
	if _, err := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "allowed"); err != nil {
		t.Fatalf("spawn after unfreeze: %v", err)
	}
	messages, err := o.Sup.Mail.All(worker)
	if err != nil {
		t.Fatal(err)
	}
	foundWake := false
	for _, message := range messages {
		if message.From == bus.SystemSender && message.Subject == wakeSubject && strings.Contains(message.Body, "unfrozen") {
			foundWake = true
		}
	}
	if !foundWake {
		t.Fatalf("global wake-up mail missing: %#v", messages)
	}
	events, err := db.EventsSince(o.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("freeze events missing")
	}
}
