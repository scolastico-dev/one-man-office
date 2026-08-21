//go:build !linux && !windows

package session

func lowerProcessPriority(int, int) error { return nil }
