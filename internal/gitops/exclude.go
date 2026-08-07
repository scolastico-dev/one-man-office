package gitops

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Exclude adds a pattern to a repository's .git/info/exclude — the private,
// untracked counterpart to .gitignore. omo uses it when an office lives
// inside the repo it works on, so its own state never shows up in the user's
// `git status` or gets swept into a developer's commit.
//
// .gitignore is deliberately left alone: it is the user's tracked file.
func (g *Git) Exclude(repo, pattern string) error {
	l := g.repoLock(repo)
	l.Lock()
	defer l.Unlock()

	gitDir, err := resolveGitDir(repo)
	if err != nil {
		return err
	}
	info := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		return err
	}
	path := filepath.Join(info, "exclude")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == pattern {
			return nil // already excluded
		}
	}
	body := string(raw)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += "# added by omo: this office's own state\n" + pattern + "\n"
	return os.WriteFile(path, []byte(body), 0o644)
}

// resolveGitDir finds a repository's git directory. In a normal clone .git is
// a directory; in a worktree or submodule it is a file pointing elsewhere.
func resolveGitDir(repo string) (string, error) {
	p := filepath.Join(repo, ".git")
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("%s is not a git repository: %w", repo, err)
	}
	if fi.IsDir() {
		return p, nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(raw)), "gitdir:"))
	if line == "" {
		return "", fmt.Errorf("%s: unreadable .git file", repo)
	}
	if !filepath.IsAbs(line) {
		line = filepath.Join(repo, line)
	}
	return line, nil
}
