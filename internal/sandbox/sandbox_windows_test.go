//go:build windows

package sandbox

import (
	"errors"
	"testing"
)

func TestWindowsSandboxFailsTyped(t *testing.T) {
	if err := platformCheck(); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("platformCheck = %v", err)
	}
}
