// Package pluginmanager installs and refreshes Git-backed office plugins.
package pluginmanager

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/config"
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
	names := make([]string, 0, len(settings.Installed))
	for name := range settings.Installed {
		names = append(names, name)
	}
	sort.Strings(names)
	var results []Result
	var errs []error
	for _, name := range names {
		result, err := Sync(ctx, officeDir, name, settings.Installed[name])
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		results = append(results, result)
	}
	return results, errs
}

// Sync clones or fast-forwards a managed repository and atomically refreshes
// its active plugin directory. Disabled plugins are still updated on disk.
func Sync(ctx context.Context, officeDir, name string, plugin config.Plugin) (Result, error) {
	if err := ValidateName(name); err != nil {
		return Result{}, err
	}
	if strings.HasPrefix(plugin.Source, "builtin:") {
		if plugin.Source != "builtin:nudge" || name != bundledplugins.NudgeName {
			return Result{}, fmt.Errorf("unknown bundled plugin %q", plugin.Source)
		}
		created, err := bundledplugins.EnsureNudge(officeDir)
		return Result{Name: name, Revision: "bundled", Changed: created}, err
	}
	source, err := NormalizeSource(plugin.Source)
	if err != nil {
		return Result{}, err
	}
	subpath, err := NormalizeSubpath(plugin.Subpath)
	if err != nil {
		return Result{}, err
	}
	root := filepath.Join(officeDir, rootDir)
	repos := filepath.Join(root, ".repos")
	if err := os.MkdirAll(repos, 0o755); err != nil {
		return Result{}, err
	}
	cache := filepath.Join(repos, name)
	before, _ := revision(ctx, cache)
	cacheInfo, cacheErr := os.Lstat(cache)
	if os.IsNotExist(cacheErr) {
		if err := runGit(ctx, officeDir, "clone", "--quiet", "--depth", "1", source, cache); err != nil {
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
		if err := runGit(ctx, cache, "pull", "--quiet", "--ff-only"); err != nil {
			return Result{}, fmt.Errorf("update %s: %w", source, err)
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
	if err := installTree(root, name, sourceDir); err != nil {
		return Result{}, err
	}
	return Result{Name: name, Revision: after, Changed: before == "" || before != after}, nil
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

func installTree(root, name, source string) error {
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
	defer os.RemoveAll(backup)
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
			_ = os.Rename(backup, target)
		}
		return err
	}
	return nil
}

func copyTree(source, target string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if rel == ".git" || strings.HasPrefix(filepath.ToSlash(rel), ".git/") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dest := filepath.Join(target, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not supported: %s", rel)
		}
		if info.IsDir() {
			return os.MkdirAll(dest, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file type: %s", rel)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			in.Close()
			return err
		}
		_, copyErr := io.Copy(out, in)
		in.Close()
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
