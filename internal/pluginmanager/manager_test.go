package pluginmanager

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/filelock"
	"github.com/scolastico-dev/one-man-office/internal/pluginfiles"
	internalplugins "github.com/scolastico-dev/one-man-office/internal/plugins"
	"gopkg.in/yaml.v3"
)

func TestGlobalUpdateWaitsForSharedRootLock(t *testing.T) {
	_, remote := pluginRemote(t, "global")
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("plugins:\n  installed: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	held, err := filelock.Acquire(context.Background(), filepath.Join(root, ".update.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	settings := config.Plugins{Installed: map[string]config.Plugin{"nudge": {Source: remote, Subpath: "examples/nudge", Enabled: true}}}
	results, errs := SyncAllAt(ctx, root, configPath, settings)
	if len(results) != 0 || len(errs) != 1 || !errors.Is(errs[0], context.DeadlineExceeded) {
		t.Fatalf("updater bypassed lock: %v %v", results, errs)
	}
	if _, err := os.Stat(filepath.Join(root, ".repos")); !os.IsNotExist(err) {
		t.Fatalf("updater changed shared checkout while locked: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	results, errs = SyncAllAt(context.Background(), root, configPath, settings)
	if len(errs) != 0 || len(results) != 1 {
		t.Fatalf("update after unlock: %v %v", results, errs)
	}
}

func TestSinglePluginUpdateWaitsForOfficeRootLock(t *testing.T) {
	_, remote := pluginRemote(t, "locked")
	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	root := filepath.Join(office, rootDir)
	held, err := pluginfiles.Lock(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = Sync(ctx, office, "nudge", config.Plugin{Source: remote, Subpath: "examples/nudge", Enabled: true})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("single plugin update bypassed lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".repos")); !os.IsNotExist(err) {
		t.Fatalf("single plugin update changed checkout while locked: %v", err)
	}
}

func TestNormalizeSourceAddsDotGit(t *testing.T) {
	tests := map[string]string{
		"https://github.com/acme/plugins":      "https://github.com/acme/plugins.git",
		"https://gitea.example/acme/plugins/":  "https://gitea.example/acme/plugins.git",
		"ssh://git@git.example/acme/plugins":   "ssh://git@git.example/acme/plugins.git",
		"git@git.example:acme/plugins":         "git@git.example:acme/plugins.git",
		"https://github.com/acme/plugins.git":  "https://github.com/acme/plugins.git",
		"https://gitea.example/acme/p.git?q=1": "https://gitea.example/acme/p.git?q=1",
	}
	for input, want := range tests {
		got, err := NormalizeSource(input)
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeSource(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeBranchRejectsInvalidNames(t *testing.T) {
	for _, branch := range []string{"bad name", "../main", "-option", "HEAD", "refs/heads/main"} {
		if _, err := NormalizeBranch(branch); err == nil {
			t.Fatalf("NormalizeBranch(%q) succeeded", branch)
		}
	}
	if got, err := NormalizeBranch(" feature/preview "); err != nil || got != "feature/preview" {
		t.Fatalf("NormalizeBranch() = %q, %v", got, err)
	}
}

func TestSyncInstallsAndUpdatesRepositorySubpath(t *testing.T) {
	work, remoteURL := pluginRemote(t, "one")
	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: remoteURL, Subpath: "examples/nudge", Enabled: true}

	first, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed {
		t.Fatal("initial clone must be reported as changed")
	}
	active := filepath.Join(office, rootDir, "nudge")
	assertFile(t, filepath.Join(active, "hook.lua"), "one")
	if _, err := os.Stat(filepath.Join(active, "unrelated.txt")); !os.IsNotExist(err) {
		t.Fatal("repository files outside the selected subpath were activated")
	}

	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: update plugin")
	remotePath, _ := url.Parse(remoteURL)
	git(t, work, "push", remotePath.Path, "HEAD:main")

	second, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Changed {
		t.Fatal("new upstream commit was not detected")
	}
	assertFile(t, filepath.Join(active, "hook.lua"), "two")
	third, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if third.Changed {
		t.Fatal("unchanged repository was reported as updated")
	}
}

func TestSyncRejectsInvalidDependencyManifestBeforeActivation(t *testing.T) {
	work, remoteURL := pluginRemote(t, "one")
	manifest := filepath.Join(work, "examples", "nudge", "plugin.json")
	if err := os.WriteFile(manifest, []byte(`{"name":"nudge","requires":[{"name":"..","source":"https://example.test/bad.git"}],"hooks":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "test: invalid dependency")
	remotePath, _ := url.Parse(remoteURL)
	git(t, work, "push", remotePath.Path, "HEAD:main")
	office, configPath := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: remoteURL, Subpath: "examples/nudge", Enabled: true}

	if _, err := Sync(context.Background(), office, "nudge", entry); err == nil {
		t.Fatal("Sync() activated a manifest with invalid dependencies")
	}
	if _, err := os.Stat(filepath.Join(office, rootDir, "nudge")); !os.IsNotExist(err) {
		t.Fatalf("invalid plugin was activated: %v", err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "nudge:") {
		t.Fatalf("invalid plugin was added to config:\n%s", raw)
	}
}

func TestSyncTracksConfiguredBranchAndCanSwitchBranches(t *testing.T) {
	work, remoteURL := pluginRemote(t, "main")
	git(t, work, "checkout", "-b", "preview")
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte("preview-one"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: add preview plugin")
	remotePath, _ := url.Parse(remoteURL)
	git(t, work, "push", remotePath.Path, "HEAD:preview")
	git(t, work, "checkout", "main")

	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: remoteURL, Subpath: "examples/nudge", Branch: "preview", Enabled: true}
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(office, rootDir, "nudge", "hook.lua")
	assertFile(t, active, "preview-one")

	git(t, work, "checkout", "preview")
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte("preview-two"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: update preview plugin")
	git(t, work, "push", remotePath.Path, "HEAD:preview")
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	assertFile(t, active, "preview-two")

	entry.Branch = "main"
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	assertFile(t, active, "main")

	entry.Branch = "preview"
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	assertFile(t, active, "preview-two")
	entry.Branch = ""
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	assertFile(t, active, "main")
}

func TestPlanDetectsRemoteUpdateWithoutChangingCheckoutOrActivePlugin(t *testing.T) {
	work, remoteURL := pluginRemote(t, "one")
	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: remoteURL, Subpath: "examples/nudge", Enabled: true}
	first, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: update plugin")
	remotePath, _ := url.Parse(remoteURL)
	git(t, work, "push", remotePath.Path, "HEAD:main")

	plan, err := Plan(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Changed || plan.Previous != first.Revision || plan.Revision == first.Revision {
		t.Fatalf("plan = %+v, first = %+v", plan, first)
	}
	assertFile(t, filepath.Join(office, rootDir, "nudge", "hook.lua"), "one")
	checkoutRevision, err := revision(context.Background(), filepath.Join(office, rootDir, ".repos", "nudge"))
	if err != nil || checkoutRevision != first.Revision {
		t.Fatalf("planning changed checkout to %q: %v", checkoutRevision, err)
	}
}

func TestPlanRejectsBundledPluginAtRegularFile(t *testing.T) {
	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	path := filepath.Join(office, rootDir, "nudge")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("collision"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(context.Background(), office, "nudge", config.Plugin{Source: "builtin:nudge", Enabled: true}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("bundled regular-file plan error = %v", err)
	}
}

func TestSyncAllPreviewRunsBeforePluginWrite(t *testing.T) {
	work, remoteURL := pluginRemote(t, "one")
	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: remoteURL, Subpath: "examples/nudge", Enabled: true}
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: update plugin")
	remotePath, _ := url.Parse(remoteURL)
	git(t, work, "push", remotePath.Path, "HEAD:main")

	previewed := false
	settings := config.Plugins{Installed: map[string]config.Plugin{"nudge": entry}}
	results, errs := SyncAllWithPreview(context.Background(), office, settings, func(plan Result) {
		previewed = true
		if !plan.Changed {
			t.Fatalf("preview = %+v", plan)
		}
		assertFile(t, filepath.Join(office, rootDir, "nudge", "hook.lua"), "one")
	})
	if len(errs) != 0 || len(results) != 1 || !previewed {
		t.Fatalf("sync = %+v, errs=%v, previewed=%v", results, errs, previewed)
	}
	assertFile(t, filepath.Join(office, rootDir, "nudge", "hook.lua"), "two")
}

func TestSyncAllDoesNotWriteWhenAnyPreviewFails(t *testing.T) {
	_, remoteURL := pluginRemote(t, "good")
	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	settings := config.Plugins{Installed: map[string]config.Plugin{
		"good": {Source: remoteURL, Subpath: "examples/nudge", Enabled: true},
		"zbad": {Source: "not-a-plugin-source", Enabled: true},
	}}
	results, errs := SyncAllWithPreview(context.Background(), office, settings, nil)
	if len(results) != 0 || len(errs) != 1 {
		t.Fatalf("sync results = %+v, errors = %v", results, errs)
	}
	if _, err := os.Stat(filepath.Join(office, rootDir, ".repos", "good")); !os.IsNotExist(err) {
		t.Fatalf("valid plugin was written before the full preview completed: %v", err)
	}
}

func TestSyncAllPreflightsEveryManifestBeforeWriting(t *testing.T) {
	_, goodRemote := pluginRemote(t, "good")
	badWork, badRemote := pluginRemote(t, "bad")
	if err := os.WriteFile(filepath.Join(badWork, "examples", "nudge", "plugin.json"), []byte(`{"name":`), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, badWork, "add", ".")
	git(t, badWork, "commit", "-m", "break manifest")
	badPath, _ := url.Parse(badRemote)
	git(t, badWork, "push", badPath.Path, "HEAD:main")

	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	settings := config.Plugins{Installed: map[string]config.Plugin{
		"a-good": {Source: goodRemote, Subpath: "examples/nudge", Enabled: true},
		"z-bad":  {Source: badRemote, Subpath: "examples/nudge", Enabled: true},
	}}
	results, errs := SyncAllWithPreview(context.Background(), office, settings, nil)
	if len(results) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "preflight") {
		t.Fatalf("sync results = %+v, errors = %v", results, errs)
	}
	if _, err := os.Stat(filepath.Join(office, rootDir, ".repos", "a-good")); !os.IsNotExist(err) {
		t.Fatalf("valid plugin was written before manifest preflight completed: %v", err)
	}
}

func TestSyncAllPreflightsActivationTreesBeforeWriting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git symlink fixture requires Unix symlink semantics")
	}
	_, goodRemote := pluginRemote(t, "good")
	badWork, badRemote := pluginRemote(t, "bad")
	if err := os.Symlink("hook.lua", filepath.Join(badWork, "examples", "nudge", "linked.lua")); err != nil {
		t.Fatal(err)
	}
	git(t, badWork, "add", ".")
	git(t, badWork, "commit", "-m", "add unsupported symlink")
	badPath, _ := url.Parse(badRemote)
	git(t, badWork, "push", badPath.Path, "HEAD:main")

	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	settings := config.Plugins{Installed: map[string]config.Plugin{
		"a-good": {Source: goodRemote, Subpath: "examples/nudge", Enabled: true},
		"z-bad":  {Source: badRemote, Subpath: "examples/nudge", Enabled: true},
	}}
	results, errs := SyncAllWithPreview(context.Background(), office, settings, nil)
	if len(results) != 0 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "symbolic links") {
		t.Fatalf("sync results = %+v, errors = %v", results, errs)
	}
	if _, err := os.Stat(filepath.Join(office, rootDir, ".repos", "a-good")); !os.IsNotExist(err) {
		t.Fatalf("valid plugin was written before activation preflight completed: %v", err)
	}
}

func TestPlannedSyncInstallsImmutableRevision(t *testing.T) {
	work, remoteURL := pluginRemote(t, "planned")
	office, configPath := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: remoteURL, Subpath: "examples/nudge", Enabled: true}
	plan, err := Plan(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte("advanced"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "advance after plan")
	remotePath, _ := url.Parse(remoteURL)
	git(t, work, "push", remotePath.Path, "HEAD:main")

	result, err := syncAtRevision(context.Background(), filepath.Join(office, rootDir), configPath, "nudge", entry, &plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != plan.Revision {
		t.Fatalf("planned revision %q, installed %q", plan.Revision, result.Revision)
	}
	assertFile(t, filepath.Join(office, rootDir, "nudge", "hook.lua"), "planned")
}

func TestSyncAllPreviewFollowsChangedRemoteDefaultBranch(t *testing.T) {
	work, remoteURL := pluginRemote(t, "main")
	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: remoteURL, Subpath: "examples/nudge", Enabled: true}
	if _, err := Sync(context.Background(), office, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	git(t, work, "checkout", "-b", "preview")
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte("preview"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: preview branch")
	remotePath, _ := url.Parse(remoteURL)
	git(t, work, "push", remotePath.Path, "HEAD:preview")
	git(t, remotePath.Path, "symbolic-ref", "HEAD", "refs/heads/preview")

	var preview Result
	results, errs := SyncAllWithPreview(context.Background(), office, config.Plugins{Installed: map[string]config.Plugin{"nudge": entry}}, func(plan Result) {
		preview = plan
	})
	if len(errs) != 0 || len(results) != 1 {
		t.Fatalf("sync results = %+v, errors = %v", results, errs)
	}
	if !preview.Changed || preview.Revision != results[0].Revision {
		t.Fatalf("default-branch preview = %+v, sync = %+v", preview, results[0])
	}
	assertFile(t, filepath.Join(office, rootDir, "nudge", "hook.lua"), "preview")
}

func TestPlanUsesConfiguredBranchForInstallAndSwitch(t *testing.T) {
	work, remoteURL := pluginRemote(t, "main")
	git(t, work, "checkout", "-b", "preview")
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte("preview"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: preview branch")
	remotePath, _ := url.Parse(remoteURL)
	git(t, work, "push", remotePath.Path, "HEAD:preview")

	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: remoteURL, Subpath: "examples/nudge", Branch: "preview", Enabled: true}
	installPlan, err := Plan(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if installPlan.Revision != installed.Revision {
		t.Fatalf("install plan revision %q != sync revision %q", installPlan.Revision, installed.Revision)
	}

	entry.Branch = "main"
	switchPlan, err := Plan(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	switched, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil {
		t.Fatal(err)
	}
	if !switchPlan.Changed || switchPlan.Revision != switched.Revision {
		t.Fatalf("switch plan = %+v, sync = %+v", switchPlan, switched)
	}
}

func TestSyncAllAtUsesGlobalPluginRoot(t *testing.T) {
	_, remote := pluginRemote(t, "global")
	home := t.TempDir()
	root := filepath.Join(home, "plugins")
	configPath := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(configPath, []byte("plugins:\n  installed: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	settings := config.Plugins{Installed: map[string]config.Plugin{"nudge": {Source: remote, Subpath: "examples/nudge", Enabled: true}}}
	results, errs := SyncAllAt(context.Background(), root, configPath, settings)
	if len(errs) != 0 || len(results) != 1 || !results[0].Changed {
		t.Fatalf("sync=%+v, %v", results, errs)
	}
	assertFile(t, filepath.Join(root, "nudge", "hook.lua"), "global")
	if _, err := os.Stat(filepath.Join(root, ".repos", "nudge", ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".omo")); !os.IsNotExist(err) {
		t.Fatalf("office layout leaked: %v", err)
	}
}

func TestSyncAtAllowsGlobalFilebrowserBuiltin(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("plugins:\n  installed: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	entry := config.Plugin{Source: "builtin:filebrowser", Enabled: true}
	result, err := SyncAt(context.Background(), root, configPath, "filebrowser", entry)
	if err != nil || !result.Changed || result.Revision != "bundled" {
		t.Fatalf("global filebrowser sync = %+v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "filebrowser", "plugin.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSyncAtRejectsOfficeScopedOrUnknownGlobalBuiltins(t *testing.T) {
	for _, source := range []string{"builtin:nudge", "builtin:tools", "builtin:unknown"} {
		t.Run(source, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(configPath, []byte("plugins:\n  installed: {}\n"), 0o640); err != nil {
				t.Fatal(err)
			}
			_, err := SyncAt(context.Background(), root, configPath, strings.TrimPrefix(source, "builtin:"), config.Plugin{Source: source, Enabled: true})
			if err == nil || !strings.Contains(err.Error(), "bundled plugin") {
				t.Fatalf("source %s error = %v", source, err)
			}
		})
	}
}

func TestGlobalFilebrowserSyncMergesDefaultsWithoutReplacingEdits(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("# global\nplugins:\n  installed: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	entry := config.Plugin{Source: "builtin:filebrowser", Enabled: true}
	if _, err := SyncAt(context.Background(), root, configPath, "filebrowser", entry); err != nil {
		t.Fatal(err)
	}
	custom := []byte("// customized\n")
	asset := filepath.Join(root, "filebrowser", "web", "main.js")
	if err := os.WriteFile(asset, custom, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncAt(context.Background(), root, configPath, "filebrowser", entry); err != nil {
		t.Fatal(err)
	}
	assertFile(t, asset, string(custom))
	configured := readPluginConfigNamed(t, configPath, "filebrowser")
	for _, key := range []string{"download_warn_bytes", "upload_warn_bytes", "upload_max_bytes"} {
		if _, ok := configured.Config[key]; !ok {
			t.Fatalf("missing global default %q in %#v", key, configured.Config)
		}
	}
}

func TestGlobalFilebrowserLoadsFromSharedRuntimeSnapshot(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("plugins:\n  installed: {}\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	entry := config.Plugin{Source: "builtin:filebrowser", Enabled: true}
	if _, err := SyncAt(context.Background(), root, configPath, "filebrowser", entry); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(t.TempDir(), "plugins.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	manager, err := internalplugins.LoadSources(t.TempDir(), database, internalplugins.Source{
		Root: root, Shared: true,
		Configured: map[string]internalplugins.Settings{"filebrowser": {Enabled: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	extensions := manager.CompanyExtensions()
	if len(extensions) != 1 || extensions[0].Plugin != "filebrowser" || extensions[0].Javascript != "web/main.js" {
		t.Fatalf("global filebrowser extensions = %+v", extensions)
	}
	if len(extensions[0].Files) != 4 || extensions[0].Files[0] != "web/commands.js" || extensions[0].Files[1] != "web/helpers.js" || extensions[0].Files[2] != "web/main.js" || extensions[0].Files[3] != "web/style.css" {
		t.Fatalf("global filebrowser files = %v", extensions[0].Files)
	}
	active := filepath.Join(root, "filebrowser", "web", "main.js")
	original, err := os.ReadFile(active)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active, []byte("// changed after load\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotPath, ok := manager.CompanyFile("filebrowser", "web/main.js")
	if !ok || snapshotPath == active {
		t.Fatalf("company file did not use private snapshot: %q active=%q", snapshotPath, active)
	}
	assertFile(t, snapshotPath, string(original))
}

func TestConfigEditsPreservePluginWhileToggling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.yaml")
	if err := os.WriteFile(path, []byte("# office\nrepos:\n  api: /tmp/api\n\nplugins:\n  update_on_start: true\n  installed: {}\n\nnotifications:\n  input_debounce: 30s\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	entry := config.Plugin{
		Source: "https://example.test/acme/nudge.git", Subpath: "plugins/nudge", Branch: "stable", Enabled: true,
		Config: map[string]any{"check_interval": "5m", "nested": map[string]any{"mode": "careful"}},
	}
	if err := UpsertConfig(path, "nudge", entry); err != nil {
		t.Fatal(err)
	}
	if err := SetEnabled(path, "nudge", false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Plugins config.Plugins `yaml:"plugins"`
	}
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	got, ok := decoded.Plugins.Installed["nudge"]
	if !ok || got.Source != entry.Source || got.Subpath != entry.Subpath || got.Branch != entry.Branch || got.Enabled ||
		got.Config["check_interval"] != "5m" || got.Config["nested"].(map[string]any)["mode"] != "careful" {
		t.Fatalf("plugin was deleted or changed while disabling: %+v", got)
	}
	if !strings.Contains(string(raw), "# office") {
		t.Fatal("top-level config comment was not preserved")
	}
	if !strings.Contains(string(raw), "\n\nplugins:\n") || !strings.Contains(string(raw), "\n\nnotifications:\n") {
		t.Fatalf("blank lines between config blocks were not preserved:\n%s", raw)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("config mode changed: %v %v", info, err)
	}
}

func TestSyncEnsuresBundledNudgeWithoutOverwritingIt(t *testing.T) {
	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: "builtin:nudge", Enabled: true}
	first, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil || !first.Changed || first.Revision != "bundled" {
		t.Fatalf("first bundled sync = %+v, %v", first, err)
	}
	script := filepath.Join(office, rootDir, "nudge", "nudge.lua")
	if err := os.WriteFile(script, []byte("-- local edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := Sync(context.Background(), office, "nudge", entry)
	if err != nil || second.Changed {
		t.Fatalf("second bundled sync = %+v, %v", second, err)
	}
	assertFile(t, script, "-- local edit")
}

func TestPlanAndPreviewSyncSupportBundledTools(t *testing.T) {
	office, _ := configOffice(t, "plugins:\n  installed: {}\n")
	entry := config.Plugin{Source: "builtin:tools", Enabled: true}

	plan, err := Plan(context.Background(), office, "tools", entry)
	if err != nil || !plan.Changed || plan.Revision != "bundled" {
		t.Fatalf("initial tools plan = %+v, %v", plan, err)
	}

	var previewed []Result
	results, errs := SyncAllWithPreview(
		context.Background(),
		office,
		config.Plugins{Installed: map[string]config.Plugin{"tools": entry}},
		func(result Result) { previewed = append(previewed, result) },
	)
	if len(errs) != 0 || len(results) != 1 || len(previewed) != 1 {
		t.Fatalf("tools sync = %+v, errs=%v, previewed=%+v", results, errs, previewed)
	}
	if _, err := os.Stat(filepath.Join(office, rootDir, "tools", "plugin.json")); err != nil {
		t.Fatalf("stat bundled tools manifest: %v", err)
	}

	plan, err = Plan(context.Background(), office, "tools", entry)
	if err != nil || plan.Changed || plan.Previous != "bundled" {
		t.Fatalf("installed tools plan = %+v, %v", plan, err)
	}
}

func pluginRemote(t *testing.T, content string) (string, string) {
	t.Helper()
	base := t.TempDir()
	work := filepath.Join(base, "work")
	if err := os.MkdirAll(filepath.Join(work, "examples", "nudge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "plugin.json"), []byte("{\"name\":\"nudge\",\"hooks\":[]}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "examples", "nudge", "hook.lua"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "unrelated.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, "init", "-b", "main")
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "feat: initial plugin")
	remote := filepath.Join(base, "plugins.git")
	git(t, base, "clone", "--bare", work, remote)
	return work, (&url.URL{Scheme: "file", Path: remote}).String()
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=omo-test", "GIT_AUTHOR_EMAIL=omo@test",
		"GIT_COMMITTER_NAME=omo-test", "GIT_COMMITTER_EMAIL=omo@test")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != want {
		t.Fatalf("%s = %q, want %q", path, raw, want)
	}
}
