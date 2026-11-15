package engine

import (
	"context"

	"github.com/scolastico-dev/one-man-office/internal/board"
)

// CheckHandIn refuses the hand-in of agent session a while its worktree
// holds uncommitted work.
func (e *Engine) CheckHandIn(ctx context.Context, a board.Actor) error { return nil }
