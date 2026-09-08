package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"github.com/scolastico-dev/one-man-office/internal/selfupdate"
)

func TestStartupAcceptsSelfUpdateAndRestarts(t *testing.T) {
	dir := t.TempDir()
	if _, err := office.Setup(dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Startup.CheckSuperpowers = false
	restoreStartupHooks(t)
	inputIsTerminal = func(io.Reader) bool { return true }
	latestRelease = func(context.Context) (selfupdate.Release, error) {
		return selfupdate.Release{Tag: "v1.1.0"}, nil
	}
	installed, restarted := false, false
	installRelease = func(_ context.Context, release selfupdate.Release, target string) error {
		installed = release.Tag == "v1.1.0" && target != ""
		return nil
	}
	currentExecutable = func() (string, error) { return filepath.Join(dir, "omo"), nil }
	launchRestart = func(string) error { restarted = true; return nil }
	cmd, _, _ := startupCommand("yes\n")
	got, err := runStartupChecks(cmd, dir, cfg, "1.0.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if !got || !installed || !restarted {
		t.Fatalf("restarted=%v installed=%v launched=%v", got, installed, restarted)
	}
}

func TestStartupAcceptsEmbeddedAssetUpdateAndRestarts(t *testing.T) {
	dir := t.TempDir()
	if _, err := office.Setup(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, office.TemplatesVersionPath), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(dir, ".omo", "plugins", "nudge", "nudge.lua")
	wantPlugin, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginPath, []byte("-- old bundled plugin"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Startup.CheckSelfUpdate = false
	cfg.Startup.CheckSuperpowers = false
	restoreStartupHooks(t)
	inputIsTerminal = func(io.Reader) bool { return true }
	currentExecutable = func() (string, error) { return filepath.Join(dir, "omo"), nil }
	restarted := false
	launchRestart = func(string) error { restarted = true; return nil }
	cmd, out, stderr := startupCommand("y\n")
	got, err := runStartupChecks(cmd, dir, cfg, "1.0.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if !got || !restarted {
		t.Fatalf("restarted=%v launched=%v", got, restarted)
	}
	if !strings.Contains(stderr.String(), "bundled plugins") || !strings.Contains(out.String(), "bundled-plugin edits") {
		t.Fatalf("startup did not explain bundled plugin replacement: stdout=%q stderr=%q", out.String(), stderr.String())
	}
	if outdated, err := office.TemplatesOutdated(dir); err != nil || outdated {
		t.Fatalf("embedded assets still outdated=%v, err=%v", outdated, err)
	}
	if got, err := os.ReadFile(pluginPath); err != nil || string(got) != string(wantPlugin) {
		t.Fatalf("bundled plugin was not updated: %q, err=%v", got, err)
	}
}

func TestStartupDeclinesEmbeddedAssetUpdateAndPreservesBundledPlugin(t *testing.T) {
	dir := t.TempDir()
	if _, err := office.Setup(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, office.TemplatesVersionPath), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(dir, ".omo", "plugins", "nudge", "nudge.lua")
	const customized = "-- keep my local plugin edit"
	if err := os.WriteFile(pluginPath, []byte(customized), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Startup.CheckSelfUpdate = false
	cfg.Startup.CheckSuperpowers = false
	cfg.Plugins.UpdateOnStart = false
	restoreStartupHooks(t)
	inputIsTerminal = func(io.Reader) bool { return true }
	cmd, out, _ := startupCommand("n\n")
	restarted, err := runStartupChecks(cmd, dir, cfg, "1.0.0", true)
	if err != nil || restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
	if !strings.Contains(out.String(), "bundled-plugin edits") {
		t.Fatalf("startup did not ask before replacement: %q", out.String())
	}
	if got, err := os.ReadFile(pluginPath); err != nil || string(got) != customized {
		t.Fatalf("declined update changed bundled plugin: %q, err=%v", got, err)
	}
	if outdated, err := office.TemplatesOutdated(dir); err != nil || !outdated {
		t.Fatalf("declined update changed generation marker: outdated=%v, err=%v", outdated, err)
	}
}

func TestStartupTemplateCheckCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	if _, err := office.Setup(dir); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, office.TemplatesVersionPath), []byte("stale\n"), 0o644)
	cfg, _ := config.Load(filepath.Join(dir, office.ConfigPath))
	cfg.Startup.CheckSelfUpdate = false
	cfg.Startup.CheckTemplates = false
	cfg.Startup.CheckSuperpowers = false
	cmd, out, stderr := startupCommand("yes\n")
	restarted, err := runStartupChecks(cmd, dir, cfg, "1.0.0", true)
	if err != nil || restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
	if out.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("disabled checks wrote output: %q %q", out.String(), stderr.String())
	}
}

