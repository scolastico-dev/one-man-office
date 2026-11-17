package engine

// SettingsChanged tells the engine that the settings changed: omo's own git
// client takes the agent environment of the new settings at once (instead
// of at the next health check; in the background, see
// requestGitEnvironment), and a pass follows. The environment's commands
// read the settings, so the caller must not hold the settings store's
// lock; the store's change callback may only call Wake.
func (e *Engine) SettingsChanged() {
	e.requestGitEnvironment()
	e.Wake()
}
