//go:build !windows

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRestartProcessReplacesCurrentProcess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "restart-pids")
	cmd := exec.Command(os.Args[0], "-test.run=^TestRestartProcessHelper$")
	cmd.Env = append(os.Environ(), "OMO_RESTART_TEST_MARKER="+marker)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("restart helper failed: %v\n%s", err, output)
	}

	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(raw))
	if len(lines) != 2 {
		t.Fatalf("restart PIDs = %q, want two entries", raw)
	}
	if lines[0] != lines[1] {
		t.Fatalf("restart changed process from PID %s to %s; terminal ownership would return to the shell", lines[0], lines[1])
	}
}

func TestRestartProcessHelper(t *testing.T) {
	marker := os.Getenv("OMO_RESTART_TEST_MARKER")
	if marker == "" {
		return
	}

	raw, err := os.ReadFile(marker)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		if err := os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := restartProcess(os.Args[0]); err != nil {
			t.Fatal(err)
		}
		t.Fatal("restartProcess returned after a successful Unix restart")
	}

	f, err := os.OpenFile(marker, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, os.Getpid()); err != nil {
		t.Fatal(err)
	}
}
