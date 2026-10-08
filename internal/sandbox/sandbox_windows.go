//go:build windows

package sandbox

import "fmt"

func platformCheck() error {
	return fmt.Errorf("%w: Windows kernel sandbox is not available", ErrUnsupported)
}
func execute(Policy, string, []string, []string) error { return platformCheck() }
