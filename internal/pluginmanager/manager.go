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
	Previous string
	Revision string
	Changed  bool
	branch   string
}

// Plan checks remote revisions without changing a checkout, active plugin, or
// configuration. The result mirrors the change Sync would report at that moment.
func Plan(ctx context.Context, officeDir, name string, plugin config.Plugin) (Result, error) {
	if strings.HasPrefix(plugin.Source, "builtin:") {
		if _, ok := bundledEnsure(name, plugin.Source); !ok {
			return Result{}, fmt.Errorf("unknown bundled plugin %q", plugin.Source)
		}
		info, err := os.Stat(filepath.Join(officeDir, rootDir, name))
		if err == nil && !info.IsDir() {
			return Result{}, fmt.Errorf("bundled plugin path is not a directory: %s", filepath.Join(officeDir, rootDir, name))
		}
		if err == nil {
			return Result{Name: name, Previous: "bundled", Revision: "bundled"}, nil
		}
		if !os.IsNotExist(err) {
			return Result{}, err
		}
		return Result{Name: name, Revision: "bundled", Changed: true}, nil
	}
	return planAt(ctx, filepath.Join(officeDir, rootDir), name, plugin)
}

func PlanAll(ctx context.Context, officeDir string, settings config.Plugins) ([]Result, []error) {
	return syncAll(ctx, settings, func(name string, plugin config.Plugin) (Result, error) {
		return Plan(ctx, officeDir, name, plugin)
	})
}

// PlanAllAt is the read-only counterpart of SyncAllAt for a global plugin root.
func PlanAllAt(ctx context.Context, root string, settings config.Plugins) ([]Result, []error) {
	return syncAll(ctx, settings, func(name string, plugin config.Plugin) (Result, error) {
		return planAt(ctx, root, name, plugin)
	})
}

func planAt(ctx context.Context, root, name string, plugin config.Plugin) (Result, error) {
	if err := ValidateName(name); err != nil {
		return Result{}, err
	}
	if strings.HasPrefix(plugin.Source, "builtin:") {
		if _, ok := bundledEnsureAt(root, name, plugin.Source); !ok {
			return Result{}, fmt.Errorf("unknown bundled plugin %q", plugin.Source)
		}
		target := filepath.Join(root, name)
		info, err := os.Stat(target)
		if err == nil && !info.IsDir() {
			return Result{}, fmt.Errorf("bundled plugin path is not a directory: %s", target)
		}
		if err == nil {
			return Result{Name: name, Previous: "bundled", Revision: "bundled"}, nil
		}
		if !os.IsNotExist(err) {
			return Result{}, err
		}
		return Result{Name: name, Revision: "bundled", Changed: true}, nil
	}
	source, err := NormalizeSource(plugin.Source)
	if err != nil {
		return Result{}, err
	}
	if _, err := NormalizeSubpath(plugin.Subpath); err != nil {
		return Result{}, err
	}
	branch, err := NormalizeBranch(plugin.Branch)
	if err != nil {
		return Result{}, err
	}
	cache := filepath.Join(root, ".repos", name)
	before := ""
	if info, err := os.Lstat(cache); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return Result{}, fmt.Errorf("managed checkout must not be a symbolic link: %s", cache)
		}
		if _, err := os.Stat(filepath.Join(cache, ".git")); err != nil {
			return Result{}, fmt.Errorf("%s is not a managed Git checkout", cache)
		}
		before, err = revision(ctx, cache)
		if err != nil {
			return Result{}, err
		}
	} else if !os.IsNotExist(err) {
		return Result{}, err
	}
	queryDir := root
	for {
		if _, err := os.Stat(queryDir); err == nil {
			break
		}
		parent := filepath.Dir(queryDir)
		if parent == queryDir {
			return Result{}, fmt.Errorf("no existing directory available to query plugin source")
		}
		queryDir = parent
	}
	resolvedBranch, after, err := remoteTarget(ctx, queryDir, source, branch)
	if err != nil {
		return Result{}, err
	}
	return Result{Name: name, Previous: before, Revision: after, Changed: before != after, branch: resolvedBranch}, nil
}

