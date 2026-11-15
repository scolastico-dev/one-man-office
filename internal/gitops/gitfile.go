package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrForeignGitFile marks a worktree whose .git entry is not the gitfile git
// wrote for it: a directory, a link, a file pointing elsewhere or one
// carrying more than the path.
var ErrForeignGitFile = errors.New("the worktree's .git is not the file git wrote for it")

// CheckWorktreeGitFile checks that the .git entry at the top of the worktree
// dir of the repository at repo is still the gitfile git wrote: a regular
// file whose whole content is "gitdir: <path>\n", the path naming the git
// directory the repository records for dir. omo itself never lets git read
// that entry; this is for programs that do, such as a user's shell whose
// prompt runs git status there. An agent that replaced the entry by a
// repository of its own could otherwise have its config (an fsmonitor hook,
// a filter) run as the user. Errors wrap ErrForeignGitFile, or
// ErrNotRecorded when the repository does not record dir.
func (g *Git) CheckWorktreeGitFile(ctx context.Context, repo, dir string) error {
	common, err := g.commonDir(ctx, repo)
	if err != nil {
		return err
	}
	cand, err := plainFolder(dir)
	if err != nil {
		return err
	}
	record, err := recordOf(common, cand)
	if err != nil {
		return err
	}
	if record == common {
		return fmt.Errorf("%w: %s is the repository's own folder", ErrForeignGitFile, dir)
	}
	path := filepath.Join(cand, ".git")
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is missing or not a regular file", ErrForeignGitFile, path)
	}
	raw, err := readSmallFile(path, 1<<12)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrForeignGitFile, path, err)
	}
	target, ok := strings.CutPrefix(string(raw), "gitdir: ")
	if ok {
		target, ok = strings.CutSuffix(target, "\n")
	}
	if !ok || target == "" || strings.ContainsAny(target, "\r\n\x00") {
		return fmt.Errorf("%w: %s is not a gitfile", ErrForeignGitFile, path)
	}
	p := fromGitPath(target)
	if !filepath.IsAbs(p) {
		p = filepath.Join(cand, p)
	}
	p = filepath.Clean(p)
	if samePathString(p, record) {
		return nil
	}
	// git may have written the path through a link of the user's, outside
	// the worktree: the same folder is fine, a link inside the worktree is not.
	if p != cand && !within(cand, p) {
		if real, err := filepath.EvalSymlinks(p); err == nil && samePathString(real, record) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s points to %s, not to %s", ErrForeignGitFile, path, target, record)
}
