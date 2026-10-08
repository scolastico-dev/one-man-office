//go:build linux

package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSandboxHelper(t *testing.T) {
	if os.Getenv("OMO_TEST_SANDBOX_HELPER") == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("OMO_TEST_ARGS")), &args); err != nil {
		t.Fatal(err)
	}
	if err := Exec(os.Getenv("OMO_TEST_POLICY"), os.Getenv("OMO_TEST_COMMAND"), args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(91)
	}
}

func TestSandboxSocketClient(t *testing.T) {
	if os.Getenv("OMO_TEST_SOCKET_CLIENT") == "" {
		return
	}
	conn, err := net.Dial("unix", os.Getenv("OMO_TEST_SOCKET"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
}

func sandboxCommand(t *testing.T, prepared *Prepared, command string, args ...string) *exec.Cmd {
	t.Helper()
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSandboxHelper$")
	cmd.Env = append(os.Environ(), "OMO_TEST_SANDBOX_HELPER=1", "OMO_TEST_POLICY="+prepared.PolicyPath, "OMO_TEST_COMMAND="+command, "OMO_TEST_ARGS="+string(encoded))
	return cmd
}

func TestLinuxSandboxFilesystemAndSocket(t *testing.T) {
	if err := platformCheck(); err != nil {
		if errors.Is(err, ErrUnsupported) {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	secretDir, err := os.MkdirTemp(home, ".omo-sandbox-test-")
	if err != nil {
		t.Skipf("cannot create real-home fixture: %v", err)
	}
	defer os.RemoveAll(secretDir)
	secret := filepath.Join(secretDir, "secret")
	if err := os.WriteFile(secret, []byte("hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	office := t.TempDir()
	if err := os.WriteFile(filepath.Join(office, "readme"), []byte("public"), 0600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, ".omo-test-state")
	if err := os.Mkdir(state, 0700); err != nil && !os.IsExist(err) {
		t.Fatal(err)
	}
	defer os.Remove(state)
	prepared, err := Prepare(Options{RealHome: home, OfficeRoot: office, HomeLinks: []string{".omo-test-state"}, Command: "/bin/sh", TempParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if err := os.Symlink(secret, filepath.Join(prepared.Policy.PrivateHome, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := prepared.SetPTY("/dev/null"); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("cat %q >/dev/null; echo ok > %q; if echo denied > %q; then exit 41; fi; if cat %q >/dev/null 2>&1; then exit 42; fi; if cat %q >/dev/null 2>&1; then exit 43; fi", filepath.Join(office, "readme"), filepath.Join(prepared.Policy.PrivateHome, "written"), filepath.Join(office, "blocked"), secret, filepath.Join(prepared.Policy.PrivateHome, "escape"))
	cmd := sandboxCommand(t, prepared, "/bin/sh", "-c", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sandbox command: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(prepared.Policy.PrivateHome, "written")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(office, "blocked")); !os.IsNotExist(err) {
		t.Fatalf("office write escaped policy: %v", err)
	}
	git := exec.Command("git", "init", "-q", office)
	if output, err := git.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	cmd = sandboxCommand(t, prepared, "/bin/sh", "-c", "git status --short >/dev/null")
	cmd.Dir = office
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status under sandbox: %v\n%s", err, output)
	}

	socket := filepath.Join(t.TempDir(), "rpc.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	prepared.Policy.Socket = socket
	prepared.Policy.WriteFiles = append(prepared.Policy.WriteFiles, socket)
	if err := prepared.save(); err != nil {
		t.Fatal(err)
	}
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			accepted <- err
			return
		}
		defer conn.Close()
		buf := make([]byte, 4)
		_, err = conn.Read(buf)
		accepted <- err
	}()
	cmd = sandboxCommand(t, prepared, os.Args[0], "-test.run=^TestSandboxSocketClient$")
	cmd.Env = append(cmd.Env, "OMO_TEST_SOCKET_CLIENT=1", "OMO_TEST_SOCKET="+socket)
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("socket client: %v\n%s", err, output)
	}
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("socket RPC timed out")
	}
}

func TestLinuxSandboxRejectsInvalidPolicyWithoutFallback(t *testing.T) {
	if err := platformCheck(); err != nil {
		t.Skip(err)
	}
	prepared, err := Prepare(Options{RealHome: os.Getenv("HOME"), OfficeRoot: t.TempDir(), Command: "/bin/sh", TempParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if err := prepared.SetPTY("/dev/null"); err != nil {
		t.Fatal(err)
	}
	prepared.Policy.ReadPaths = append(prepared.Policy.ReadPaths, "/nonexistent/omo-sandbox-path")
	if err := prepared.save(); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	cmd := sandboxCommand(t, prepared, "/bin/sh", "-c", "touch "+marker)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "/nonexistent/omo-sandbox-path") {
		t.Fatalf("invalid policy launched command: %v\n%s", err, output)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unsandboxed command ran: %v", err)
	}
}

func TestLinuxSandboxExecutesFileInHomeRootWithoutReadingHome(t *testing.T) {
	if err := platformCheck(); err != nil {
		t.Skip(err)
	}
	home := t.TempDir()
	command := filepath.Join(home, "tool")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nprintf launched\n"), 0700); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(Options{RealHome: home, OfficeRoot: t.TempDir(), Command: command, TempParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if err := prepared.SetPTY("/dev/null"); err != nil {
		t.Fatal(err)
	}
	output, err := sandboxCommand(t, prepared, command).CombinedOutput()
	if err != nil || string(output) != "launched" {
		t.Fatalf("home-root executable: %v %q", err, output)
	}
}
