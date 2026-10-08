package sandbox

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func requireKernelSandbox(t *testing.T) {
	t.Helper()
	if err := platformCheck(); err != nil {
		if errors.Is(err, ErrUnsupported) {
			t.Skip(err)
		}
		t.Fatal(err)
	}
}

func TestPrepareExcludesRealHomeAndCreatesPrivateLayout(t *testing.T) {
	requireKernelSandbox(t)
	base := t.TempDir()
	home := filepath.Join(base, "user")
	office := filepath.Join(base, "office")
	state := filepath.Join(home, ".codex")
	for _, dir := range []string{state, office} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := Prepare(Options{RealHome: home, OfficeRoot: office, HomeLinks: []string{".codex"}, Command: "/bin/sh", TempParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if !filepath.IsAbs(prepared.Policy.PrivateHome) || !filepath.IsAbs(prepared.Policy.PrivateTemp) {
		t.Fatal("private paths must be absolute")
	}
	link, err := os.Readlink(filepath.Join(prepared.Policy.PrivateHome, ".codex"))
	if err != nil || link != state {
		t.Fatalf("home link = %q, %v", link, err)
	}
	config, err := os.ReadFile(filepath.Join(prepared.Policy.PrivateHome, ".gitconfig"))
	if err != nil || !strings.Contains(string(config), "[safe]") || !strings.Contains(string(config), "directory = *") {
		t.Fatalf("gitconfig = %q, %v", config, err)
	}
	for _, path := range prepared.Policy.ReadPaths {
		if path == home || path == filepath.Dir(home) {
			t.Fatalf("real home parent allowed: %s", path)
		}
	}
	if !contains(prepared.Policy.WriteDirs, state) {
		t.Fatal("state link target not writable")
	}
	if !contains(prepared.Policy.ReadPaths, office) {
		t.Fatal("office missing from read allowlist")
	}
	if err := prepared.SetPTY("/dev/null"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(prepared.PolicyPath)
	if err != nil {
		t.Fatal(err)
	}
	var saved Policy
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.PTY != "/dev/null" {
		t.Fatalf("saved PTY = %q", saved.PTY)
	}
	private := prepared.Policy.PrivateHome
	prepared.Cleanup()
	if _, err := os.Stat(private); !os.IsNotExist(err) {
		t.Fatalf("private home still exists: %v", err)
	}
}

func TestPrepareRejectsRelativeOrMissingPaths(t *testing.T) {
	requireKernelSandbox(t)
	_, err := Prepare(Options{RealHome: "relative", OfficeRoot: t.TempDir(), Command: "/bin/sh"})
	if err == nil {
		t.Fatal("relative home accepted")
	}
	_, err = Prepare(Options{RealHome: t.TempDir(), OfficeRoot: t.TempDir(), Command: "/missing/command"})
	if err == nil {
		t.Fatal("missing command accepted")
	}
}

func TestPrepareUsesExplicitHomeLinkTargetAndOwnsEnvironment(t *testing.T) {
	requireKernelSandbox(t)
	home := t.TempDir()
	state := t.TempDir()
	prepared, err := Prepare(Options{RealHome: home, OfficeRoot: t.TempDir(), Command: "/bin/sh", HomeLinks: []string{".codex"}, HomeLinkTargets: map[string]string{".codex": state}, TempParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if got, err := os.Readlink(filepath.Join(prepared.Policy.PrivateHome, ".codex")); err != nil || got != state {
		t.Fatalf("Codex state link = %q, %v", got, err)
	}
	if !contains(prepared.Policy.WriteDirs, state) {
		t.Fatal("explicit Codex state is not writable")
	}
	env := Environment([]string{"HOME=/real", "GH_TOKEN=secret", "CODEX_HOME=" + state, "OMO_SOCKET=/socket"}, prepared.Policy)
	for _, item := range env {
		if item == "GH_TOKEN=secret" || item == "HOME=/real" {
			t.Fatalf("unsafe environment retained: %q", item)
		}
	}
	if !contains(env, "HOME="+prepared.Policy.PrivateHome) || !contains(env, "OMO_SOCKET=/socket") {
		t.Fatalf("sandbox environment = %v", env)
	}
}

func TestPrepareKeepsHomePrivateWhenCommandLivesAtHomeRoot(t *testing.T) {
	requireKernelSandbox(t)
	home := t.TempDir()
	command := filepath.Join(home, "tool")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(Options{RealHome: home, OfficeRoot: t.TempDir(), Command: command, TempParent: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Cleanup()
	if contains(prepared.Policy.ReadPaths, home) {
		t.Fatal("real HOME granted as executable directory")
	}
	if !contains(prepared.Policy.ReadPaths, command) {
		t.Fatal("executable file missing")
	}
}

func TestPrepareRejectsHomeLinkThatResolvesToWholeHome(t *testing.T) {
	requireKernelSandbox(t)
	home := t.TempDir()
	if err := os.Symlink(home, filepath.Join(home, ".codex")); err != nil {
		t.Fatal(err)
	}
	prepared, err := Prepare(Options{RealHome: home, OfficeRoot: t.TempDir(), Command: "/bin/sh", HomeLinks: []string{".codex"}, TempParent: t.TempDir()})
	if prepared != nil {
		prepared.Cleanup()
	}
	if err == nil {
		t.Fatal("whole HOME linked read-write")
	}
}

func contains(paths []string, path string) bool {
	for _, p := range paths {
		if p == path {
			return true
		}
	}
	return false
}
