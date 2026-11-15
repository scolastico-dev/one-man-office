package engine

import "context"

// exportFailed records that the export of ticket n failed with msg.
func (e *Engine) exportFailed(ctx context.Context, n int64, msg string) {}
