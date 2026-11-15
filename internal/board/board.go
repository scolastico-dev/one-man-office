// Package board owns the ticket board: tickets, their columns, descriptions,
// dependencies, comments and the agent session records. Every rule about who
// may change what lives here; callers (the HTTP server, the engine) only map
// requests onto Board methods. Each state change is one SQLite transaction
// that also appends the matching event to the event log.
package board

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Policy carries the global settings the board rules depend on.
type Policy struct {
	CEOEnabled              bool
	CEOMayApproveStories    bool
	TasksRequireUserRelease bool
	ReviewStory             api.Review
	ReviewTask              api.Review
	WhenApprovedStory       api.WhenApproved
	WhenApprovedTask        api.WhenApproved
}

// ProjectInfo is what the board needs to know about a registered project.
type ProjectInfo struct {
	ID      string
	Path    string
	Enabled bool
	Missing bool
}

// ModelInfo is what the board needs to know about a configured model.
type ModelInfo struct {
	ID         string
	Selectable bool
}

// Environment gives the board read access to settings and the file system.
// Implementations must be safe for concurrent use and must not use the
// board's database.
type Environment interface {
	Policy() Policy
	Project(id string) (ProjectInfo, bool)
	Model(id string) (ModelInfo, bool)
	// CurrentBranch returns the checked-out branch of the repository at
	// path, or "" for a detached HEAD.
	CurrentBranch(path string) (string, error)
}

// Options tune a Board.
type Options struct {
	// Now returns the current time; nil means time.Now.
	Now func() time.Time
	// Notify is called after every committed change, outside the
	// transaction; nil means no notification.
	Notify func()
}

// Board applies the board rules to the database.
type Board struct {
	db     *sql.DB
	env    Environment
	now    func() time.Time
	notify func()
}

// New returns a Board on an opened omo database.
func New(d *sql.DB, env Environment, o Options) *Board {
	b := &Board{db: d, env: env, now: o.Now, notify: o.Notify}
	if b.now == nil {
		b.now = time.Now
	}
	return b
}

// timeLayout is RFC 3339 in UTC with a fixed nanosecond width, so stored
// timestamps sort lexically.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// stamp returns the current time in the stored format.
func (b *Board) stamp() string {
	return b.now().UTC().Format(timeLayout)
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored time %q: %w", s, err)
	}
	return t, nil
}

func parseNullTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid {
		return nil, nil
	}
	t, err := parseTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// write runs fn in one transaction, commits it and then calls Notify. An
// error from fn rolls everything back.
func (b *Board) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if b.notify != nil {
		b.notify()
	}
	return nil
}

// read runs fn in one transaction that is never committed, so every query
// inside sees the same snapshot.
func (b *Board) read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}