func remoteTarget(ctx context.Context, queryDir, source, branch string) (string, string, error) {
	if branch != "" {
		ref := "refs/heads/" + branch
		out, err := gitOutput(ctx, queryDir, "ls-remote", source, ref)
		if err != nil {
			return "", "", fmt.Errorf("check %s: %w", source, err)
		}
		fields := strings.Fields(out)
		if len(fields) < 2 {
			return "", "", fmt.Errorf("check %s: remote ref %s was not found", source, ref)
		}
		return branch, fields[0], nil
	}
	out, err := gitOutput(ctx, queryDir, "ls-remote", "--symref", source, "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("check %s: %w", source, err)
	}
	var resolved, revision string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && fields[2] == "HEAD" {
			resolved = strings.TrimPrefix(fields[1], "refs/heads/")
		}
		if len(fields) == 2 && fields[1] == "HEAD" {
			revision = fields[0]
		}
	}
	if resolved == "" || revision == "" {
		return "", "", fmt.Errorf("check %s: remote HEAD did not identify a branch and revision", source)
	}
	resolved, err = NormalizeBranch(resolved)
	if err != nil {
		return "", "", fmt.Errorf("check %s: %w", source, err)
	}
	return resolved, revision, nil
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
	return SyncAllWithPreview(ctx, officeDir, settings, nil)
}

// SyncAllWithPreview serializes a read-only revision plan with each subsequent
// update and calls preview before changing that plugin's checkout or active copy.
func SyncAllWithPreview(ctx context.Context, officeDir string, settings config.Plugins, preview func(Result)) ([]Result, []error) {
	root := filepath.Join(officeDir, rootDir)
	lock, err := pluginfiles.Lock(ctx, root)
	if err != nil {
		return nil, []error{fmt.Errorf("lock office plugins: %w", err)}
	}
	defer lock.Close()
	plans, errs := syncAll(ctx, settings, func(name string, plugin config.Plugin) (Result, error) {
		result, err := Plan(ctx, officeDir, name, plugin)
		return result, wrapPreviewError(err)
	})
	if len(errs) > 0 {
		return nil, errs
	}
	if errs = preflightPlans(ctx, root, filepath.Join(officeDir, ".omo", "omo.yaml"), settings, plans, false); len(errs) > 0 {
		return nil, errs
	}
	emitChangedPlans(plans, preview)
	planByName := indexPlans(plans)
	return syncAll(ctx, settings, func(name string, plugin config.Plugin) (Result, error) {
		if strings.HasPrefix(plugin.Source, "builtin:") {
			return syncUnlocked(ctx, officeDir, name, plugin)
		}
		plan := planByName[name]
		return syncAtRevision(ctx, root, filepath.Join(officeDir, ".omo", "omo.yaml"), name, plugin, &plan)
	})
}

// SyncAllAt updates managed Git plugins at an explicit installation root.
// Global homes have no office layout; global bundled plugins use this root
// directly and remain independently managed from office-scoped bundles.
func SyncAllAt(ctx context.Context, root, configPath string, settings config.Plugins) ([]Result, []error) {
	return SyncAllAtWithPreview(ctx, root, configPath, settings, nil)
}

// SyncAt updates one managed plugin at an explicit installation root. It is
// used by global plugin commands, whose config and cache do not live below an
// office directory.
func SyncAt(ctx context.Context, root, configPath, name string, plugin config.Plugin) (Result, error) {
	lock, err := pluginfiles.Lock(ctx, root)
	if err != nil {
		return Result{}, fmt.Errorf("lock plugin root: %w", err)
	}
	defer lock.Close()
	plan, err := planAt(ctx, root, name, plugin)
	if err != nil {
		return Result{}, err
	}
	if err := preflightPlan(ctx, root, configPath, name, plugin, plan, true); err != nil {
		return Result{}, err
	}
	if strings.HasPrefix(plugin.Source, "builtin:") {
		return syncBundledAt(root, configPath, name, plugin)
	}
	return syncAtRevision(ctx, root, configPath, name, plugin, &plan)
}

// SyncAllAtWithPreview is SyncAllWithPreview for an explicit/global root.
func SyncAllAtWithPreview(ctx context.Context, root, configPath string, settings config.Plugins, preview func(Result)) ([]Result, []error) {
	lock, err := pluginfiles.Lock(ctx, root)
	if err != nil {
		return nil, []error{fmt.Errorf("lock global plugins: %w", err)}
	}
	defer lock.Close()
	plans, errs := syncAll(ctx, settings, func(name string, plugin config.Plugin) (Result, error) {
		result, err := planAt(ctx, root, name, plugin)
		return result, wrapPreviewError(err)
	})
	if len(errs) > 0 {
		return nil, errs
	}
	if errs = preflightPlans(ctx, root, configPath, settings, plans, true); len(errs) > 0 {
		return nil, errs
	}
	emitChangedPlans(plans, preview)
	planByName := indexPlans(plans)
	return syncAll(ctx, settings, func(name string, plugin config.Plugin) (Result, error) {
		plan := planByName[name]
		if strings.HasPrefix(plugin.Source, "builtin:") {
			return syncBundledAt(root, configPath, name, plugin)
		}
		return syncAtRevision(ctx, root, configPath, name, plugin, &plan)
	})
}

