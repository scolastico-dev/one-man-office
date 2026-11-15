package engine

import "context"

// runNotifier delivers board events (mentions, questions, answers, moves,
// retries) to the agents they concern until ctx ends.
func (e *Engine) runNotifier(ctx context.Context) {}
