//go:build unix

package usage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestRunRefusesAFIFO(t *testing.T) {
	cleanEnv(t)
	home := t.TempDir()
	fifo := filepath.Join(home, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runScript(t, readScript(fifo), RunOptions{Home: home})
	if err == nil || !strings.HasSuffix(err.Error(), "refused: file is not a regular file") {
		t.Fatalf("err = %v", err)
	}
}

// The real omo.agy_usage runs the profile's command with agy's usage
// arguments and the profile's environment in an empty folder of its own,
// which it removes afterwards.
func TestAgyUsageRunsTheProfileCommand(t *testing.T) {
	cleanEnv(t)
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-agy")
	script := "#!/bin/sh\nprintf '%s ' \"$@\"; printf '|%s|' \"$AGY_PLAIN\"; pwd; ls -A\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	j := agyJob()
	j.Profile.Command = fake
	j.Text = `function usage() local out, err = omo.agy_usage() error(tostring(out or err), 0) end`
	_, err := Run(context.Background(), j, RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t)})
	if err == nil {
		t.Fatal("no output")
	}
	out := err.Error()
	args, rest, _ := strings.Cut(out, "|x|")
	if args != "-p /usage --output-format json --print-timeout 25s " {
		t.Fatalf("args = %q (output %q)", args, out)
	}
	folder := strings.TrimSpace(rest)
	if !strings.Contains(filepath.Base(folder), "omo-agy-usage-") {
		t.Fatalf("folder = %q", folder)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("the folder %s was left behind: %v", folder, err)
	}
	j.Profile.Command = filepath.Join(dir, "missing")
	j.Text = `function usage() local out, err = omo.agy_usage() error(tostring(err), 0) end`
	if _, err := Run(context.Background(), j, RunOptions{Home: t.TempDir(), HTTPClient: noNetwork(t)}); err == nil || err.Error() != "the agy usage command failed" {
		t.Fatalf("missing command: err = %v", err)
	}
}