func indexPlans(plans []Result) map[string]Result {
	indexed := make(map[string]Result, len(plans))
	for _, plan := range plans {
		indexed[plan.Name] = plan
	}
	return indexed
}

func wrapPreviewError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("preview: %w", err)
}

func emitChangedPlans(plans []Result, preview func(Result)) {
	if preview == nil {
		return
	}
	for _, plan := range plans {
		if plan.Changed {
			preview(plan)
		}
	}
}

func preflightPlans(ctx context.Context, root, configPath string, settings config.Plugins, plans []Result, globalRoot bool) []error {
	indexed := indexPlans(plans)
	_, errs := syncAll(ctx, settings, func(name string, plugin config.Plugin) (Result, error) {
		plan := indexed[name]
		if err := preflightPlan(ctx, root, configPath, name, plugin, plan, globalRoot); err != nil {
			return Result{}, fmt.Errorf("preflight: %w", err)
		}
		return plan, nil
	})
	return errs
}

func preflightPlan(ctx context.Context, root, configPath, name string, plugin config.Plugin, plan Result, globalRoot bool) error {
	var sourceDir string
	cleanup := func() {}
	if strings.HasPrefix(plugin.Source, "builtin:") {
		var ensure func(string) (bool, error)
		var ok bool
		if globalRoot {
			ensure, ok = bundledEnsureAt(root, name, plugin.Source)
		} else {
			ensure, ok = bundledEnsure(name, plugin.Source)
		}
		if !ok {
			return fmt.Errorf("unknown bundled plugin %q", plugin.Source)
		}
		sourceDir = filepath.Join(root, name)
		if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
			temp, err := os.MkdirTemp("", "omo-plugin-preflight-")
			if err != nil {
				return err
			}
			cleanup = func() { _ = os.RemoveAll(temp) }
			if _, err := ensure(temp); err != nil {
				cleanup()
				return err
			}
			if globalRoot {
				sourceDir = filepath.Join(temp, name)
			} else {
				sourceDir = filepath.Join(temp, rootDir, name)
			}
		} else if err != nil {
			return err
		}
	} else {
		temp, err := os.MkdirTemp("", "omo-plugin-preflight-")
		if err != nil {
			return err
		}
		cleanup = func() { _ = os.RemoveAll(temp) }
		checkout := filepath.Join(temp, "checkout")
		if err := os.Mkdir(checkout, 0o755); err != nil {
			cleanup()
			return err
		}
		source, err := NormalizeSource(plugin.Source)
		if err != nil {
			cleanup()
			return err
		}
		if err := runGit(ctx, checkout, "init", "--quiet"); err != nil {
			cleanup()
			return err
		}
		if err := runGit(ctx, checkout, "remote", "add", "origin", source); err != nil {
			cleanup()
			return err
		}
		if err := fetchPlannedRevision(ctx, checkout, source, plan); err != nil {
			cleanup()
			return err
		}
		sourceDir = checkout
		if subpath, err := NormalizeSubpath(plugin.Subpath); err != nil {
			cleanup()
			return err
		} else if subpath != "" {
			sourceDir = filepath.Join(checkout, filepath.FromSlash(subpath))
		}
	}
	defer cleanup()
	activation, err := os.MkdirTemp("", "omo-plugin-activation-preflight-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(activation)
	if err := copyTree(sourceDir, activation); err != nil {
		return fmt.Errorf("validate activation tree: %w", err)
	}
	sourceDir = activation
	manifest, err := plugins.ReadManifest(sourceDir)
	if err != nil {
		return fmt.Errorf("plugin %q: %w", name, err)
	}
	_, err = prepareConfig(configPath, name, plugin, manifest.DefaultConfig)
	return err
}

