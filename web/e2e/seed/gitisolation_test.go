package main

import (
	"os"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"github.com/scolastico-dev/one-man-office/internal/gittest"
)

// TestMain keeps git away from the developer's git configuration.
func TestMain(m *testing.M) {
	gittest.Isolate()
	os.Exit(childenvtest.Run(m))
}
