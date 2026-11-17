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
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Policy carries the global settings the board rules depend on.
type Policy struct {
	CEOEnabled           bool
	CEOMayApproveStories bool
	// CEOTicketsNeedUserApproval: the tickets the CEO creates wait in
	// Planning until the user approves them; the CEO neither approves nor
	// withdraws them, whatever CEOMayApproveStories and the task release
	// say.
	CEOTicketsNeedUserApproval bool
	TasksRequireUserRelease    bool
	ReviewStory                api.Review
	ReviewTask                 api.Review
	ReviewStoryTask            api.Review
	WhenApprovedStory          api.WhenApproved
	WhenApprovedTask           api.WhenApproved
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
	// RefsLock is held by every ticket write that names a project or a
	// model, from the check that the project or model exists until the
	// write is committed. Code that removes projects or models from the
	// settings holds it from its check that no open ticket uses them until
	// the removal is written, so either the ticket write sees the removal
	// or the removal sees the ticket. It is taken before any transaction
	// and before the Environment is asked; nothing may take it while it
	// holds a transaction or a lock of the Environment. nil means a lock of
	// the board's own.
	RefsLock sync.Locker
}

// Board applies the board rules to the database.
type Board struct {
	db     *sql.DB
	env    Environment
	now    func() time.Time
	notify func()
	refs   sync.Locker
}

// New returns a Board on an opened omo database.
func New(d *sql.DB, env Environment, o Options) *Board {
	b := &Board{db: d, env: env, now: o.Now, notify: o.Notify, refs: o.RefsLock}
	if b.now == nil {
		b.now = time.Now
	}
	if b.refs == nil {
		b.refs = &sync.Mutex{}
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
	return b.writeChanges(ctx, func(tx *sql.Tx) (bool, error) {
		return true, fn(tx)
	})
}

// writeChanges is write for an fn that reports whether it changed anything:
// Notify is called only when it did. The engine calls such methods on every
// pass, and a notification would wake it for another pass.
func (b *Board) writeChanges(ctx context.Context, fn func(tx *sql.Tx) (bool, error)) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Rolls back on an error and on a panic in fn; a no-op after Commit.
	defer tx.Rollback()
	changed, err := fn(tx)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if changed && b.notify != nil {
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
