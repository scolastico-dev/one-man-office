//go:build !windows && !linux

package session

// awaitExit has no portable way to wait without reaping on this system;
// the caller then reaps directly. Between that reaping and the caller
// marking the process as reaped, a signal could reach a reused process id.
func awaitExit(int) bool { return false }
