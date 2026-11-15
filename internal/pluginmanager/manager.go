// Package pluginmanager installs, updates and removes the plugins the user
// chose: from a git repository or from the plugins bundled with omo.
//
// A plugin's installed tree is Root/<name> (Root is <home>/plugins, next to
// the settings files plugins/<name>.yaml). Install is also the update: it
// builds the new tree in a staging folder, checks it, and only then puts it
// in place of the old one, so a failed install or update leaves nothing
// behind and never touches a working install.
//
// A plugin is code that runs as the user. This package never decides that
// one is installed: its callers (the user's routes) do. What it guarantees
// is that a source cannot turn the install itself into command execution or
// into files outside the plugin's folder:
//   - repositories are fetched only through gitops.FetchSource (no hooks,
//     filters, submodules or links, bounded in size and time, credentials
//     never in a message), into Root/.cache/<name>, which is removed after
//     every install;
//   - subpath selects a folder inside the checkout: relative, no "..", no
//     ".git", every element a real folder (no link);
//   - the copy takes folders and regular files only, read through homefile
//     (no link is followed), skips .git and is bounded in files and bytes;
//   - the manifest is read with the runtime's plugins.ReadManifest and every
//     Lua file it names must be a regular file that parses, before anything
//     is put in place.
//
// Nothing here runs at server start; nothing is fetched except by Install.
package pluginmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yuin/gopher-lua/parse"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	bundledplugins "github.com/scolastico-dev/one-man-office/plugins"
)

// Options are where plugins are installed and the git runner that fetches
// them.
type Options struct {
	// Root is the plugins folder (<home>/plugins), an absolute path.
	Root string
	// Git fetches repository sources; not needed for bundled plugins.
	Git *gitops.Git
}

// Bounds of an installed tree.
const (
	// MaxFiles is the number of files a plugin may have.
	MaxFiles = 10000
	// MarkerFile is the record of what was installed, written into the
	// installed tree last (a file of the same name in the source is
	// replaced).
	MarkerFile = ".omo-install.json"
	// maxMarker bounds the marker when it is read back.
	maxMarker = 8 << 10
)

// MaxTreeBytes bounds the bytes of all files of a plugin. It is a variable
// so tests can lower it.
var MaxTreeBytes int64 = 256 << 20

// CommitBundled is the commit recorded for a plugin copied from omo.
const CommitBundled = "bundled"

var (
	// ErrNameTaken is the error of an install whose name differs from an
	// existing plugin only in letter case: on a file system that ignores
	// case both would be one folder.
	ErrNameTaken = errors.New("another plugin has this name in other letter case")
	// ErrNotInstalled is the error of Installed for a plugin without an
	// installed tree or without its record.
	ErrNotInstalled = errors.New("the plugin is not installed")
)

// FieldError is a refusal of one field of the plugin settings ("name",
// "source", "subpath", "ref"). Its message holds no value of the field.
type FieldError struct {
	Field   string
	Message string
	err     error
}

func (e *FieldError) Error() string { return e.Message }
func (e *FieldError) Unwrap() error { return e.err }

func fieldErr(field, msg string, wrapped error) error {
	return &FieldError{Field: field, Message: msg, err: wrapped}
}

