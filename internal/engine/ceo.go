package engine

import (
	"context"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// StartCEO returns the live CEO session, starting one when there is none;
// fresh ends the live one first.
func (e *Engine) StartCEO(ctx context.Context, fresh bool) (api.AgentSession, error) {
	return api.AgentSession{}, nil
}
