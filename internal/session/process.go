package session

import "io"

// terminalProcess hides the OS-specific interactive-process implementation.
// Unix uses a PTY and Windows uses ConPTY.
type terminalProcess interface {
	io.Reader
	io.Writer
	Resize(rows, cols uint16) error
	Kill() error
	// Wait returns the main process's exit status once it has exited.
	Wait() error
	// Exited is closed once the main process has exited, even while a
	// child it left behind still holds the terminal open.
	Exited() <-chan struct{}
	// Pid is the main process's id (on Unix also its process group id).
	Pid() int
	Close() error
}