// Installation is the record of an installed tree.
type Installation struct {
	Source  string `json:"source"`
	Subpath string `json:"subpath,omitempty"`
	Ref     string `json:"ref,omitempty"`
	// Commit is the full commit id of the checkout the tree came from, or
	// CommitBundled.
	Commit string `json:"commit"`
	// Digest is, for a bundled plugin, bundledplugins.Digest of the copied
	// files; compare it with the running omo's to see whether Update
	// changes anything.
	Digest      string    `json:"digest,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
}

const nameRule = "a plugin name is 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit"

// Install installs or updates plugin s (name, source, subpath, ref) at
// Root/<s.Name>: a repository source is fetched at s.Ref through
// gitops.FetchSource, a source builtin:<name> is copied from the binary.
// The tree is built in a staging folder, checked (manifest through
// plugins.ReadManifest, every Lua file present and parsing) and then moved
// in place of the old tree, which is kept until then. It returns s with the
// manifest's default_config merged under s.Config (the user's keys win; top
// level only) and the installed commit (CommitBundled for a bundled
// plugin). On any error nothing is left behind and an existing install is
// unchanged.
//
// Install writes no settings file, starts nothing and deletes no data.
func Install(ctx context.Context, o Options, s api.PluginSettings) (api.PluginSettings, string, error) {
	out, commit, prev, err := InstallKeeping(ctx, o, s)
	if err == nil {
		prev.Discard()
	}
	return out, commit, err
}

// Previous is the tree an install by InstallKeeping replaced, kept aside
// until Discard or Restore. Its zero value (no earlier tree) is usable.
type Previous struct {
	root, name string
	// old is the kept tree; "" when there was none.
	old string
}

// Discard deletes the kept tree.
func (p *Previous) Discard() {
	if p != nil && p.old != "" {
		_ = os.RemoveAll(p.old)
		p.old = ""
	}
}

// Restore puts the kept tree back in place of the new one (without an
// earlier tree, the new one is removed). The caller makes sure no other
// install of the plugin runs meanwhile.
func (p *Previous) Restore() error {
	if p == nil || p.name == "" {
		return nil
	}
	target := filepath.Join(p.root, p.name)
	if _, err := os.Lstat(target); err == nil {
		tmp, err := os.MkdirTemp(p.root, ".old-"+p.name+"-")
		if err != nil || os.Remove(tmp) != nil || os.Rename(target, tmp) != nil {
			return errors.New("the new plugin folder cannot be moved aside")
		}
		defer os.RemoveAll(tmp)
	}
	if p.old == "" {
		return nil
	}
	if err := os.Rename(p.old, target); err != nil {
		return errors.New("the earlier plugin folder cannot be put back")
	}
	p.old = ""
	return nil
}

// InstallKeeping is Install that keeps the tree it replaced aside: the
// caller calls Restore when what follows the install fails (its settings
// cannot be written), else Discard. A kept tree that neither happens to
// (omo ended) is removed by the next install of the plugin.
func InstallKeeping(ctx context.Context, o Options, s api.PluginSettings) (api.PluginSettings, string, *Previous, error) {
	if !plugins.ValidName(s.Name) {
		return s, "", nil, fieldErr("name", nameRule, nil)
	}
	subpath, err := cleanSubpath(s.Subpath)
	if err != nil {
		return s, "", nil, err
	}
	bundled, isBundled := strings.CutPrefix(s.Source, settings.BuiltinSource)
	if isBundled {
		if !bundledplugins.Has(bundled) {
			return s, "", nil, fieldErr("source", "omo bundles no plugin of this name; the bundled plugins are "+strings.Join(bundledplugins.Names(), ", "), bundledplugins.ErrUnknown)
		}
		if subpath != "" {
			return s, "", nil, fieldErr("subpath", "a bundled plugin has no subpath", nil)
		}
		if s.Ref != "" {
			return s, "", nil, fieldErr("ref", "a bundled plugin has no ref; it is the version of the running omo", nil)
		}
	} else {
		if err := gitops.ValidSourceURL(s.Source); err != nil {
			return s, "", nil, fieldErr("source", "source must be builtin:<name>, an https:// or ssh:// address, a host:path address or an absolute folder path", err)
		}
		if err := gitops.ValidRef(s.Ref); err != nil {
			return s, "", nil, fieldErr("ref", "ref must be empty (the default branch), a branch or tag name git accepts that does not start with -, or a full commit id", err)
		}
		if o.Git == nil {
			return s, "", nil, errors.New("plugin installs from git are not available")
		}
	}
	root, err := rootOf(o)
	if err != nil {
		return s, "", nil, err
	}
	unlock, err := lock(ctx, root, s.Name)
	if err != nil {
		return s, "", nil, err
	}
	defer unlock()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return s, "", nil, errors.New("the plugins folder cannot be created")
	}
	if err := checkCase(root, s.Name); err != nil {
		return s, "", nil, err
	}
	removeLeftovers(root, s.Name)

	stage, err := os.MkdirTemp(root, ".stage-"+s.Name+"-")
	if err != nil {
		return s, "", nil, errors.New("the staging folder cannot be created")
	}
	defer os.RemoveAll(stage)

	inst := Installation{Source: s.Source, Subpath: subpath, Ref: s.Ref, InstalledAt: time.Now().UTC()}
	if isBundled {
		if err := bundledplugins.CopyTo(bundled, stage); err != nil {
			return s, "", nil, errors.New("the bundled plugin cannot be copied")
		}
		inst.Commit = CommitBundled
		if inst.Digest, err = bundledplugins.Digest(bundled); err != nil {
			return s, "", nil, err
		}
	} else {
		cacheRoot := filepath.Join(root, ".cache")
		if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
			return s, "", nil, errors.New("the plugin cache folder cannot be created")
		}
		cache := filepath.Join(cacheRoot, s.Name)
		defer os.RemoveAll(cache)
		commit, err := o.Git.FetchSource(ctx, s.Source, s.Ref, cache)
		if err != nil {
			return s, "", nil, err
		}
		from, err := folderIn(cache, subpath)
		if err != nil {
			return s, "", nil, err
		}
		if err := copyTree(from, stage); err != nil {
			return s, "", nil, err
		}
		inst.Commit = commit
	}

	man, err := check(stage)
	if err != nil {
		return s, "", nil, fmt.Errorf("the plugin is not valid: %w", err)
	}
	marker, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return s, "", nil, err
	}
	if err := os.WriteFile(filepath.Join(stage, MarkerFile), append(marker, '\n'), 0o600); err != nil {
		return s, "", nil, errors.New("the install record cannot be written")
	}
	old, err := activate(root, s.Name, stage)
	if err != nil {
		return s, "", nil, err
	}
	out := s
	out.Subpath = subpath
	out.Config = mergeConfig(man.DefaultConfig, s.Config)
	return out, inst.Commit, &Previous{root: root, name: s.Name, old: old}, nil
}

// Remove deletes plugin name's installed tree (moved aside first, so no half
// tree stays at Root/<name>), its fetch cache Root/.cache/<name> and its data
// folder Root/.data/<name>. A part that does not exist is no error.
//
// The caller stops the plugin first (loads the runtime without it), and
// deletes its settings file and its stored values and log
// (db.DeletePluginData) itself.
func Remove(o Options, name string) error {
	if !plugins.ValidName(name) {
		return fieldErr("name", nameRule, nil)
	}
	root, err := rootOf(o)
	if err != nil {
		return err
	}
	unlock, err := lock(context.Background(), root, name)
	if err != nil {
		return err
	}
	defer unlock()
	var errs []error
	target := filepath.Join(root, name)
	if _, err := os.Lstat(target); err == nil {
		if old, err := os.MkdirTemp(root, ".old-"+name+"-"); err == nil {
			_ = os.Remove(old)
			if os.Rename(target, old) == nil {
				target = old
			}
		}
		if err := os.RemoveAll(target); err != nil {
			errs = append(errs, errors.New("the plugin's folder cannot be deleted"))
		}
	}
	if err := os.RemoveAll(filepath.Join(root, ".cache", name)); err != nil {
		errs = append(errs, errors.New("the plugin's fetch cache cannot be deleted"))
	}
	if err := os.RemoveAll(filepath.Join(root, ".data", name)); err != nil {
		errs = append(errs, errors.New("the plugin's data folder cannot be deleted"))
	}
	removeLeftovers(root, name)
	return errors.Join(errs...)
}

// Installed reads the record Install wrote into Root/<name>. The record lies
// in a folder agents can write to: its texts are for display only, and a
// commit that is neither a full commit id nor CommitBundled reads as "".
func Installed(o Options, name string) (Installation, error) {
	if !plugins.ValidName(name) {
		return Installation{}, ErrNotInstalled
	}
	root, err := rootOf(o)
	if err != nil {
		return Installation{}, err
	}
	raw, err := homefile.Read(filepath.Join(root, name, MarkerFile), maxMarker)
	if errors.Is(err, os.ErrNotExist) {
		return Installation{}, ErrNotInstalled
	}
	if err != nil {
		return Installation{}, errors.New("the install record cannot be read")
	}
	var inst Installation
	if err := json.Unmarshal(raw, &inst); err != nil {
		return Installation{}, errors.New("the install record is not valid")
	}
	if inst.Commit != CommitBundled && !commitID.MatchString(inst.Commit) {
		inst.Commit = ""
	}
	return inst, nil
}

var commitID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// Bundled lists the plugins omo bundles, with the description of their
// manifest.
func Bundled() []api.BundledPlugin {
	out := []api.BundledPlugin{}
	for _, name := range bundledplugins.Names() {
		b := api.BundledPlugin{Name: name}
		if raw, err := bundledplugins.ReadFile(name, plugins.ManifestFile); err == nil {
			if m, err := plugins.ParseManifest(raw); err == nil {
				b.Description = m.Description
			}
		}
		out = append(out, b)
	}
	return out
}

// rootOf is o.Root, cleaned; it must be an absolute path.
func rootOf(o Options) (string, error) {
	if o.Root == "" || !filepath.IsAbs(o.Root) {
		return "", errors.New("the plugins folder is not an absolute path")
	}
	return filepath.Clean(o.Root), nil
}

// locks holds one lock per plugins folder and lower-case plugin name:
// installs, updates and removals of one plugin (also of two names that
// differ only in case) run one after the other.
var locks sync.Map // string → chan struct{}

func lock(ctx context.Context, root, name string) (func(), error) {
	v, _ := locks.LoadOrStore(root+"\x00"+strings.ToLower(name), make(chan struct{}, 1))
	ch := v.(chan struct{})
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// checkCase refuses name when a folder or settings file in root has the
// same name in other letter case.
func checkCase(root, name string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return errors.New("the plugins folder cannot be read")
	}
	for _, e := range entries {
		other := e.Name()
		if strings.HasPrefix(other, ".") {
			continue
		}
		if !e.IsDir() {
			var ok bool
			if other, ok = strings.CutSuffix(other, ".yaml"); !ok {
				continue
			}
		}
		if other != name && strings.EqualFold(other, name) {
			return fieldErr("name", "another plugin, "+quote(other)+", has this name in other letter case; on some file systems both would be one folder", ErrNameTaken)
		}
	}
	return nil
}

// removeLeftovers deletes staging and old folders of name that an
// interrupted install or removal left in root.
func removeLeftovers(root, name string) {
	for _, prefix := range []string{".stage-", ".old-"} {
		matches, _ := filepath.Glob(filepath.Join(root, prefix+name+"-*"))
		for _, m := range matches {
			// A name may be a prefix of another ("a" of "a-1"): only the
			// folders MkdirTemp made for this name (digits after
			// "<prefix><name>-") are removed.
			rest := strings.TrimPrefix(filepath.Base(m), prefix+name+"-")
			if _, err := strconv.ParseUint(rest, 10, 64); err == nil {
				_ = os.RemoveAll(m)
			}
		}
	}
}

// cleanSubpath checks a subpath: "" (or ".") for the repository's top
// folder, else a relative path with forward slashes, no "..", no ".git"
// element. It returns the cleaned path.
func cleanSubpath(sub string) (string, error) {
	refuse := func() (string, error) {
		return "", fieldErr("subpath", "subpath must be a folder inside the repository: a relative path with / between folder names, without .. or .git", nil)
	}
	if sub == "" || sub == "." {
		return "", nil
	}
	if len(sub) > 512 || !utf8.ValidString(sub) || strings.ContainsAny(sub, "\\\x00:") || strings.HasPrefix(sub, "/") {
		return refuse()
	}
	for _, part := range strings.Split(sub, "/") {
		if part == ".." || strings.EqualFold(part, ".git") {
			return refuse()
		}
		for _, r := range part {
			if r < 0x20 || r == 0x7f {
				return refuse()
			}
		}
	}
	clean := path.Clean(sub)
	if clean == "." {
		return "", nil
	}
	return clean, nil
}

// folderIn is the folder sub of checkout; every element of the way must be
// a folder, not a link.
func folderIn(checkout, sub string) (string, error) {
	cur := checkout
	if sub == "" {
		return cur, nil
	}
	for _, part := range strings.Split(sub, "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			return "", fieldErr("subpath", "the repository has no folder at subpath", err)
		}
		if err != nil || !info.IsDir() {
			return "", fieldErr("subpath", "subpath must name a folder of the repository, not a file or link", nil)
		}
	}
	return cur, nil
}

// copyTree copies the folders and regular files of src into the existing
// folder dst, skipping every .git; a link or any other kind of file is an
// error. Files keep an executable bit (mode 0700, else 0600); folders get
// 0700. At most MaxFiles files and MaxTreeBytes bytes.
func copyTree(src, dst string) error {
	files := 0
	var total int64
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return errors.New("the plugin's files cannot be read")
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.EqualFold(d.Name(), ".git") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		shown := quote(filepath.ToSlash(rel))
		target := filepath.Join(dst, rel)
		switch {
		case d.Type()&os.ModeSymlink != 0:
			return fmt.Errorf("the plugin holds a link, %s; plugins hold folders and regular files only", shown)
		case d.IsDir():
			if err := os.Mkdir(target, 0o700); err != nil {
				return errors.New("the staging folder cannot be written")
			}
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("the plugin holds %s, which is not a regular file", shown)
		}
		files++
		if files > MaxFiles {
			return fmt.Errorf("the plugin has more than %d files", MaxFiles)
		}
		n, err := copyFile(p, target, MaxTreeBytes-total)
		total += n
		if errors.Is(err, errTreeTooLarge) {
			return fmt.Errorf("the plugin's files are larger than %s", homefile.Size(MaxTreeBytes))
		}
		if err != nil {
			return fmt.Errorf("copying %s: %w", shown, err)
		}
		return nil
	})
}

var errTreeTooLarge = errors.New("too large")

// copyFile copies the regular file src (no link followed) to the new file
// dst, at most limit bytes.
func copyFile(src, dst string, limit int64) (int64, error) {
	in, err := homefile.Open(src)
	if err != nil {
		if text, ok := homefile.Refusal(err, limit); ok {
			return 0, errors.New(text)
		}
		return 0, errors.New("the file cannot be read")
	}
	defer in.Close()
	mode := os.FileMode(0o600)
	if info, err := in.Stat(); err == nil && info.Mode().Perm()&0o111 != 0 {
		mode = 0o700
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return 0, errors.New("the file cannot be written")
	}
	n, err := io.Copy(out, io.LimitReader(in, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, errors.New("the file cannot be copied")
	}
	if n > limit {
		return n, errTreeTooLarge
	}
	return n, nil
}

// check reads the manifest of the tree at dir with the runtime's reader and
// checks that every Lua file it names is a regular file that parses, the
// way the runtime loads it.
func check(dir string) (plugins.Manifest, error) {
	man, err := plugins.ReadManifest(dir)
	if errors.Is(err, plugins.ErrNotInstalled) {
		return man, errors.New("it has no plugin.json")
	}
	if err != nil {
		return man, err
	}
	for i, h := range man.Hooks {
		if h.Lua == "" {
			continue
		}
		rel := path.Clean(strings.ReplaceAll(h.Lua, "\\", "/"))
		src, err := homefile.Read(filepath.Join(dir, filepath.FromSlash(rel)), plugins.MaxLuaBytes)
		if err != nil {
			if text, ok := homefile.Refusal(err, plugins.MaxLuaBytes); ok {
				return man, fmt.Errorf("hook %d: %s: %s", i+1, quote(rel), text)
			}
			return man, fmt.Errorf("hook %d: the Lua file %s is missing", i+1, quote(rel))
		}
		if len(src) > 0 && src[0] == 0x1b {
			return man, fmt.Errorf("hook %d: %s is precompiled Lua, which is not accepted", i+1, quote(rel))
		}
		// The runtime runs a hook's source as the body of one function.
		if _, err := parse.Parse(strings.NewReader("function f(...) "+string(src)+"\nend"), rel); err != nil {
			return man, fmt.Errorf("hook %d: %s does not parse: %s", i+1, quote(rel), quote(oneLine(err.Error())))
		}
	}
	return man, nil
}

// activate puts the checked tree stage in place as root/name. An existing
// tree is moved aside first and returned (the caller removes or restores
// it); if stage cannot be moved, the old tree is moved back.
func activate(root, name, stage string) (string, error) {
	target := filepath.Join(root, name)
	old := ""
	if _, err := os.Lstat(target); err == nil {
		tmp, err := os.MkdirTemp(root, ".old-"+name+"-")
		if err != nil || os.Remove(tmp) != nil || os.Rename(target, tmp) != nil {
			return "", errors.New("the old plugin folder cannot be moved aside")
		}
		old = tmp
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("the plugin folder cannot be checked")
	}
	if err := os.Rename(stage, target); err != nil {
		if old != "" {
			_ = os.Rename(old, target)
		}
		return "", errors.New("the new plugin folder cannot be put in place")
	}
	return old, nil
}

// mergeConfig is defaults with user over it (top-level keys of user win).
// Numbers of the manifest become int64 or float64, so the settings file
// stores them as numbers.
func mergeConfig(defaults plugins.DefaultConfig, user map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range defaults {
		out[k] = plainJSON(v)
	}
	for k, v := range user {
		out[k] = v
	}
	return out
}

// plainJSON replaces every json.Number in v by int64 or float64.
func plainJSON(v any) any {
	switch t := v.(type) {
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return i
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, x := range t {
			m[k] = plainJSON(x)
		}
		return m
	case []any:
		l := make([]any, len(t))
		for i, x := range t {
			l[i] = plainJSON(x)
		}
		return l
	}
	return v
}

// quote is text from a source (a file name, a parser message) as it may
// appear in a message: cut, quoted, every character that is not printable
// ASCII escaped.
func quote(s string) string {
	if utf8.RuneCountInString(s) > 200 {
		s = string([]rune(s)[:200]) + "…"
	}
	return strconv.QuoteToASCII(s)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
