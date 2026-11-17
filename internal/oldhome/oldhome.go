// Package oldhome finds what omo 0.11 and older left on the machine: the
// releases that ran offices in a terminal. They kept a global home (in the
// same folder this omo uses) with config.yaml, the list of trusted offices,
// shared plugins and the company dashboard, and per office a .omo folder
// with the office's configuration, database, logs and the agents' git
// worktrees. Nothing of it can be carried over.
//
// Find only reads. Remove deletes what Find listed, and only what it
// listed: each office's .omo folder (its worktrees first, through git, so
// the repositories forget them; branches are kept), the login autostart
// entry of the company dashboard and everything inside the home folder. It
// refuses while an old office or the dashboard runs. DisableUpdateCheck
// edits one office's omo.yaml so the old omo stops offering an update at
// its start.
package oldhome

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/scolastico-dev/one-man-office/internal/filelock"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

// LastRelease is the last release of the terminal omo: the one a downgrade
// installs.
const LastRelease = "0.11.0"

// ErrNotOld is returned by Find when the folder has none of the files every
// old omo home has: it is no omo home of any version, and nothing may be
// removed from it.
var ErrNotOld = errors.New("not an omo home")

// The files every old home has: old omo wrote both at its first start.
var homeMarks = []string{"config.yaml", "known_plugins.json"}

// Worktree is a git worktree an old agent worked in.
type Worktree struct {
	Path string
	// Dirty: git status lists changes, or the state could not be read.
	Dirty bool
	// Unknown: git could not read the worktree; it counts as dirty.
	Unknown bool
	// gitDir is the git folder of the repository the worktree belongs to;
	// empty when the folder is no worktree git knows.
	gitDir string
}

// Office is an office of the old omo that still has its .omo folder.
type Office struct {
	Dir string
	// Data is Dir/.omo.
	Data string
	// Config is Data/omo.yaml; empty when the office has none.
	Config    string
	Worktrees []Worktree
	// Running: an old omo serves this office right now.
	Running bool
}

// Installation is what Find found.
type Installation struct {
	Home string
	// Entries are the names inside the home folder.
	Entries []string
	Offices []Office
	// Gone are trusted offices without a .omo folder (moved or deleted).
	Gone []string
	// CompanyRunning: the old company dashboard of this home runs.
	CompanyRunning bool
	// Autostart is the login autostart entry of the old dashboard; empty
	// when there is none.
	Autostart       string
	autostartConfig string
}

// Running names what of the old omo runs right now, one line each.
func (in Installation) Running() []string {
	var out []string
	if in.CompanyRunning {
		out = append(out, "the company dashboard of "+in.Home)
	}
	for _, o := range in.Offices {
		if o.Running {
			out = append(out, "the office "+o.Dir)
		}
	}
	return out
}

// Dirty lists the worktrees with changes nobody committed.
func (in Installation) Dirty() []Worktree {
	var out []Worktree
	for _, o := range in.Offices {
		for _, w := range o.Worktrees {
			if w.Dirty {
				out = append(out, w)
			}
		}
	}
	return out
}

// IsOld reports whether home holds the files of an old omo home (and not
// the layout marker of this omo's home).
func IsOld(home string) bool {
	if _, err := os.Lstat(filepath.Join(home, "layout.json")); err == nil {
		return false
	}
	for _, name := range homeMarks {
		if _, err := os.Lstat(filepath.Join(home, name)); err == nil {
			return true
		}
	}
	return false
}

// oldConfig is the part of the old config.yaml Find reads.
type oldConfig struct {
	TrustedOffices []string `yaml:"trusted_offices"`
}

// Find reads what the old omo left in home and in the offices its
// config.yaml lists. It changes nothing.
func Find(ctx context.Context, home string) (Installation, error) {
	in := Installation{Home: home}
	if !IsOld(home) {
		return in, ErrNotOld
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return in, err
	}
	for _, e := range entries {
		in.Entries = append(in.Entries, e.Name())
	}
	var cfg oldConfig
	if raw, err := homefile.Read(filepath.Join(home, "config.yaml"), 1<<20); err == nil {
		// A broken file lists no office; the home is still removed only
		// after the user saw that no office was found.
		_ = yaml.Unmarshal(raw, &cfg)
	} else if !os.IsNotExist(err) {
		return in, err
	}
	seen := map[string]bool{}
	for _, dir := range cfg.TrustedOffices {
		if !filepath.IsAbs(dir) {
			continue
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		data := filepath.Join(dir, ".omo")
		if info, err := os.Lstat(data); err != nil || !info.IsDir() {
			in.Gone = append(in.Gone, dir)
			continue
		}
		in.Offices = append(in.Offices, findOffice(ctx, dir, data))
	}
	in.CompanyRunning = companyRunning(ctx, filepath.Join(home, "company", "run.lock"))
	in.autostartConfig = filepath.Join(home, "company", "autostart.json")
	if _, err := os.Stat(in.autostartConfig); err == nil {
		in.Autostart = autostartEntry(in.autostartConfig)
	}
	return in, nil
}

func findOffice(ctx context.Context, dir, data string) Office {
	o := Office{Dir: dir, Data: data}
	if _, err := os.Stat(filepath.Join(data, "omo.yaml")); err == nil {
		o.Config = filepath.Join(data, "omo.yaml")
	}
	o.Running = officeRunning(ctx, filepath.Join(data, "omo.lock"))
	entries, _ := os.ReadDir(filepath.Join(data, "worktrees"))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		o.Worktrees = append(o.Worktrees, inspectWorktree(ctx, filepath.Join(data, "worktrees", e.Name())))
	}
	sort.Slice(o.Worktrees, func(i, j int) bool { return o.Worktrees[i].Path < o.Worktrees[j].Path })
	return o
}

