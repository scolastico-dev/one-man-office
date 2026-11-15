package engine

// SettingsChanged tells the engine that the settings changed: omo's own git
// client takes the agent environment of the new settings at once (instead
// of at the next health check), and a pass follows. It may run the
// environment's commands, so the caller must not hold the settings store's
// lock; the store's change callback may only call Wake.
func (e *Engine) SettingsChanged() {
	e.syncGitEnvironment()
	e.Wake()
}
