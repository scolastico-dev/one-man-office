package engine

import "context"

// Shutdown asks every working agent for a handoff, waits until ctx ends or
// all have answered, then stops the engine (see halt).
func (e *Engine) Shutdown(ctx context.Context) {}

// StopAll pauses omo and ends every agent except the CEO at once.
func (e *Engine) StopAll(ctx context.Context) error { return nil }
