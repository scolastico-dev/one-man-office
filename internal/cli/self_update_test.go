package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/selfupdate"
	"github.com/spf13/cobra"
)

func TestSelfUpdateInstallsNewerLatestRelease(t *testing.T) {
	restoreSelfUpdateCommandHooks(t)
	latestRelease = func(context.Context) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: "v1.1.0"}, nil
	}
	installed := false
	installRelease = func(_ context.Context, release selfupdate.Release, target string) error {
		installed = release.Tag == "v1.1.0" && target != ""
		return nil
	}
	currentExecutable = func() (string, error) { return filepath.Join(t.TempDir(), "omo"), nil }

	cmd, out, stderr := selfUpdateCommand("v1.0.0", "self-update")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("newer release was not installed")
	}
	if !strings.Contains(out.String(), "updated omo to v1.1.0") || stderr.Len() != 0 {
		t.Fatalf("unexpected command output: stdout=%q stderr=%q", out.String(), stderr.String())
	}
}

func TestSelfUpdateCheckReportsAvailabilityWithoutInstalling(t *testing.T) {
	restoreSelfUpdateCommandHooks(t)
	latestRelease = func(context.Context) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: "v1.1.0"}, nil
	}
	installRelease = func(context.Context, selfupdate.Release, string) error {
		t.Fatal("--check must not install a binary")
		return nil
	}

	cmd, out, _ := selfUpdateCommand("v1.0.0", "self-update", "--check")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "omo v1.1.0 is available (running v1.0.0)") {
		t.Fatalf("availability was not reported: %q", out.String())
	}
}

func TestSelfUpdateExplicitVersionInstallsDowngradeAndDisablesOfficeChecks(t *testing.T) {
	restoreSelfUpdateCommandHooks(t)
	officeDir := t.TempDir()
	configPath := filepath.Join(officeDir, ".omo", "omo.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	config := "# keep this comment\nstartup:\n  check_self_update: true # keep this too\n\nmodels: {}\n"
	if err := os.WriteFile(configPath, []byte(config), 0o640); err != nil {
		t.Fatal(err)
	}
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	nestedDir := filepath.Join(officeDir, "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(nestedDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	releaseByTag = func(_ context.Context, tag string) (selfupdate.Release, error) {
		if tag != "v1.1.0" {
			t.Fatalf("requested tag = %q", tag)
		}
		return selfupdate.Release{Tag: "v1.1.0"}, nil
	}
	installed := false
	installRelease = func(_ context.Context, release selfupdate.Release, target string) error {
		installed = release.Tag == "v1.1.0" && target != ""
		return nil
	}
	currentExecutable = func() (string, error) { return filepath.Join(officeDir, "omo"), nil }

	cmd, out, _ := selfUpdateCommand("v1.2.0", "self-update", "--version", "v1.1.0")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("explicit older release was not installed")
	}
	updated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(updated), "# keep this comment") || !strings.Contains(string(updated), "check_self_update: false # keep this too") {
		t.Fatalf("office configuration was not safely updated:\n%s", updated)
	}
	if !strings.Contains(out.String(), "disabled automatic self-update checks") {
		t.Fatalf("setting change was not reported: %q", out.String())
	}
}

func TestSelfUpdateExplicitVersionWorksOutsideOffice(t *testing.T) {
	restoreSelfUpdateCommandHooks(t)
	dir := t.TempDir()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	releaseByTag = func(context.Context, string) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: "v1.0.0"}, nil
	}
	installed := false
	installRelease = func(context.Context, selfupdate.Release, string) error {
		installed = true
		return nil
	}
	currentExecutable = func() (string, error) { return filepath.Join(dir, "omo"), nil }

	cmd, out, _ := selfUpdateCommand("v1.2.0", "self-update", "--version", "v1.0.0")
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !installed {
		t.Fatal("explicit release was not installed outside an office")
	}
	if !strings.Contains(out.String(), "no office setting was changed") {
		t.Fatalf("outside-office outcome was not reported: %q", out.String())
	}
}

func TestSelfUpdateExplicitVersionDoesNotInstallWhenOfficeAutoUpdateCannotBeDisabled(t *testing.T) {
	restoreSelfUpdateCommandHooks(t)
	releaseByTag = func(context.Context, string) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: "v1.1.0"}, nil
	}
	findOfficeConfigForSelfUpdate = func() (string, bool, error) {
		return "/office/.omo/omo.yaml", true, nil
	}
	setCheckSelfUpdateForSelfUpdate = func(string, bool) error {
		return errors.New("permission denied")
	}
	installRelease = func(context.Context, selfupdate.Release, string) error {
		t.Fatal("explicit install ran after automatic updates could not be disabled")
		return nil
	}

	cmd, _, _ := selfUpdateCommand("v1.2.0", "self-update", "--version", "v1.1.0")
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "disable automatic self-update") {
		t.Fatalf("explicit update error = %v", err)
	}
}

func TestSelfUpdateExplicitVersionDoesNotDisableOfficeAutoUpdateBeforeResolvingExecutable(t *testing.T) {
	restoreSelfUpdateCommandHooks(t)
	releaseByTag = func(context.Context, string) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: "v1.1.0"}, nil
	}
	findOfficeConfigForSelfUpdate = func() (string, bool, error) {
		return "/office/.omo/omo.yaml", true, nil
	}
	setCheckSelfUpdateForSelfUpdate = func(string, bool) error {
		t.Fatal("automatic updates were disabled before resolving the executable")
		return nil
	}
	currentExecutable = func() (string, error) {
		return "", errors.New("executable unavailable")
	}

	cmd, _, _ := selfUpdateCommand("v1.2.0", "self-update", "--version", "v1.1.0")
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "executable unavailable") {
		t.Fatalf("explicit update error = %v", err)
	}
}

func TestSelfUpdateRejectsCheckWithExplicitVersion(t *testing.T) {
	cmd, _, _ := selfUpdateCommand("v1.0.0", "self-update", "--check", "--version", "v1.1.0")
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "none of the others") {
		t.Fatalf("--check with --version error = %v", err)
	}
}

func selfUpdateCommand(version string, args ...string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := Root(version)
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SetArgs(args)
	return cmd, out, stderr
}

func restoreSelfUpdateCommandHooks(t *testing.T) {
	t.Helper()
	oldLatest, oldInstall, oldExecutable := latestRelease, installRelease, currentExecutable
	oldByTag, oldFind, oldSet := releaseByTag, findOfficeConfigForSelfUpdate, setCheckSelfUpdateForSelfUpdate
	t.Cleanup(func() {
		latestRelease, installRelease, currentExecutable = oldLatest, oldInstall, oldExecutable
		releaseByTag, findOfficeConfigForSelfUpdate, setCheckSelfUpdateForSelfUpdate = oldByTag, oldFind, oldSet
	})
}
