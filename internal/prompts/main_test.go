package prompts

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "omo-prompts-home-*")
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
