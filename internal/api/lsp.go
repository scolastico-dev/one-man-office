package api

// LSPSettings configures user-registered stdio language servers in managed
// worktrees. An empty Servers list starts nothing.
type LSPSettings struct {
	Servers []LSPServer `yaml:"servers" json:"servers"`
	// Debounce is the quiet interval before reporting changed diagnostics;
	// default 500ms, 0s disables the delay, maximum 1m.
	Debounce Duration `yaml:"debounce" json:"debounce"`
	// ForceChat lists Profile.CLI values whose notifications must go to chat,
	// taking precedence over an active MCP notification listener.
	ForceChat []string `yaml:"force_chat" json:"force_chat"`
}

// LSPServer registers one named stdio server. Its working directory is always
// the managed worktree root. Command is an executable (PATH is searched for
// a bare name), Args are literal argv entries, Env holds literal overrides.
// Nothing is installed automatically; Enabled must be set explicitly.
type LSPServer struct {
	Name    string            `yaml:"name" json:"name"`
	Enabled bool              `yaml:"enabled" json:"enabled"`
	Command string            `yaml:"command" json:"command"`
	Args    []string          `yaml:"args" json:"args"`
	Env     map[string]string `yaml:"env" json:"env"`
	// Files are tried in order; the first match supplies the language ID.
	// No match means the file is not selected. Servers may overlap.
	Files []LSPFileSelector `yaml:"files" json:"files"`
	// InitializationOptions is the initialize request's initializationOptions.
	// Configuration is the server settings object, for configuration requests
	// and changes. Both contain only JSON values, including nested nulls.
	InitializationOptions map[string]any `yaml:"initialization_options" json:"initialization_options"`
	Configuration         map[string]any `yaml:"configuration" json:"configuration"`
}

// LSPFileSelector pairs a file glob with its LSP language ID. Pattern uses
// case-sensitive Go path.Match syntax (*, ?, character classes) on every OS.
// Without a slash it matches a basename at any depth; with a slash it matches
// the slash-separated path relative to the worktree root. No **, backslash,
// colon, absolute path or empty, dot or parent component is accepted.
type LSPFileSelector struct {
	Pattern    string `yaml:"pattern" json:"pattern"`
	LanguageID string `yaml:"language_id" json:"language_id"`
}

// LSPIgnoreRequest is the body of POST /api/lsp/ignore: the id of a reported
// language-server issue, as the notification or `omo ai lsp ignore` names it.
type LSPIgnoreRequest struct {
	ID string `json:"id"`
}

// LSPIgnoreResult confirms that an issue is suppressed in the caller's
// worktree: it is no longer reported there, queued or in the future, while
// the language server keeps reporting it. File is slash-separated and
// relative to Worktree; the position is one-based as editors show it.
type LSPIgnoreResult struct {
	ID       string `json:"id"`
	Ignored  bool   `json:"ignored"`
	Worktree string `json:"worktree"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Message  string `json:"message"`
}

// LSPConfirmRequest is the body of POST /api/lsp/confirm: the token of the
// probe omo sent over the caller's MCP notification stream.
type LSPConfirmRequest struct {
	Token string `json:"token"`
}

// LSPConfirmResult says the probe's token was accepted: from now on, while
// that stream stays open, omo tells the caller about language-server issues
// over MCP instead of in the terminal.
type LSPConfirmResult struct {
	Confirmed bool `json:"confirmed"`
}
