package gitops

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FetchTarget fetches only refs/heads/<base> at the recorded remote URL,
// retaining it below refs/omo/export-target/. The user's checkout, index,
// branches, tracking refs and FETCH_HEAD are untouched. Like PushCommit,
// the network command uses a private bare repository, so only the user's
// global/system credential and transport settings apply, never repo's
// hooks, credential helpers, URL rewrites or remote configuration. Objects
// go directly into repo's object store; the private ref is then pinned in
// repo. A failed fetch never returns an old retained target.
func (g *Git) FetchTarget(ctx context.Context, repo string, r Remote, base string) (string, error) {
	if base == "" || ValidRef(base) != nil {
		return "", fmt.Errorf("%w: %q is not a target branch", ErrBadRef, oneLine(base))
	}
	if !remoteName.MatchString(r.Name) {
		return "", fmt.Errorf("%w: the remote name %q", ErrBadRemote, oneLine(r.Name))
	}
	if err := ValidSourceURL(r.URL); err != nil {
		return "", fmt.Errorf("%w: %v", ErrBadRemote, err)
	}
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	ctx, cancel := context.WithTimeoutCause(ctx, g.op, ErrTimeout)
	defer cancel()
	common, err := g.commonDir(ctx, repo)
	if err != nil {
		return "", err
	}
	objects := filepath.Join(common, "objects")
	if fi, err := os.Stat(objects); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("the repository %s has no objects folder", repo)
	}
	tmp, err := os.MkdirTemp("", "omo-export-target-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	env := append([]string{"GIT_DIR=" + tmp, "GIT_CEILING_DIRECTORIES=" + filepath.Dir(tmp)}, noPromptEnv...)
	o := cmdOpts{ctx: ctx, timeout: g.op, dir: tmp, env: env, cLocale: true}
	format, _, err := g.exec(cmdOpts{ctx: ctx, timeout: g.query, dir: repo}, "rev-parse", "--show-object-format")
	if err != nil {
		return "", err
	}
	if _, _, err := g.exec(o, append(append([]string(nil), pushConfig...), "init", "--quiet", "--bare", "--template=", "--object-format="+strings.TrimSpace(format))...); err != nil {
		return "", sourceError(ctx, r.URL, err)
	}
	// The hash prevents credentials and arbitrary branch path components
	// from entering the private ref name; distinct URLs/bases never collide.
	ref := fmt.Sprintf("refs/omo/export-target/%x", sha256.Sum256([]byte(r.URL+"\x00"+base)))
	o.env = append(o.env, "GIT_OBJECT_DIRECTORY="+objects)
	args := append(append([]string(nil), pushConfig...), "fetch", "--quiet", "--no-tags", "--no-recurse-submodules",
		"--no-write-fetch-head", "--no-write-commit-graph", "--no-auto-maintenance", "--end-of-options", r.URL, "refs/heads/"+base+":"+ref)
	g.at("fetch-target")
	if _, _, err := g.exec(o, args...); err != nil {
		if errors.Is(err, ErrTimeout) {
			return "", &cleanError{msg: "fetching " + r.Address() + ": timed out", err: ErrTimeout}
		}
		return "", sourceError(ctx, r.URL, err)
	}
	out, _, err := g.exec(o, "rev-parse", "--verify", "--quiet", "--end-of-options", ref+"^{commit}")
	if err != nil {
		return "", sourceError(ctx, r.URL, err)
	}
	commit := strings.TrimSpace(out)
	if !fullID(commit) {
		return "", fmt.Errorf("git named %q as the fetched target", oneLine(commit))
	}
	args = append(append([]string(nil), noHooks...), "update-ref", ref, commit)
	if _, _, err := g.exec(cmdOpts{ctx: ctx, timeout: g.op, dir: repo}, args...); err != nil {
		return "", err
	}
	return commit, nil
}

// ExportContained reports positive evidence that target contains the
// reviewed changes start..commit. Empty changes never count. An ancestry
// match covers ordinary/fast-forward merges; otherwise a merge using the
// recorded start as its base must leave target's tree unchanged. This also
// recognizes rebases and squash merges after unrelated target advances.
// Content proof uses the built-in text merger independently of configured
// merge drivers, union policies, attributes and normalization settings.
// Conflicts are inconclusive (false, nil). Missing commits and git failures
// are errors. All arguments must be full object IDs. Temporary merge objects
// are discarded; no refs, index, HEAD or working files are changed.
func (g *Git) ExportContained(repo, start, commit, target string) (bool, error) {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()
	for _, id := range []string{start, commit, target} {
		if !fullID(id) {
			return false, fmt.Errorf("%w: %q is not a full commit id", ErrNoCommit, oneLine(id))
		}
		if _, err := g.commitOf(repo, id); err != nil {
			return false, fmt.Errorf("%w: %s: %w", ErrNoCommit, id, err)
		}
	}
	trees := make([]string, 3)
	for i, id := range []string{start, commit, target} {
		out, _, err := g.exec(cmdOpts{timeout: g.query, dir: repo}, "rev-parse", "--verify", "--end-of-options", id+"^{tree}")
		if err != nil {
			return false, err
		}
		trees[i] = strings.TrimSpace(out)
	}
	if trees[0] == trees[1] {
		return false, nil
	}
	if ok, err := g.isAncestor(repo, commit, target); err != nil || ok {
		return ok, err
	}
	tree, err := g.containmentTree(repo, start, target, commit)
	if err != nil {
		return false, err
	}
	return tree == trees[2], nil
}
