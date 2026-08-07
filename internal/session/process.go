package session

import "io"

// terminalProcess hides the OS-specific interactive-process implementation.
// Unix uses a PTY and Windows uses ConPTY.
type terminalProcess interface {
	io.Reader
	io.Writer
	Resize(rows, cols uint16) error
	Kill() error
	Wait() error
	Close() error
}
