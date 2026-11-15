package engine

import (
	"context"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/board"
)

// Wait parks the agent session a until a notification arrives or timeout
// passes, and returns why it returned.
func (e *Engine) Wait(ctx context.Context, a board.Actor, timeout time.Duration) (string, error) {
	return "", nil
}
