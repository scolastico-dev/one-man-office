package board

import (
	"context"
	"database/sql"
	"errors"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

const stateKeyPaused = "paused"

// Paused reports whether starting new agents is paused. The flag survives
// restarts.
func (b *Board) Paused(ctx context.Context) (bool, error) {
	var v string
	err := b.db.QueryRowContext(ctx, `SELECT value FROM server_state WHERE key = ?`, stateKeyPaused).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v == "true", nil
}

// SetPaused stores the paused flag and emits server.paused when it changes.
func (b *Board) SetPaused(ctx context.Context, paused bool) error {
	return b.write(ctx, func(tx *sql.Tx) error {
		var old string
		err := tx.QueryRow(`SELECT value FROM server_state WHERE key = ?`, stateKeyPaused).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		v := "false"
		if paused {
			v = "true"
		}
		if old == v || (old == "" && !paused) {
			return nil
		}
		if _, err := tx.Exec(`INSERT INTO server_state (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, stateKeyPaused, v); err != nil {
			return err
		}
		return emit(tx, api.KindServerPaused, 0, User, api.ServerPausedData{Paused: paused})
	})
}
