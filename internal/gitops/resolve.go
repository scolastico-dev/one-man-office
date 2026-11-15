package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// An agent owns the folder of its worktree: it can replace or delete the
// .git file at its top, put a repository of its own there, or replace the
// folder itself by a link. git asked to work in such a folder finds its
// repository through that .git entry (or, without one, in a parent folder)
// and runs whatever that repository's config names: a clean filter, an
// fsmonitor hook, an external diff. So omo never lets git find the
// repository of a worktree. It looks the worktree up in the records of the
// project's repository (the user's, trusted), and runs git with the
// repository, the work tree and the common git directory set explicitly.

var (
	// ErrNotRecorded marks a folder the repository does not record as one
	// of its worktrees (or records more than once).
	ErrNotRecorded = errors.New("not a worktree the repository records")
	// ErrNotAFolder marks a worktree path that is not a plain folder: a
	// link, a file or something else.
	ErrNotAFolder = errors.New("the worktree is not a plain folder")
	// ErrWorktreeConfig marks a worktree whose own config file
	// (config.worktree) sets something omo will not run git with.
	ErrWorktreeConfig = errors.New("the worktree's own config sets something omo does not run git with")
	// ErrAgentFolder marks a git command that would have git look for its
	// repository in or below a folder agents control. Commands there run
	// only through a worktree resolved from its repository's records.
	ErrAgentFolder = errors.New("git may not look for a repository in a folder agents control")
	// ErrUnsafeFile marks a file of a git directory that omo reads itself
	// and that is not a regular file of a sane size.
	ErrUnsafeFile = errors.New("not a regular file of a sane size")
)

// worktreeGit is a worktree resolved from its repository's records.
type worktreeGit struct {
	// dir is the worktree's top folder: a plain folder (no link) whose
	// parent is given with links resolved, as the repository records it.
	dir string
	// gitDir is the worktree's own git directory, from the records.
	gitDir string
	// common is the repository's common git directory.
	common string
}

// worktreeConfig is put before the subcommand of every git command run in a
// resolved worktree: no file system monitor (it is a command), no hooks, no
// untracked cache (the index is the agent's and could fake one), no descent
// into submodules (their repositories are found through .git entries in the
// agent's folder).
var worktreeConfig = []string{
	"-c", "core.fsmonitor=false",
	"-c", "core.hooksPath=" + os.DevNull,
	"-c", "submodule.recurse=false",
	"-c", "diff.ignoreSubmodules=dirty",
	"-c", "status.submoduleSummary=false",
	"-c", "core.untrackedCache=false",
}

// config is what the commands in w get before their subcommand. The main
// worktree is the user's own folder: its untracked cache is kept (a command
// that writes the index would drop it otherwise).
func (w *worktreeGit) config() []string {
	if w.gitDir == w.common {
		return worktreeConfig[:len(worktreeConfig)-2]
	}
	return worktreeConfig
}

// env sets git's repository and work tree explicitly. GIT_COMMON_DIR keeps
// git from reading the commondir file of the worktree's git directory;
// GIT_CEILING_DIRECTORIES keeps any git started below the worktree from
// walking up out of it.
func (w *worktreeGit) env() []string {
	return []string{
		"GIT_DIR=" + w.gitDir,
		"GIT_WORK_TREE=" + w.dir,
		"GIT_COMMON_DIR=" + w.common,
		"GIT_CEILING_DIRECTORIES=" + filepath.Dir(w.dir),
	}
}

// at is where a command in this worktree runs.
func (w *worktreeGit) at() where { return where{wt: w} }

// where says where a git command runs: in a folder of a repository the user
// owns (dir), where git finds the repository itself, or in a worktree
// resolved from its repository's records (wt).
type where struct {
	dir string
	wt  *worktreeGit
}

func inRepo(dir string) where { return where{dir: dir} }

// folder is the folder the command runs in.
func (w where) folder() string {
	if w.wt != nil {
		return w.wt.dir
	}
	return w.dir
}

