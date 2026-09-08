package supervisor

import (
	"errors"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func TestPluginFreezeHaltsEverySpawnUntilManagementResumes(t *testing.T) {
	o := newOffice(t, map[string]string{
		"ceo":         "ready\nsleep|60s\n",
		"firefighter": "ready\nsleep|60s\n",
		"freelancer":  "ready\nsleep|60s\n",
		"smokealarm":  "ready\nsleep|60s\n",
	})
	ceo, err := o.Sup.Spawn("ceo", "ceo", 0, o.Dir, "run office")
	if err != nil {
		t.Fatal(err)
	}
	firefighter, err := o.Sup.Spawn("firefighter", "firefighter", 0, o.Dir, "monitor")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "agents ready", func() bool {
		return agentState(t, o, ceo) == "working" && agentState(t, o, firefighter) == "working"
	})

	if err := sockc.Call(o.Sup.SocketPath, bus.SystemSender, "office.freeze", nil, nil); err != nil {
		t.Fatal(err)
	}
	if !o.Sup.Frozen() {
		t.Fatal("office did not enter frozen state")
	}
	if err := o.Sup.BeginFreeze(bus.SystemSender); err != nil {
		t.Fatalf("retrying an interrupted freeze action: %v", err)
	}
	for _, role := range config.AllRoles {
		if o.Sup.spawnAllowed(role) {
			t.Errorf("%s spawn remained allowed while frozen", role)
		}
	}
	if messages, err := o.Sup.Mail.History(); err != nil {
		t.Fatal(err)
	} else if len(messages) != 0 {
		t.Fatalf("freeze verb sent mail instead of leaving the broadcast to the Lua action: %+v", messages)
	}
	if _, err := o.Sup.spawnAttempt("freelancer", "freelancer", 0, o.Dir, "restart", 0, true, false, true); !errors.Is(err, ErrSpawningHalted) {
		t.Fatalf("management restart while frozen = %v, want ErrSpawningHalted", err)
	}
	smoke, err := o.Sup.Spawn("smokealarm", "smokealarm", 0, o.Dir, "check")
	if !errors.Is(err, ErrSpawningHalted) {
		t.Fatalf("smoke-alarm spawn while frozen = %v, want ErrSpawningHalted", err)
	}
	if smoke != "" {
		t.Fatalf("frozen smoke-alarm spawn returned %q", smoke)
	}
	if err := sockc.Call(o.Sup.SocketPath, "user", "office.resume-spawns", nil, nil); err == nil {
		t.Fatal("user bypassed the CEO wake-mail sequence")
	}
	if err := sockc.Call(o.Sup.SocketPath, firefighter, "office.resume-spawns", nil, nil); err == nil {
		t.Fatal("firefighter bypassed the CEO wake-mail sequence")
	}
	waitDone := make(chan error, 1)
	go func() {
		_, err := o.Sup.waitVerb(firefighter, 0)
		waitDone <- err
	}()
	waitFor(t, time.Second, "firefighter parked for freeze", func() bool {
		return agentState(t, o, firefighter) == "waiting"
	})
	if _, err := o.Sup.Mail.Send(bus.SystemSender, firefighter, "ordinary", "stay parked", bus.PrioNormal); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waitDone:
		t.Fatalf("ordinary mail released frozen waiter: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err := sockc.Call(o.Sup.SocketPath, ceo, "office.resume-spawns", nil, nil); err == nil {
		t.Fatal("CEO resumed a freeze without global wake-up mail")
	}
	if err := sockc.Call(o.Sup.SocketPath, ceo, "office.unfreeze", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-waitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("global wake-up did not release frozen waiter")
	}
	if o.Sup.Frozen() {
		t.Fatal("office remained frozen after CEO resumed spawning")
	}
	if !o.Sup.spawnAllowed("developer") || !o.Sup.spawnAllowed("smokealarm") {
		t.Fatal("normal and safety spawns were not restored")
	}
	messages, err := o.Sup.Mail.History()
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 || messages[0].Subject != "Office unfrozen" || messages[0].From != ceo {
		t.Fatalf("CEO global wake-up mail = %+v", messages)
	}
}

func TestFreezePreservesDeferredCapacitySpawns(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.pendingRestarts = map[string]capacitySpawn{}
	o.Sup.pendingRestarts["developer-restart"] = capacitySpawn{role: "developer", profile: "developer", managementRestart: true}
	o.Sup.pendingSmoke = []capacitySpawn{{role: "smokealarm", profile: "smokealarm"}}
	if err := o.Sup.BeginFreeze(bus.SystemSender); err != nil {
		t.Fatal(err)
	}
	// Exercise the post-dequeue freeze race directly: a restart taken just as
	// freezing begins must be put back when spawnAttempt rejects it.
	o.Sup.resumeExplicitRestarts()
	o.Sup.dispatchOnce()
	o.Sup.runSmokeRound()
	o.Sup.mu.Lock()
	defer o.Sup.mu.Unlock()
	if _, ok := o.Sup.pendingRestarts["developer-restart"]; !ok {
		t.Fatal("frozen dispatcher consumed a deferred restart")
	}
	if len(o.Sup.pendingSmoke) != 1 {
		t.Fatalf("frozen smoke loop consumed %d pending spawns", len(o.Sup.pendingSmoke))
	}
}

func TestFreezeWaitsForTheFinalSpawnBoundary(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.spawnGate.RLock()
	done := make(chan error, 1)
	go func() { done <- o.Sup.BeginFreeze(bus.SystemSender) }()
	select {
	case err := <-done:
		t.Fatalf("freeze crossed an active spawn boundary: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	o.Sup.spawnGate.RUnlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("freeze did not complete after spawn boundary closed")
	}
}

func TestSmokeAlarmMayWaitOnlyDuringFreeze(t *testing.T) {
	o := newOffice(t, map[string]string{"smokealarm": "ready\nsleep|60s\n"})
	smoke, err := o.Sup.Spawn("smokealarm", "smokealarm", 0, o.Dir, "check")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, "smoke alarm ready", func() bool { return agentState(t, o, smoke) == "working" })
	if _, err := o.Sup.waitVerb(smoke, time.Millisecond); err == nil {
		t.Fatal("smoke alarm parked outside a freeze")
	}
	if err := o.Sup.BeginFreeze(bus.SystemSender); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Sup.Mail.Send(bus.SystemSender, smoke, "older", "unread", bus.PrioNormal); err != nil {
		t.Fatal(err)
	}
	response, err := o.Sup.waitVerb(smoke, time.Millisecond)
	if err != nil {
		t.Fatalf("smoke alarm could not obey freeze wait instruction: %v", err)
	}
	if response.Reason != "timeout" {
		t.Fatalf("older unread mail released freeze wait: %+v", response)
	}
}
