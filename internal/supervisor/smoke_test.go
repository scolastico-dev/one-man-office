package supervisor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

func TestSmokeReportContainsAgentTailAndChatter(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer": "ready\nsleep|30s\n",
	})
	name, _ := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "long research goal")
	waitFor(t, 5*time.Second, "agent up", func() bool { return agentState(t, o, name) == "working" })
	report := o.Sup.smokeReport()
	for _, want := range []string{name, "freelancer", "long research goal", "PM lateral messages since last round"} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	// Second round: events since round one are included, not repeated forever.
	db.AppendEvent(o.DB, "test_marker", "", 0, "round-two-marker")
	report2 := o.Sup.smokeReport()
	if !strings.Contains(report2, "round-two-marker") {
		t.Error("second report missing new event")
	}
	report3 := o.Sup.smokeReport()
	if strings.Contains(report3, "round-two-marker") {
		t.Error("third report repeats old events — delta tracking broken")
	}
}

func TestSmokeLoopSpawnsFreshAlarmAndIncidentSpawnsFirefighter(t *testing.T) {
	o := newOffice(t, map[string]string{
		"freelancer":  "ready\nhang\n",
		"smokealarm":  "ready\nincident|freelancer|stuck|no output for a while\ndone|round complete: 1 incidents\n",
		"firefighter": "ready\nsleep|30s\n",
	})
	o.Sup.Cfg.SmokeAlarm = config.SmokeAlarm{Interval: config.Duration(500 * time.Millisecond), TailLines: 20}
	name, _ := o.Sup.Spawn("freelancer", "freelancer", 0, o.Dir, "hang forever")
	waitFor(t, 5*time.Second, "victim up", func() bool { return agentState(t, o, name) == "working" })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Sup.SmokeLoop(ctx)
	// A smoke alarm spawns, files an incident, exits; a firefighter spawns.
	waitFor(t, 30*time.Second, "open incident recorded", func() bool {
		var n int
		o.DB.QueryRow(`SELECT COUNT(*) FROM incidents WHERE state = 'open'`).Scan(&n)
		return n >= 1
	})
	waitFor(t, 30*time.Second, "firefighter living", func() bool {
		n, _ := db.CountLivingByRole(o.DB, "firefighter")
		return n == 1
	})
	// The firefighter's goal names the incident id.
	ff := onlyAgentOfRole(t, o, "firefighter")
	a, _ := db.GetAgent(o.DB, ff)
	if !strings.Contains(a.Goal, "INCIDENT_ID: ") {
		t.Fatalf("firefighter goal missing INCIDENT_ID line:\n%s", a.Goal)
	}
}
