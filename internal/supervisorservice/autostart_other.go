//go:build !linux && !darwin && !windows

package supervisorservice

import "fmt"

func installAutostart(executable, config string) (string, error) {
	return "", fmt.Errorf("supervisor autostart is supported on Linux, macOS, and Windows")
}
func removeAutostart(config string) error {
	return fmt.Errorf("supervisor autostart is supported on Linux, macOS, and Windows")
}
