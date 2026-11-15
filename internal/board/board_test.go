package board

import (
	"context"
	"database/sql"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestWritePanicRollsBackAndFreesConnection(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected a panic")
			}
		}()
		_ = b.write(ctx, func(tx *sql.Tx) error {
			if _, err := tx.Exec(`INSERT INTO server_state (key, value) VALUES ('probe', 'x')`); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "after"})
	if _, err := b.Get(ctx, User, int64(tk.Number)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM server_state WHERE key = 'probe'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("panicked write was kept: %d %v", n, err)
	}
}

func TestInternalReadersWorkInsideTransaction(t *testing.T) {
	b, _ := newTestBoard(t)
	ctx := context.Background()
	tk := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}})
	dev := agent(t, b, api.RoleDeveloper, int64(tk.Number))
	err := b.write(ctx, func(tx *sql.Tx) error {
		ss, err := sessions(tx, int64(tk.Number), true)
		if err != nil || len(ss) != 1 {
			t.Errorf("sessions = %v %v", ss, err)
		}
		a, err := resolveSession(tx, dev.Session)
		if err != nil || a.Role != api.RoleDeveloper {
			t.Errorf("resolveSession = %+v %v", a, err)
		}
		if p, err := paused(tx); err != nil || p {
			t.Errorf("paused = %v %v", p, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