// SetAgentFolders names the folders whose content agents control (omo's
// worktrees folder). From then on the client refuses, with ErrAgentFolder,
// any git command that would look for its repository in or below one of
// them: commands in a worktree there run only through its resolution from
// the repository's records.
func (g *Git) SetAgentFolders(roots ...string) {
	var clean []string
	for _, r := range roots {
		if r == "" {
			continue
		}
		if abs, err := filepath.Abs(r); err == nil {
			r = abs
		}
		clean = append(clean, filepath.Clean(r))
		if real, err := filepath.EvalSymlinks(r); err == nil && real != filepath.Clean(r) {
			clean = append(clean, real)
		}
	}
	g.mu.Lock()
	g.agentRoots = clean
	g.mu.Unlock()
}

// AgentFolders returns the folders set with SetAgentFolders (and their
// resolved forms).
func (g *Git) AgentFolders() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.agentRoots...)
}

// checkDiscovery refuses a folder in or below an agent folder for a command
// that lets git find its repository.
func (g *Git) checkDiscovery(dir string) error {
	g.mu.Lock()
	roots := g.agentRoots
	g.mu.Unlock()
	if len(roots) == 0 || dir == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrAgentFolder, dir)
	}
	forms := []string{filepath.Clean(abs)}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		forms = append(forms, real)
	}
	for _, r := range roots {
		for _, f := range forms {
			if f == r || within(r, f) {
				return fmt.Errorf("%w: %s", ErrAgentFolder, dir)
			}
		}
	}
	return nil
}

// within reports whether p lies below root (both clean and absolute).
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// samePathString compares two clean absolute paths as the platform's file
// system does by default.
func samePathString(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// commonDir returns the common git directory of the repository at repo,
// with links resolved.
func (g *Git) commonDir(ctx context.Context, repo string) (string, error) {
	out, _, err := g.exec(cmdOpts{ctx: ctx, timeout: g.query, dir: repo}, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common := filepath.Clean(fromGitPath(strings.TrimSpace(out)))
	if real, err := filepath.EvalSymlinks(common); err == nil {
		common = real
	}
	return common, nil
}

// PlainFolder returns the absolute form of dir with links in its parent
// resolved, and checks that dir itself is a plain folder, not a link to one
// (ErrNotAFolder). It is how omo names a folder an agent controls: the agent
// can replace the folder, not its parent.
func PlainFolder(dir string) (string, error) { return plainFolder(dir) }

// plainFolder returns dir with links in its parent resolved, and checks
// that dir itself is a plain folder, not a link to one.
func plainFolder(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	parent := filepath.Dir(abs)
	if real, err := filepath.EvalSymlinks(parent); err == nil {
		parent = real
	}
	cand := filepath.Join(parent, filepath.Base(abs))
	fi, err := os.Lstat(cand)
	if err != nil {
		return "", err
	}
	if fi.Mode().Type() != fs.ModeDir {
		return "", fmt.Errorf("%w: %s", ErrNotAFolder, dir)
	}
	return cand, nil
}

// recordOf finds the git directory that the repository with the common git
// directory common records for the worktree at dir (clean, absolute, links
// in its parent resolved): the common directory itself for the main
// worktree, else the one entry of common/worktrees whose gitdir file names
// dir/.git. Nothing in dir is read.
func recordOf(common, dir string) (string, error) {
	if filepath.Base(common) == ".git" && samePathString(filepath.Dir(common), dir) {
		return common, nil
	}
	want := filepath.Join(dir, ".git")
	records := filepath.Join(common, "worktrees")
	entries, err := os.ReadDir(records)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	found := ""
	for _, e := range entries {
		rec := filepath.Join(records, e.Name())
		if fi, err := os.Lstat(rec); err != nil || !fi.IsDir() {
			continue
		}
		raw, err := readSmallFile(filepath.Join(rec, "gitdir"), 1<<16)
		if err != nil {
			continue
		}
		p := fromGitPath(strings.TrimSpace(string(raw)))
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(rec, p)
		}
		if !samePathString(filepath.Clean(p), want) {
			continue
		}
		if found != "" {
			return "", fmt.Errorf("%w: %s is recorded twice", ErrNotRecorded, dir)
		}
		found = rec
	}
	if found == "" {
		return "", fmt.Errorf("%w: %s", ErrNotRecorded, dir)
	}
	return found, nil
}

// inWorktree resolves the worktree at dir of the repository at repo for
// running git there. The worktree's git directory comes from the
// repository's own records, which must name exactly this folder; dir must
// be a plain folder (links are refused, also one to another worktree);
// nothing inside dir is read. The worktree's own config file, read by git
// when the repository enables per-worktree config, may set only what the
// user's main worktree sets itself or what sparse checkout needs
// (ErrWorktreeConfig). Every git command run with the result gets GIT_DIR,
// GIT_WORK_TREE, GIT_COMMON_DIR and GIT_CEILING_DIRECTORIES and the
// settings of worktreeConfig.
func (g *Git) inWorktree(ctx context.Context, repo, dir string) (*worktreeGit, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: no folder given", ErrNotRecorded)
	}
	common, err := g.commonDir(ctx, repo)
	if err != nil {
		return nil, err
	}
	cand, err := plainFolder(dir)
	if err != nil {
		return nil, err
	}
	gitDir, err := recordOf(common, cand)
	if errors.Is(err, ErrNotRecorded) {
		gitDir, err = g.mainWorktreeOf(ctx, repo, cand, err)
	}
	if err != nil {
		return nil, err
	}
	wt := &worktreeGit{dir: cand, gitDir: gitDir, common: common}
	if gitDir != common {
		if err := g.vetWorktreeConfig(ctx, wt); err != nil {
			return nil, err
		}
	}
	return wt, nil
}

