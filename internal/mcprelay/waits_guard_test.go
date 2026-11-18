package mcprelay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A test here waits for the exact event it depends on (a channel, a join, the
// upstream's own answer) and never decides a hang by a clock: under load a
// fixed limit fails a correct run, and a test binary that really hangs ends
// with its own time limit and a dump of every goroutine. This fails when a
// test brings a fixed-time select back.
func TestTestsDecideNoHangByAClock(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("test files: %v, %v", files, err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "time"+".After(") && filepath.Base(f) != "waits_guard_test.go" {
			t.Errorf("%s waits with time.After: wait for the event itself", f)
		}
	}
}
