package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestDisplayStateMigrationPreservesOnlyExactUserStops(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	d, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:8] {
		if _, err := d.Exec(m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`PRAGMA user_version = 8;
INSERT INTO tickets(id,type,title,col,rank,supervisor,created_by_role,created_at,updated_at)
VALUES(1,'task','T','in_progress',1,'user','user','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"ceo", "pm", "developer", "reviewer"} {
		var ticket any = 1
		if role == "ceo" {
			ticket = nil
		}
		if _, err := d.Exec(`INSERT INTO agent_sessions(id,role,ticket_id,name,model,state,end_reason,started_at,ended_at)
VALUES(?,?,?,?,'fake/a','ended','stopped by the user','2026-11-01T00:00:00Z','2026-11-01T00:01:00Z')`, role, role, ticket, role); err != nil {
			t.Fatal(err)
		}
	}
	for id, reason := range map[string]string{"normal": "#1 is done", "unrelated": "stopped by the user yesterday"} {
		if _, err := d.Exec(`INSERT INTO agent_sessions(id,role,ticket_id,name,model,state,end_reason,started_at,ended_at)
VALUES(?,'developer',1,?,'fake/a','ended',?,'2026-11-01T00:00:00Z','2026-11-01T00:01:00Z')`, id, id, reason); err != nil {
			t.Fatal(err)
		}
	}
	d.Close()
	for pass := 0; pass < 2; pass++ {
		d, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		for id, want := range map[string]string{"ceo": "user", "pm": "user", "developer": "user", "reviewer": "user", "normal": "", "unrelated": ""} {
			var kind string
			if err := d.QueryRow(`SELECT end_kind FROM agent_sessions WHERE id = ?`, id).Scan(&kind); err != nil || kind != want {
				t.Errorf("pass %d: session %s kind = %q, want %q (%v)", pass, id, kind, want, err)
			}
		}
		// If the migration were replayed, this later change to display text
		// would turn a historical unknown outcome into an explicit stop.
		if _, err := d.Exec(`UPDATE agent_sessions SET end_reason = 'stopped by the user' WHERE id = 'normal'`); err != nil {
			t.Fatal(err)
		}
		d.Close()
	}
}
