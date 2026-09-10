//go:build !linux && !darwin && !windows

package companyservice

import "fmt"

func installAutostart(executable, config string) (string, error) {
	return "", fmt.Errorf("company autostart is supported on Linux, macOS, and Windows")
}
func removeAutostart(config string) error {
	return fmt.Errorf("company autostart is supported on Linux, macOS, and Windows")
}