// mainWorktreeOf returns the git directory of the repository folder repo
// itself when cand is that folder (a main worktree whose git directory is
// kept elsewhere, as git init --separate-git-dir does); otherwise notFound.
func (g *Git) mainWorktreeOf(ctx context.Context, repo, cand string, notFound error) (string, error) {
	out, _, err := g.exec(cmdOpts{ctx: ctx, timeout: g.query, dir: repo}, "rev-parse", "--path-format=absolute", "--show-toplevel", "--absolute-git-dir")
	if err != nil {
		return "", notFound
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return "", notFound
	}
	top, err := plainFolder(fromGitPath(strings.TrimSpace(lines[0])))
	if err != nil || !samePathString(top, cand) {
		return "", notFound
	}
	gitDir := filepath.Clean(fromGitPath(strings.TrimSpace(lines[1])))
	if real, err := filepath.EvalSymlinks(gitDir); err == nil {
		gitDir = real
	}
	return gitDir, nil
}

// worktreeRecord returns the git directory the repository at repo records
// for the worktree at dir, without checking the folder itself. It is for
// reading state files of that git directory, never for running git.
func (g *Git) worktreeRecord(repo, dir string) (string, error) {
	common, err := g.commonDir(context.Background(), repo)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	parent := filepath.Dir(abs)
	if real, err := filepath.EvalSymlinks(parent); err == nil {
		parent = real
	}
	return recordOf(common, filepath.Join(parent, filepath.Base(abs)))
}

// sparseKeys may be set in a worktree's own config whatever the main
// worktree sets: git's sparse checkout writes them there.
var sparseKeys = map[string]bool{
	"core.sparsecheckout":     true,
	"core.sparsecheckoutcone": true,
	"index.sparse":            true,
}