// inspectWorktree reads whether git knows the folder as a worktree and
// whether it has changes.
func inspectWorktree(ctx context.Context, path string) Worktree {
	w := Worktree{Path: path}
	if raw, err := homefile.Read(filepath.Join(path, ".git"), 4096); err == nil {
		if gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:"); ok {
			w.gitDir = commonDir(strings.TrimSpace(gitdir))
		}
	}
	if w.gitDir == "" {
		// A plain folder: nothing of git to forget, but its files may be
		// work nobody committed.
		w.Dirty, w.Unknown = true, true
		return w
	}
	// --no-optional-locks: reading never rewrites the worktree's index.
	out, err := git(ctx, path, "--no-optional-locks", "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		w.Dirty, w.Unknown = true, true
		return w
	}
	w.Dirty = strings.TrimSpace(out) != ""
	return w
}

// commonDir turns the worktree's own git folder (.git/worktrees/<name> of
// its repository) into the repository's git folder.
func commonDir(gitdir string) string {
	raw, err := homefile.Read(filepath.Join(gitdir, "commondir"), 4096)
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(raw))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	return filepath.Clean(common)
}

// officeRunning: the old office wrote the address of its socket into
// .omo/omo.lock and removed the file when it ended. Only an address that
// answers counts; a file left by a crash does not.
func officeRunning(ctx context.Context, lock string) bool {
	raw, err := homefile.Read(lock, 4096)
	if err != nil {
		return false
	}
	endpoint := strings.TrimSpace(string(raw))
	if endpoint == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	conn, err := transport.Dial(ctx, endpoint)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// companyRunning: the old dashboard held company/run.lock while it ran.
func companyRunning(ctx context.Context, lock string) bool {
	if _, err := os.Stat(lock); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	l, err := filelock.Acquire(ctx, lock)
	if err != nil {
		return true
	}
	l.Close()
	return false
}

// Remove deletes what Find listed: every worktree (through git, so its
// repository forgets it; the branch stays), each office's .omo folder, the
// dashboard's login autostart entry and everything inside the home folder.
// It refuses while anything of the old omo runs. say gets one line per
// step.
func (in Installation) Remove(ctx context.Context, say func(string)) error {
	if running := in.Running(); len(running) > 0 {
		return fmt.Errorf("the old omo is running (%s); stop it first", strings.Join(running, ", "))
	}
	for _, o := range in.Offices {
		for _, w := range o.Worktrees {
			removeWorktree(ctx, w)
			say("removed the worktree " + w.Path)
		}
		if err := os.RemoveAll(o.Data); err != nil {
			return fmt.Errorf("remove %s: %w", o.Data, err)
		}
		say("removed " + o.Data)
	}
	if in.Autostart != "" {
		if err := removeAutostart(in.autostartConfig); err != nil {
			return fmt.Errorf("remove the login autostart entry %s: %w", in.Autostart, err)
		}
		say("removed the login autostart entry " + in.Autostart)
	}
	entries, err := os.ReadDir(in.Home)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(in.Home, e.Name())); err != nil {
			return fmt.Errorf("remove %s: %w", filepath.Join(in.Home, e.Name()), err)
		}
	}
	say("emptied " + in.Home)
	return nil
}

// removeWorktree asks git to remove the worktree; when git cannot, the
// folder is deleted and git told to forget worktrees whose folder is gone.
func removeWorktree(ctx context.Context, w Worktree) {
	if w.gitDir != "" {
		if _, err := git(ctx, "", "--git-dir="+w.gitDir, "worktree", "remove", "--force", "--force", w.Path); err == nil {
			return
		}
	}
	_ = os.RemoveAll(w.Path)
	if w.gitDir != "" {
		_, _ = git(ctx, "", "--git-dir="+w.gitDir, "worktree", "prune")
	}
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// DisableUpdateCheck sets startup.check_self_update to false in an old
// office's omo.yaml, so the old omo no longer offers a newer release at its
// start; every other line stays. It is the change the old omo itself made
// for `omo self-update --version`.
func DisableUpdateCheck(path string) error {
	raw, err := homefile.Read(path, 4<<20)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: expected a YAML mapping", path)
	}
	root := doc.Content[0]
	startup := mappingValue(root, "startup")
	if startup == nil {
		startup = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "startup"}, startup)
	}
	if startup.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: startup must be a mapping", path)
	}
	value := mappingValue(startup, "check_self_update")
	if value == nil {
		value = &yaml.Node{}
		startup.Content = append(startup.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "check_self_update"}, value)
	}
	*value = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false", LineComment: value.LineComment}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".omo-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
