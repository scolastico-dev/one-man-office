package engine

import (
	"context"

	"github.com/scolastico-dev/one-man-office/internal/board"
)

// Ready acknowledges the start of the agent session a and returns its role
// prompt.
func (e *Engine) Ready(ctx context.Context, a board.Actor) (string, error) { return "", nil }