func TestStartupDoesNotOfferTemplateUpdateWhenPreviewFails(t *testing.T) {
	dir := t.TempDir()
	if _, err := office.Setup(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, office.TemplatesVersionPath), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(dir, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Startup.CheckSelfUpdate = false
	cfg.Plugins.UpdateOnStart = false
	restoreStartupHooks(t)
	inputIsTerminal = func(io.Reader) bool { return true }
	templateUpdatePlan = func(string) ([]string, error) { return nil, context.DeadlineExceeded }
	called := false
	templateUpdate = func(string, func(string), func([]string) error) ([]string, error) { called = true; return nil, nil }
	cmd, out, stderr := startupCommand("yes\n")

	restarted, err := runStartupChecks(cmd, dir, cfg, "dev", true)
	if err != nil || restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
	if called || strings.Contains(out.String(), "Run 'omo setup --update'") {
		t.Fatalf("unpreviewed update was offered or run: called=%v output=%q", called, out.String())
	}
	if !strings.Contains(stderr.String(), "could not preview") {
		t.Fatalf("preview failure not reported: %q", stderr.String())
	}
}

func TestOfficeUpdateCommitEligibilityRequiresVisibleConfigInGitWorktree(t *testing.T) {
	if officeUpdateCommitEligible(t.TempDir()) {
		t.Fatal("non-repository office was eligible for an update commit")
	}
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	if !officeUpdateCommitEligible(repo) {
		t.Fatal("visible office config in Git worktree was not eligible")
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "exclude"), []byte("/.omo/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if officeUpdateCommitEligible(repo) {
		t.Fatal("ignored office config was eligible for an update commit")
	}
}

func TestCommitTemplateUpdateLeavesUnrelatedStagedChangesAlone(t *testing.T) {
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	runGitCommand(t, repo, "config", "user.name", "Test User")
	runGitCommand(t, repo, "config", "user.email", "test@example.com")
	runGitCommand(t, repo, "config", "commit.gpgSign", "false")
	if err := os.MkdirAll(filepath.Join(repo, ".omo", "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(repo, ".omo", "prompts", "common.md")
	if err := os.WriteFile(tracked, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, repo, "add", ".omo/prompts/common.md")
	runGitCommand(t, repo, "commit", "-m", "test: baseline")
	if err := os.WriteFile(tracked, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "unrelated.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, repo, "add", "unrelated.txt")
	if committed, err := commitTemplateUpdate(repo, []string{".omo/prompts/common.md"}); err != nil || !committed {
		t.Fatal(err)
	}
	changed := strings.TrimSpace(runGitOutput(t, repo, "show", "--pretty=", "--name-only", "HEAD"))
	if changed != ".omo/prompts/common.md" {
		t.Fatalf("update commit included unrelated paths: %q", changed)
	}
	staged := strings.TrimSpace(runGitOutput(t, repo, "diff", "--cached", "--name-only"))
	if staged != "unrelated.txt" {
		t.Fatalf("unrelated staged change was disturbed: %q", staged)
	}
}

func TestCommitTemplateUpdateSkipsDeletedUntrackedAndIgnoredPlanPaths(t *testing.T) {
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	runGitCommand(t, repo, "config", "user.name", "Test User")
	runGitCommand(t, repo, "config", "user.email", "test@example.com")
	runGitCommand(t, repo, "config", "commit.gpgSign", "false")
	if err := os.MkdirAll(filepath.Join(repo, ".omo", "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(repo, ".omo", "prompts", "common.md")
	if err := os.WriteFile(tracked, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, repo, "add", ".omo/prompts/common.md")
	runGitCommand(t, repo, "commit", "-m", "test: baseline")
	if err := os.WriteFile(tracked, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".omo/prompts/ignored.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".omo", "prompts", "ignored.md"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := []string{".omo/prompts/common.md", ".omo/prompts/obsolete.md", ".omo/prompts/ignored.md"}
	if committed, err := commitTemplateUpdate(repo, plan); err != nil || !committed {
		t.Fatal(err)
	}
	changed := strings.TrimSpace(runGitOutput(t, repo, "show", "--pretty=", "--name-only", "HEAD"))
	if changed != ".omo/prompts/common.md" {
		t.Fatalf("filtered update commit paths = %q", changed)
	}
}

func TestStartupCommitUsesAuthoritativeUpdatePlan(t *testing.T) {
	t.Setenv("OMO_HOME", t.TempDir())
	repo := t.TempDir()
	runGitCommand(t, repo, "init")
	runGitCommand(t, repo, "config", "user.name", "Test User")
	runGitCommand(t, repo, "config", "user.email", "test@example.com")
	runGitCommand(t, repo, "config", "commit.gpgSign", "false")
	if _, err := office.Setup(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := office.EnableGitIntegration(repo); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, office.TemplatesVersionPath), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(filepath.Join(repo, office.ConfigPath))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Startup.CheckSelfUpdate = false
	cfg.Startup.CheckSuperpowers = false
	cfg.Plugins.UpdateOnStart = false
	restoreStartupHooks(t)
	inputIsTerminal = func(io.Reader) bool { return true }
	templateUpdatePlan = func(string) ([]string, error) { return []string{".omo/prompts/stale-preview.md"}, nil }
	templateUpdate = func(dir string, _ func(string), finalize func([]string) error) ([]string, error) {
		path := filepath.Join(dir, ".omo", "prompts", "authoritative.md")
		if err := os.WriteFile(path, []byte("updated\n"), 0o644); err != nil {
			return nil, err
		}
		return nil, finalize([]string{".omo/prompts/authoritative.md"})
	}
	currentExecutable = func() (string, error) { return "/bin/true", nil }
	launchRestart = func(string) error { return nil }
	cmd, _, _ := startupCommand("y\ny\n")
	restarted, err := runStartupChecks(cmd, repo, cfg, "dev", true)
	if err != nil || !restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
	changed := strings.TrimSpace(runGitOutput(t, repo, "show", "--pretty=", "--name-only", "HEAD"))
	if changed != ".omo/prompts/authoritative.md" {
		t.Fatalf("startup committed stale plan instead of authoritative plan: %q", changed)
	}
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func TestStartupRefreshesConfiguredPlugins(t *testing.T) {
	restoreStartupHooks(t)
	cfg := &config.Config{
		Startup: config.Startup{CheckTimeout: config.Duration(time.Second)},
		Plugins: config.Plugins{UpdateOnStart: true, Installed: map[string]config.Plugin{
			"nudge": {Source: "https://example.test/nudge.git", Enabled: true},
		}},
	}
	called := false
	pluginSyncAll = func(_ context.Context, _ string, _ config.Plugins, preview func(pluginmanager.Result)) ([]pluginmanager.Result, []error) {
		called = true
		preview(pluginmanager.Result{Name: "nudge", Revision: "1234567890abcdef", Changed: true})
		return []pluginmanager.Result{{Name: "nudge", Revision: "1234567890abcdef", Changed: true}}, nil
	}
	cmd, _, stderr := startupCommand("")
	if restarted, err := runStartupChecks(cmd, t.TempDir(), cfg, "dev", false); err != nil || restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
	if !called || !strings.Contains(stderr.String(), "updated plugin nudge to 1234567890ab") {
		t.Fatalf("plugin update called=%v output=%q", called, stderr.String())
	}
}

func TestStartupPreviewsPluginUpdateBeforeSync(t *testing.T) {
	restoreStartupHooks(t)
	cfg := &config.Config{
		Startup: config.Startup{CheckTimeout: config.Duration(time.Second)},
		Plugins: config.Plugins{UpdateOnStart: true, Installed: map[string]config.Plugin{
			"report": {Source: "https://example.test/report.git", Enabled: true},
		}},
	}
	cmd, _, stderr := startupCommand("")
	pluginSyncAll = func(_ context.Context, _ string, _ config.Plugins, preview func(pluginmanager.Result)) ([]pluginmanager.Result, []error) {
		preview(pluginmanager.Result{Name: "report", Previous: "111111111111", Revision: "222222222222", Changed: true})
		if !strings.Contains(stderr.String(), "will update plugin report from 111111111111 to 222222222222") {
			t.Fatalf("sync ran before preview: %q", stderr.String())
		}
		return []pluginmanager.Result{{Name: "report", Revision: "222222222222", Changed: true}}, nil
	}

	if restarted, err := runStartupChecks(cmd, t.TempDir(), cfg, "dev", false); err != nil || restarted {
		t.Fatalf("restarted=%v err=%v", restarted, err)
	}
}

func startupCommand(input string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{}
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	return cmd, out, stderr
}

func restoreStartupHooks(t *testing.T) {
	t.Helper()
	oldLatest, oldInstall := latestRelease, installRelease
	oldExecutable, oldRestart, oldTerminal := currentExecutable, launchRestart, inputIsTerminal
	oldTemplatePlan, oldTemplateUpdate := templateUpdatePlan, templateUpdate
	oldPluginSyncAll := pluginSyncAll
	t.Cleanup(func() {
		latestRelease, installRelease = oldLatest, oldInstall
		currentExecutable, launchRestart, inputIsTerminal = oldExecutable, oldRestart, oldTerminal
		templateUpdatePlan, templateUpdate = oldTemplatePlan, oldTemplateUpdate
		pluginSyncAll = oldPluginSyncAll
	})
}
