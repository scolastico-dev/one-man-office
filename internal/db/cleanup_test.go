package db

import (
	"testing"
	"time"
)

func TestCleanupPreservesLiveStateAndRemovesExpiredHistory(t *testing.T) {
	d := open(t)
	for _, stmt := range []string{
		`INSERT INTO messages (from_agent,to_target,subject,body,read_at) VALUES ('a','b','expired','x','2026-01-01 00:00:00')`,
		`INSERT INTO messages (from_agent,to_target,subject,body,read_at) VALUES ('a','b','recent','x','2026-01-10 11:30:00')`,
		`INSERT INTO messages (from_agent,to_target,subject,body) VALUES ('a','b','unread','x')`,
		`INSERT INTO jobs (id,title,goal,role,state,updated_at) VALUES (1,'expired','g','freelancer','done','2026-01-01 00:00:00')`,
		`INSERT INTO jobs (id,title,goal,role,state,updated_at) VALUES (2,'working','g','freelancer','working','2026-01-01 00:00:00')`,
		`INSERT INTO jobs (id,title,goal,role,state,updated_at) VALUES (3,'parent','g','product_manager','done','2026-01-01 00:00:00')`,
		`INSERT INTO jobs (id,title,goal,role,parent_job,state,updated_at) VALUES (4,'child','g','developer',3,'working','2026-01-01 00:00:00')`,
	} {
		if _, err := d.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	got, err := Cleanup(d, CleanupPolicy{ReadMessagesAfter: time.Hour, TerminalJobsAfter: time.Hour}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Messages != 1 || got.Jobs != 1 {
		t.Fatalf("cleanup = %+v, want one message and one job", got)
	}
	for table, want := range map[string]int{"messages": 2, "jobs": 3} {
		var count int
		if err := d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s count = %d, want %d", table, count, want)
		}
	}
}

func TestCleanupDisabledDoesNothing(t *testing.T) {
	d := open(t)
	if _, err := d.Exec(`INSERT INTO messages (from_agent,to_target,subject,body,read_at) VALUES ('a','b','old','x','2000-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	got, err := Cleanup(d, CleanupPolicy{}, time.Now())
	if err != nil || got != (CleanupResult{}) {
		t.Fatalf("disabled cleanup = %+v, %v", got, err)
	}
	var count int
	_ = d.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&count)
	if count != 1 {
		t.Fatalf("disabled cleanup removed data")
	}
}
