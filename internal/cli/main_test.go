package cli

import (
	"os"
	"testing"
)

// Never read or update the developer's global configuration during CLI tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "omo-cli-home-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("OMO_HOME", dir); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
