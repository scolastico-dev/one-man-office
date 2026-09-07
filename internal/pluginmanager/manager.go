// Package pluginmanager installs and refreshes Git-backed office plugins.
package pluginmanager

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/pluginfiles"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	bundledplugins "github.com/scolastico-dev/one-man-office/plugins"
)

const rootDir = ".omo/plugins"

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Result struct {
	Name     string
	Revision string
	Changed  bool
}

func NormalizeSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", fmt.Errorf("plugin repository URL is required")
	}
	if strings.Contains(source, "://") {
		parsed, err := url.Parse(source)
		if err != nil || parsed.Scheme == "" {
			return "", fmt.Errorf("invalid plugin repository URL %q", source)
		}
		switch parsed.Scheme {
		case "https", "http", "ssh", "git", "file":
		default:
			return "", fmt.Errorf("unsupported plugin repository URL scheme %q", parsed.Scheme)
		}
		if parsed.Scheme != "file" && parsed.Host == "" {
			return "", fmt.Errorf("plugin repository URL needs a host")
		}
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
		if !strings.HasSuffix(parsed.Path, ".git") {
			parsed.Path += ".git"
		}
		return parsed.String(), nil
	}
	if strings.Contains(source, "@") && strings.Contains(source, ":") {
		source = strings.TrimSuffix(source, "/")
		if !strings.HasSuffix(source, ".git") {
			source += ".git"
		}
		return source, nil
	}
	return "", fmt.Errorf("plugin source must be an http(s), ssh, git, file, or scp-style repository URL")
}

func ValidateName(name string) error {
	if !validName.MatchString(name) || name == ".repos" {
		return fmt.Errorf("plugin name %q must use letters, digits, dots, dashes, or underscores", name)
	}
	return nil
}

func NormalizeSubpath(subpath string) (string, error) {
	if subpath == "" || subpath == "." {
		return "", nil
	}
	clean := filepath.Clean(subpath)
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(filepath.ToSlash(clean), "../") {
		return "", fmt.Errorf("plugin subpath must stay inside the repository")
	}
	return filepath.ToSlash(clean), nil
}

// NormalizeBranch validates a literal Git branch name. It remains an argument
// to Git commands and is never interpreted by a shell.
func NormalizeBranch(branch string) (string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", nil
	}
	if strings.HasPrefix(branch, "-") || strings.HasPrefix(branch, "refs/") || branch == "HEAD" {
		return "", fmt.Errorf("invalid plugin branch %q", branch)
	}
	cmd := exec.Command("git", "check-ref-format", "refs/heads/"+branch)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("invalid plugin branch %q: %s", branch, strings.TrimSpace(string(output)))
	}
	return branch, nil
}

func SuggestedName(source, subpath string) string {
	candidate := subpath
	if candidate == "" {
		if parsed, err := url.Parse(source); err == nil {
			candidate = parsed.Path
		} else {
			candidate = source
		}
	}
	candidate = strings.TrimSuffix(strings.TrimSuffix(candidate, "/"), ".git")
	return filepath.Base(filepath.FromSlash(candidate))
}

func SyncAll(ctx context.Context, officeDir string, settings config.Plugins) ([]Result, []error) {
	return syncAll(ctx, settings, func(name string, plugin config.Plugin) (Result, error) { return Sync(ctx, officeDir, name, plugin) })
}

// SyncAllAt updates managed Git plugins at an explicit installation root.
// Global homes have no office layout or automatically installed bundled plugin.
func SyncAllAt(ctx context.Context, root, configPath string, settings config.Plugins) ([]Result, []error) {
	lock, err := pluginfiles.Lock(ctx, root)
	if err != nil {
		return nil, []error{fmt.Errorf("lock global plugins: %w", err)}
	}
	defer lock.Close()
	return syncAll(ctx, settings, func(name string, plugin config.Plugin) (Result, error) {
		return syncAt(ctx, root, configPath, name, plugin)
	})
}

func syncAll(ctx context.Context, settings config.Plugins, syncPlugin func(string, config.Plugin) (Result, error)) ([]Result, []error) {
	names := make([]string, 0, len(settings.Installed))
	for name := range settings.Installed {
		names = append(names, name)
	}
	sort.Strings(names)
	var results []Result
	var errs []error
	for _, name := range names {
		result, err := syncPlugin(name, settings.Installed[name])
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		results = append(results, result)
	}
	return results, errs
}

// Sync clones or fast-forwards a managed repository and atomically refreshes
// its active plugin directory and missing manifest config defaults. Disabled
// plugins are still updated on disk. An office config file must already exist.
func Sync(ctx context.Context, officeDir, name string, plugin config.Plugin) (Result, error) {
	if err := ValidateName(name); err != nil {
		return Result{}, err
	}
	if strings.HasPrefix(plugin.Source, "builtin:") {
		var ensure func(string) (bool, error)
		switch {
		case plugin.Source == "builtin:nudge" && name == bundledplugins.NudgeName:
			ensure = bundledplugins.EnsureNudge
		case plugin.Source == "builtin:tools" && name == bundledplugins.ToolsName:
			ensure = bundledplugins.EnsureTools
		default:
			return Result{}, fmt.Errorf("unknown bundled plugin %q", plugin.Source)
		}
		created, err := ensure(officeDir)
		if err != nil {
			return Result{}, err
		}
		dir := filepath.Join(officeDir, rootDir, name)
		manifest, err := plugins.ReadManifest(dir)
		if err == nil {
			var commit func() error
			commit, err = prepareConfig(filepath.Join(officeDir, ".omo", "omo.yaml"), name, plugin, manifest.DefaultConfig)
			if err == nil {
				err = commit()
			}
		}
		if err != nil && created {
			err = errors.Join(err, os.RemoveAll(dir))
		}
		return Result{Name: name, Revision: "bundled", Changed: created}, err
	}
	return syncAt(ctx, filepath.Join(officeDir, rootDir), filepath.Join(officeDir, ".omo", "omo.yaml"), name, plugin)
}

