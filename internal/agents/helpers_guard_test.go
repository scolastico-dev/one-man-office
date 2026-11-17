package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A helper process that blocks with a bare `select {}` is ended by the Go
// runtime as a deadlock when the test binary is built without cgo, which
// `make test` does. Helpers use blockForever instead.
func TestNoBareSelectInTestFiles(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f == "helpers_guard_test.go" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.TrimSpace(line) == "select {}" {
				t.Errorf("%s:%d: a bare select {} deadlocks without cgo; use blockForever", f, i+1)
			}
		}
	}
}
