package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/selfupdate"
)

func TestUpdateCheckReportsNewer(t *testing.T) {
	restoreUpdateHooks(t)
	latestRelease = func(context.Context) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: "v9.9.9"}, nil
	}
	installRelease = func(context.Context, selfupdate.Release, string) error {
		t.Fatal("--check must not install a binary")
		return nil
	}
	cmd, out := updateCommand("v1.0.0", "update", "--check")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "omo v9.9.9 is available (running v1.0.0)") {
		t.Fatalf("availability was not reported: %q", out.String())
	}
}

func TestUpdateExplicitVersionInstallsWithoutOfficeConfig(t *testing.T) {
	restoreUpdateHooks(t)
	releaseByTag = func(_ context.Context, tag string) (selfupdate.Release, error) {
		if tag != "v1.2.3" {
			t.Fatalf("requested tag = %q", tag)
		}
		return selfupdate.Release{Tag: tag}, nil
	}
	installed := ""
	installRelease = func(_ context.Context, release selfupdate.Release, target string) error {
		if target != "" {
			installed = release.Tag
		}
		return nil
	}
	currentExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "omo"), nil }
	cmd, out := updateCommand("v2.0.0", "update", "--version", "v1.2.3")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if installed != "v1.2.3" {
		t.Fatalf("installed = %q", installed)
	}
	if !strings.Contains(out.String(), "updated omo to v1.2.3") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestUpdateInstallsNewerLatestRelease(t *testing.T) {
	restoreUpdateHooks(t)
	latestRelease = func(context.Context) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: "v1.1.0"}, nil
	}
	installed := false
	installRelease = func(_ context.Context, release selfupdate.Release, _ string) error {
		installed = release.Tag == "v1.1.0"
		return nil
	}
	currentExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "omo"), nil }
	cmd, out := updateCommand("v1.0.0", "update")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !installed || !strings.Contains(out.String(), "updated omo to v1.1.0") {
		t.Fatalf("installed=%v output=%q", installed, out.String())
	}
}

func TestUpdateWhileTheServerRunsSaysToRestartIt(t *testing.T) {
	restoreUpdateHooks(t)
	startRoot(t)
	latestRelease = func(context.Context) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: "v1.1.0"}, nil
	}
	installRelease = func(context.Context, selfupdate.Release, string) error { return nil }
	currentExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "omo"), nil }
	cmd, out := updateCommand("v1.0.0", "update")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "restart it (omo stop, then omo)") {
		t.Fatalf("output=%q", out.String())
	}
}

func TestUpdateRejectsCheckWithExplicitVersion(t *testing.T) {
	cmd, _ := updateCommand("v1.0.0", "update", "--check", "--version", "v1.1.0")
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "none of the others") {
		t.Fatalf("--check with --version error = %v", err)
	}
}

func updateCommand(version string, args ...string) (*cobra.Command, *bytes.Buffer) {
	cmd := Root(version)
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	return cmd, out
}

// restoreUpdateHooks also gives the test a home of its own: an update looks
// whether a server of the home runs.
func restoreUpdateHooks(t *testing.T) {
	t.Helper()
	isolate(t)
	oldLatest, oldByTag, oldInstall, oldExecutable := latestRelease, releaseByTag, installRelease, currentExecutable
	t.Cleanup(func() {
		latestRelease, releaseByTag, installRelease, currentExecutable = oldLatest, oldByTag, oldInstall, oldExecutable
	})
}