func syncAt(ctx context.Context, root, configPath, name string, plugin config.Plugin) (Result, error) {
	if err := ValidateName(name); err != nil {
		return Result{}, err
	}
	source, err := NormalizeSource(plugin.Source)
	if err != nil {
		return Result{}, err
	}
	subpath, err := NormalizeSubpath(plugin.Subpath)
	if err != nil {
		return Result{}, err
	}
	branch, err := NormalizeBranch(plugin.Branch)
	if err != nil {
		return Result{}, err
	}
	repos := filepath.Join(root, ".repos")
	if err := os.MkdirAll(repos, 0o755); err != nil {
		return Result{}, err
	}
	cache := filepath.Join(repos, name)
	before, _ := revision(ctx, cache)
	cacheInfo, cacheErr := os.Lstat(cache)
	if os.IsNotExist(cacheErr) {
		args := []string{"clone", "--quiet", "--depth", "1"}
		if branch != "" {
			args = append(args, "--branch", branch, "--single-branch")
		}
		args = append(args, source, cache)
		if err := runGit(ctx, root, args...); err != nil {
			return Result{}, fmt.Errorf("clone %s: %w", source, err)
		}
	} else if cacheErr != nil {
		return Result{}, cacheErr
	} else {
		if cacheInfo.Mode()&os.ModeSymlink != 0 {
			return Result{}, fmt.Errorf("managed checkout must not be a symbolic link: %s", cache)
		}
		if _, err := os.Stat(filepath.Join(cache, ".git")); err != nil {
			return Result{}, fmt.Errorf("%s is not a managed Git checkout", cache)
		}
		if err := runGit(ctx, cache, "remote", "set-url", "origin", source); err != nil {
			return Result{}, err
		}
		if branch == "" {
			branch, err = remoteDefaultBranch(ctx, cache)
			if err != nil {
				return Result{}, fmt.Errorf("resolve default branch for %s: %w", source, err)
			}
		}
		if err := checkoutRemoteBranch(ctx, cache, source, branch); err != nil {
			return Result{}, err
		}
	}
	after, err := revision(ctx, cache)
	if err != nil {
		return Result{}, err
	}
	sourceDir := cache
	if subpath != "" {
		sourceDir = filepath.Join(cache, filepath.FromSlash(subpath))
	}
	manifest, err := plugins.ReadManifest(sourceDir)
	if err != nil {
		return Result{}, fmt.Errorf("plugin %q: %w", name, err)
	}
	commit, err := prepareConfig(configPath, name, plugin, manifest.DefaultConfig)
	if err != nil {
		return Result{}, err
	}
	if err := installTree(root, name, sourceDir, commit); err != nil {
		return Result{}, err
	}
	return Result{Name: name, Revision: after, Changed: before == "" || before != after}, nil
}

func remoteDefaultBranch(ctx context.Context, cache string) (string, error) {
	out, err := gitOutput(ctx, cache, "ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" {
			branch := strings.TrimPrefix(fields[1], "refs/heads/")
			if branch == fields[1] {
				break
			}
			return NormalizeBranch(branch)
		}
	}
	return "", fmt.Errorf("remote HEAD does not identify a branch")
}

func checkoutRemoteBranch(ctx context.Context, cache, source, branch string) error {
	if err := runGit(ctx, cache, "fetch", "--quiet", "--depth", "1", "origin", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("fetch branch %s from %s: %w", branch, source, err)
	}
	if err := runGit(ctx, cache, "checkout", "--quiet", "-B", branch, "FETCH_HEAD"); err != nil {
		return fmt.Errorf("checkout branch %s: %w", branch, err)
	}
	return nil
}

func revision(ctx context.Context, dir string) (string, error) {
	out, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

func runGit(ctx context.Context, dir string, args ...string) error {
	_, err := gitOutput(ctx, dir, args...)
	return err
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func installTree(root, name, source string, commitConfig func() error) error {
	info, err := os.Stat(filepath.Join(source, "plugin.json"))
	if err != nil {
		return fmt.Errorf("plugin %q: plugin.json: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("plugin %q: plugin.json is not a regular file", name)
	}
	stage, err := os.MkdirTemp(root, ".stage-"+name+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := copyTree(source, stage); err != nil {
		return fmt.Errorf("stage plugin %q: %w", name, err)
	}
	target := filepath.Join(root, name)
	backup, err := os.MkdirTemp(root, ".backup-"+name+"-")
	if err != nil {
		return err
	}
	if err := os.Remove(backup); err != nil {
		return err
	}
	keepBackup := false
	defer func() {
		if !keepBackup {
			_ = os.RemoveAll(backup)
		}
	}()
	hadTarget := false
	if _, err := os.Lstat(target); err == nil {
		hadTarget = true
		if err := os.Rename(target, backup); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(stage, target); err != nil {
		if hadTarget {
			restoreErr := os.Rename(backup, target)
			if restoreErr != nil {
				keepBackup = true
			}
			return errors.Join(err, restoreErr)
		}
		return err
	}
	if err := commitConfig(); err != nil {
		rollbackErr := os.RemoveAll(target)
		if hadTarget && rollbackErr == nil {
			rollbackErr = os.Rename(backup, target)
		}
		if rollbackErr != nil {
			keepBackup = true
		}
		return errors.Join(err, rollbackErr)
	}
	return nil
}

func copyTree(source, target string) error {
	return pluginfiles.CopyTree(source, target)
}
