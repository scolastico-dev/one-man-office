package main

import (
	"os"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/gittest"
)

// TestMain keeps git away from the developer's git configuration.
func TestMain(m *testing.M) {
	gittest.Isolate()
	os.Exit(m.Run())
}
