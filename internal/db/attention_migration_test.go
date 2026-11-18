package db

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyAttentionOwnershipMigration(t *testing.T) {
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
	if _, err := d.Exec(`PRAGMA user_version = 8`); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id                                int
		text, owner, step, project, state string
	}{
		{1, "no usable model for the developer role; set models in roles/developer.yaml", "planner", "", "", ""},
		{2, "no usable model for the pm role; set models in roles/pm.yaml", "planner", "", "", ""},
		{3, "no usable model for the reviewer role; set models in roles/reviewer.yaml", "planner", "", "", ""},
		{4, "project api is missing or disabled; omo starts no agent on this ticket until it is available again", "planner", "", "api", ""},
		{5, "Look at the logs", "", "", "", ""},
		{6, "project " + strings.Repeat("p", 191) + "…", "", "", strings.Repeat("p", 200), ""},
		{7, "no usable model for the developer role; set models in roles/developer.yaml", "", "agent", "", ""},
		{8, "question 42 to the user has waited 1h for an answer", "", "", "api", "failed"},
		{9, "Export of api failed: no remote", "export", "", "api", "failed"},
		{10, "Export of api failed: " + strings.Repeat("x", 178) + "…", "", "", "api", "failed"},
		{11, "no usable model for the developer role; set models in roles/developer.yaml", "", "", "", ""},
		{12, "Export of ui failed: no remote; Export of api failed: no remote", "export", "", "ui", "failed"},
	}
	for _, c := range cases {
		typ, col, stage := "task", "review", "human"
		switch c.id {
		case 1, 7:
			col, stage = "in_progress", ""
		case 2:
			typ, col, stage = "story", "in_progress", ""
		case 3:
			stage = "agent"
		case 4:
			col, stage = "ready", ""
		}
		if _, err := d.Exec(`INSERT INTO tickets(id,type,title,col,rank,supervisor,created_by_role,created_at,updated_at,attention,failed_step,review_stage)
VALUES(?,?,'T',?,1,'user','user','2026-11-01T00:00:00Z','2026-11-01T00:00:00Z',?,?,?)`, c.id, typ, col, c.text, c.step, stage); err != nil {
			t.Fatal(err)
		}
		if c.project != "" {
			reason := "no remote"
			if c.id == 10 {
				reason = strings.Repeat("x", 200)
			}
			if _, err := d.Exec(`INSERT INTO ticket_projects(ticket_id,project_id,position,export_state,export_error) VALUES(?,?,0,?,?)`, c.id, c.project, c.state, reason); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := d.Exec(`INSERT INTO ticket_projects(ticket_id,project_id,position,export_state,export_error) VALUES(12,'api',1,'failed','no remote')`); err != nil {
		t.Fatal(err)
	}

	d.Close()
	for pass := 0; pass < 2; pass++ {
		d, err = Open(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			var owner, text string
			if err := d.QueryRow(`SELECT attention_owner, attention FROM tickets WHERE id = ?`, c.id).Scan(&owner, &text); err != nil {
				t.Fatal(err)
			}
			if owner != c.owner || (pass == 0 && text != c.text) {
				t.Errorf("pass %d ticket %d: owner %q text %q; want owner %q and original text", pass, c.id, owner, text, c.owner)
			}
		}
		// New text after the upgrade must never be interpreted by migration again.
		if _, err := d.Exec(`UPDATE tickets SET attention = 'no usable model for the developer role; set models in roles/developer.yaml' WHERE id = 5`); err != nil {
			t.Fatal(err)
		}
		d.Close()
	}
}
