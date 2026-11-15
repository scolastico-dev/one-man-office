package engine

import (
	"context"

	"github.com/scolastico-dev/one-man-office/internal/board"
)

// cleanup removes what the done ticket t still has on disk.
func (e *Engine) cleanup(ctx context.Context, t board.EngineTicket) {}