func fetchPlannedRevision(ctx context.Context, cache, source string, plan Result) error {
	if plan.Revision == "" || plan.branch == "" {
		return fmt.Errorf("plugin %q has an incomplete update plan", plan.Name)
	}
	if err := runGit(ctx, cache, "fetch", "--quiet", "--depth", "1", "origin", plan.Revision); err != nil {
		return fmt.Errorf("fetch planned revision %s from %s: %w", plan.Revision, source, err)
	}
	if err := runGit(ctx, cache, "checkout", "--quiet", "-B", plan.branch, "FETCH_HEAD"); err != nil {
		return fmt.Errorf("checkout planned revision %s: %w", plan.Revision, err)
	}
	got, err := revision(ctx, cache)
	if err != nil {
		return err
	}
	if got != plan.Revision {
		return fmt.Errorf("planned revision %s resolved to %s", plan.Revision, got)
	}
	return nil
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
	root := filepath.Join(officeDir, rootDir)
	lock, err := pluginfiles.Lock(ctx, root)
	if err != nil {
		return Result{}, fmt.Errorf("lock office plugins: %w", err)
	}
	defer lock.Close()
	return syncUnlocked(ctx, officeDir, name, plugin)
}

func syncUnlocked(ctx context.Context, officeDir, name string, plugin config.Plugin) (Result, error) {
	if err := ValidateName(name); err != nil {
		return Result{}, err
	}
	if strings.HasPrefix(plugin.Source, "builtin:") {
		ensure, ok := bundledEnsure(name, plugin.Source)
		if !ok {
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

func bundledEnsure(name, source string) (func(string) (bool, error), bool) {
	definition, ok := bundledplugins.DefinitionFor(name)
	if !ok || definition.Scope != bundledplugins.OfficeScope || source != "builtin:"+name {
		return nil, false
	}
	switch name {
	case bundledplugins.NudgeName:
		return bundledplugins.EnsureNudge, true
	case bundledplugins.ToolsName:
		return bundledplugins.EnsureTools, true
	default:
		return nil, false
	}
}

func bundledEnsureAt(root, name, source string) (func(string) (bool, error), bool) {
	definition, ok := bundledplugins.DefinitionFor(name)
	if !ok || definition.Scope != bundledplugins.GlobalScope || source != "builtin:"+name {
		return nil, false
	}
	return func(installRoot string) (bool, error) {
		if installRoot == "" {
			installRoot = root
		}
		return bundledplugins.EnsureAt(installRoot, name)
	}, true
}

func syncBundledAt(root, configPath, name string, plugin config.Plugin) (Result, error) {
	ensure, ok := bundledEnsureAt(root, name, plugin.Source)
	if !ok {
		return Result{}, fmt.Errorf("unknown bundled plugin %q", plugin.Source)
	}
	created, err := ensure(root)
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Join(root, name)
	manifest, err := plugins.ReadManifest(dir)
	if err == nil {
		var commit func() error
		commit, err = prepareConfig(configPath, name, plugin, manifest.DefaultConfig)
		if err == nil {
			err = commit()
		}
	}
	if err != nil && created {
		err = errors.Join(err, os.RemoveAll(dir))
	}
	return Result{Name: name, Revision: "bundled", Changed: created}, err
}

func syncAt(ctx context.Context, root, configPath, name string, plugin config.Plugin) (Result, error) {
	return syncAtRevision(ctx, root, configPath, name, plugin, nil)
}

func syncAtRevision(ctx context.Context, root, configPath, name string, plugin config.Plugin, plan *Result) (Result, error) {
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
		if plan == nil {
			args := []string{"clone", "--quiet", "--depth", "1"}
			if branch != "" {
				args = append(args, "--branch", branch, "--single-branch")
			}
			args = append(args, source, cache)
			if err := runGit(ctx, root, args...); err != nil {
				return Result{}, fmt.Errorf("clone %s: %w", source, err)
			}
		} else {
			if err := os.Mkdir(cache, 0o755); err != nil {
				return Result{}, err
			}
			if err := runGit(ctx, cache, "init", "--quiet"); err != nil {
				return Result{}, err
			}
			if err := runGit(ctx, cache, "remote", "add", "origin", source); err != nil {
				return Result{}, err
			}
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
		if plan == nil && branch == "" {
			branch, err = remoteDefaultBranch(ctx, cache)
			if err != nil {
				return Result{}, fmt.Errorf("resolve default branch for %s: %w", source, err)
			}
		}
		if plan == nil {
			if err := checkoutRemoteBranch(ctx, cache, source, branch); err != nil {
				return Result{}, err
			}
		}
	}
	if plan != nil {
		if err := fetchPlannedRevision(ctx, cache, source, *plan); err != nil {
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
	result := Result{Name: name, Revision: after, Changed: before == "" || before != after, branch: branch}
	if plan != nil {
		result.Previous = plan.Previous
		result.Changed = plan.Changed
		result.branch = plan.branch
	}
	return result, nil
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
