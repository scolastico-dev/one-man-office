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

func TestCleanupEntryCapsRemoveOldestSafeRows(t *testing.T) {
	d := open(t)
	for _, stmt := range []string{
		`INSERT INTO agents (name,role,profile,state,created_at) VALUES ('old-dead','freelancer','x','dead','2026-01-01 00:00:00')`,
		`INSERT INTO agents (name,role,profile,state,created_at) VALUES ('new-dead','freelancer','x','dead','2026-01-02 00:00:00')`,
		`INSERT INTO agents (name,role,profile,state,created_at) VALUES ('living','freelancer','x','working','2025-01-01 00:00:00')`,
		`INSERT INTO messages (from_agent,to_target,subject,body,created_at,read_at) VALUES ('a','b','old','x','2026-01-01 00:00:00','2026-01-01 01:00:00')`,
		`INSERT INTO messages (from_agent,to_target,subject,body,created_at,read_at) VALUES ('a','b','new','x','2026-01-02 00:00:00','2026-01-02 01:00:00')`,
		`INSERT INTO messages (from_agent,to_target,subject,body,created_at) VALUES ('a','b','unread','x','2025-01-01 00:00:00')`,
		`INSERT INTO incidents (agent,class,state,created_at,resolved_at) VALUES ('a','x','resolved','2026-01-01 00:00:00','2026-01-01 01:00:00')`,
		`INSERT INTO incidents (agent,class,state,created_at,resolved_at) VALUES ('a','x','resolved','2026-01-02 00:00:00','2026-01-02 01:00:00')`,
		`INSERT INTO incidents (agent,class,state,created_at) VALUES ('a','x','open','2025-01-01 00:00:00')`,
	} {
		if _, err := d.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Cleanup(d, CleanupPolicy{MaxEntries: EntryCaps{Agents: 2, Messages: 2, Incidents: 2}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.CappedRows != 3 {
		t.Fatalf("capped rows = %d, want 3", got.CappedRows)
	}
	for table, want := range map[string]int{"agents": 2, "messages": 2, "incidents": 2} {
		var count int
		if err := d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s count = %d, want %d", table, count, want)
		}
	}
	for query, label := range map[string]string{
		`SELECT COUNT(*) FROM agents WHERE name = 'living'`:      "living agent",
		`SELECT COUNT(*) FROM messages WHERE subject = 'unread'`: "unread message",
		`SELECT COUNT(*) FROM incidents WHERE state = 'open'`:    "open incident",
	} {
		var count int
		_ = d.QueryRow(query).Scan(&count)
		if count != 1 {
			t.Errorf("%s was removed", label)
		}
	}
}

func TestCleanupEventCapPreservesStorageActivityDays(t *testing.T) {
	d := open(t)
	for _, timestamp := range []string{
		"2026-01-01 09:00:00", "2026-01-01 10:00:00",
		"2026-01-02 09:00:00", "2026-01-02 10:00:00",
		"2026-01-03 09:00:00", "2026-01-03 10:00:00",
	} {
		if _, err := d.Exec(`INSERT INTO events (kind,created_at) VALUES ('x',?)`, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Cleanup(d, CleanupPolicy{MaxEntries: EntryCaps{Events: 3}, StorageActiveDays: 3}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.CappedRows != 3 {
		t.Fatalf("capped rows = %d, want 3", got.CappedRows)
	}
	days, err := OfficeActivityDays(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 3 {
		t.Fatalf("activity days = %d, want 3", len(days))
	}
}

func TestCleanupEntryCapsCoverJobsCachesAndStatistics(t *testing.T) {
	d := open(t)
	for _, stmt := range []string{
		`INSERT INTO jobs (id,title,goal,role,state,created_at,updated_at) VALUES (1,'parent','g','product_manager','done','2026-01-01 00:00:00','2026-01-01 00:00:00')`,
		`INSERT INTO jobs (id,title,goal,role,parent_job,state,created_at,updated_at) VALUES (2,'child','g','developer',1,'done','2026-01-02 00:00:00','2026-01-02 00:00:00')`,
		`INSERT INTO jobs (id,title,goal,role,state,created_at,updated_at) VALUES (3,'active','g','freelancer','working','2025-01-01 00:00:00','2025-01-01 00:00:00')`,
		`INSERT INTO overall_statistics (model,updated_at) VALUES ('old','2026-01-01 00:00:00')`,
		`INSERT INTO overall_statistics (model,updated_at) VALUES ('new','2026-01-02 00:00:00')`,
		`INSERT INTO shutdown_contexts (role,agent,context,created_at) VALUES ('developer','old-agent','x','2026-01-01 00:00:00')`,
		`INSERT INTO shutdown_contexts (role,agent,context,created_at) VALUES ('developer','new-agent','x','2026-01-02 00:00:00')`,
		`INSERT INTO model_usage_snapshots (profile,provider,used_percent,fetched_at) VALUES ('old','claude',1,'2026-01-01 00:00:00')`,
		`INSERT INTO model_usage_snapshots (profile,provider,used_percent,fetched_at) VALUES ('new','claude',1,'2026-01-02 00:00:00')`,
	} {
		if _, err := d.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Cleanup(d, CleanupPolicy{MaxEntries: EntryCaps{
		Jobs: 1, OverallStatistics: 1, ShutdownContexts: 1, ModelUsageSnapshots: 1,
	}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.CappedRows != 5 {
		t.Fatalf("capped rows = %d, want 5", got.CappedRows)
	}
	for table, want := range map[string]string{
		"jobs":                  "3",
		"overall_statistics":    "new",
		"shutdown_contexts":     "new-agent",
		"model_usage_snapshots": "new",
	} {
		var value string
		column := "model"
		switch table {
		case "jobs":
			column = "id"
		case "shutdown_contexts":
			column = "agent"
		case "model_usage_snapshots":
			column = "profile"
		}
		if err := d.QueryRow(`SELECT CAST(` + column + ` AS TEXT) FROM ` + table).Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != want {
			t.Errorf("%s retained %q, want %q", table, value, want)
		}
	}
}
