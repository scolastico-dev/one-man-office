package engine

import (
	"context"

	"github.com/scolastico-dev/one-man-office/internal/board"
)

// runMerge merges ticket t, which is in Review at stage merging, into its
// target branches.
func (e *Engine) runMerge(ctx context.Context, t board.EngineTicket) {}
