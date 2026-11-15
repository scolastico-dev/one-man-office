package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func build(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "fakeagent")
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return exe
}

// environment is the test's environment without anything of the fake agent.
func environment(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if len(kv) >= 4 && kv[:4] == "OMO_" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func TestRefusesWithoutEnable(t *testing.T) {
	cmd := exec.Command(build(t))
	cmd.Env = environment()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 2 {
		t.Fatalf("want exit code 2, got %v", err)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("OMO_FAKEAGENT=1")) {
		t.Fatalf("the message does not say why: %q", stderr.String())
	}
}

func TestRunsWhenEnabled(t *testing.T) {
	cmd := exec.Command(build(t))
	cmd.Env = environment("OMO_FAKEAGENT=1", "OMO_FAKE_SCRIPT=print|hello from the fake")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("hello from the fake")) {
		t.Fatalf("output %q", out)
	}
}
