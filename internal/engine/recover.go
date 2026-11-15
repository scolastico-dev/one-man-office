package engine

import "context"

// recoverAtStart prepares what a run needs before its first pass, after
// startup ended the sessions and claims of the previous run.
func (e *Engine) recoverAtStart(ctx context.Context) {}
