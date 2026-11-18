//go:build !linux && !windows

package session

// startWithLoweredPriority runs start; lowering the priority of the started
// program is done on Linux only.
func startWithLoweredPriority(_ int, start func() error) error { return start() }