// vetWorktreeConfig checks the config.worktree file of a linked worktree's
// git directory. git reads it when the repository enables per-worktree
// config, and an agent can write it: a filter, a diff driver or any other
// command defined there would run in omo's git. Each entry must be one of
// sparseKeys or appear with the same value in the main worktree's own
// config.worktree (git copies that one into new worktrees).
func (g *Git) vetWorktreeConfig(ctx context.Context, wt *worktreeGit) error {
	path := filepath.Join(wt.gitDir, "config.worktree")
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	entries, err := g.configEntries(ctx, path)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrWorktreeConfig, path, err)
	}
	if len(entries) == 0 {
		return nil
	}
	trusted := map[[2]string]bool{}
	mainPath := filepath.Join(wt.common, "config.worktree")
	if _, err := os.Lstat(mainPath); err == nil {
		mainEntries, err := g.configEntries(ctx, mainPath)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrWorktreeConfig, mainPath, err)
		}
		for _, e := range mainEntries {
			trusted[e] = true
		}
	}
	for _, e := range entries {
		if sparseKeys[e[0]] || trusted[e] {
			continue
		}
		return fmt.Errorf("%w: %s sets %s; remove that setting there", ErrWorktreeConfig, path, displayPath(e[0]))
	}
	return nil
}

// maxConfigFile bounds a config file omo reads.
const maxConfigFile = 1 << 20

// configEntries lists the entries (name, value) of the config file at path,
// includes not followed. The file is copied first (a regular file of at most
// maxConfigFile bytes) and git reads the copy in a private folder.
func (g *Git) configEntries(ctx context.Context, path string) ([][2]string, error) {
	tmp, err := os.MkdirTemp("", "omo-config-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	cp := filepath.Join(tmp, "config")
	if err := copyRegular(ctx, path, cp, maxConfigFile); err != nil {
		return nil, err
	}
	out, _, err := g.exec(cmdOpts{ctx: ctx, timeout: g.query, dir: tmp, env: []string{"GIT_CEILING_DIRECTORIES=" + filepath.Dir(tmp)}},
		"config", "--file", cp, "--no-includes", "--null", "--list")
	if err != nil {
		return nil, err
	}
	var entries [][2]string
	for _, rec := range strings.Split(out, "\x00") {
		if rec == "" {
			continue
		}
		name, value, _ := strings.Cut(rec, "\n")
		entries = append(entries, [2]string{name, value})
	}
	return entries, nil
}

// readSmallFile reads a regular file of at most max bytes without following
// a link and without blocking on a pipe.
func readSmallFile(path string, max int64) ([]byte, error) {
	var buf bytes.Buffer
	if err := copyRegularTo(context.Background(), path, &buf, max); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// copyRegular copies the regular file src to the new file dst, refusing a
// link, a device, a pipe or a file over max bytes, and stopping when ctx
// ends.
func copyRegular(ctx context.Context, src, dst string, max int64) error {
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = copyRegularTo(ctx, src, out, max)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func copyRegularTo(ctx context.Context, src string, w io.Writer, max int64) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is a %s", ErrUnsafeFile, src, kindOf(fi.Mode()))
	}
	if fi.Size() > max {
		return fmt.Errorf("%w: %s has %d bytes, more than %d", ErrUnsafeFile, src, fi.Size(), max)
	}
	f, err := openNoFollow(src)
	if err != nil {
		return err
	}
	defer f.Close()
	// The path may have changed between the check and the open.
	ofi, err := f.Stat()
	if err != nil {
		return err
	}
	if !ofi.Mode().IsRegular() || !os.SameFile(fi, ofi) {
		return fmt.Errorf("%w: %s changed while it was opened", ErrUnsafeFile, src)
	}
	buf := make([]byte, 64<<10)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		r, rerr := f.Read(buf)
		if r > 0 {
			n += int64(r)
			if n > max {
				return fmt.Errorf("%w: %s grew past %d bytes", ErrUnsafeFile, src, max)
			}
			if _, err := w.Write(buf[:r]); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

func kindOf(m fs.FileMode) string {
	switch {
	case m&fs.ModeSymlink != 0:
		return "link"
	case m&fs.ModeNamedPipe != 0:
		return "named pipe"
	case m&fs.ModeDevice != 0:
		return "device"
	case m&fs.ModeSocket != 0:
		return "socket"
	case m.IsDir():
		return "folder"
	}
	return "special file"
}
