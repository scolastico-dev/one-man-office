//go:build !linux && !darwin && !windows

package service

import "fmt"

func installAutostart(executable, config string) (string, error) {
	return "", fmt.Errorf("autostart is supported on Linux, macOS, and Windows")
}
func removeAutostart(config string) error {
	return fmt.Errorf("autostart is supported on Linux, macOS, and Windows")
}
