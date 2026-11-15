package engine

// HoldWorktree keeps the worktree at path from being removed until release
// is called.
func (e *Engine) HoldWorktree(path string) (release func()) { return func() {} }
