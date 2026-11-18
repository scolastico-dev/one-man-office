package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// isolatedEnv is the environment for both the test's own git commands and
// the Git under test: an empty HOME, no system or global config, a fixed
// identity and signing switched off through GIT_CONFIG_* variables.
func isolatedEnv(t *testing.T, extra ...string) []string {
	t.Helper()
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "HOME=") ||
			strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+t.TempDir(),
		"XDG_CONFIG_HOME="+t.TempDir(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_AUTHOR_NAME=omo-test", "GIT_AUTHOR_EMAIL=omo@test",
		"GIT_COMMITTER_NAME=omo-test", "GIT_COMMITTER_EMAIL=omo@test",
	)
	if len(extra) == 0 {
		extra = []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=false"}
	}
	return append(env, extra...)
}

var (
	versionOnce sync.Once
	versionErr  error
)

// requireGit skips when the installed git cannot run the code under test.
func requireGit(t *testing.T) {
	t.Helper()
	versionOnce.Do(func() {
		major, minor, err := New().Version()
		if err != nil {
			versionErr = err
			return
		}
		versionErr = CheckVersion(major, minor)
	})
	if versionErr != nil {
		t.Skipf("skipping: %v", versionErr)
	}
}

// testGit returns a Git that uses the isolated environment.
func testGit(t *testing.T) *Git {
	t.Helper()
	requireGit(t)
	g := New()
	g.SetEnvironment(isolatedEnv(t))
	return g
}

// gitWithGlobal returns a Git whose environment uses a global config file
// with content: the user's own settings, which omo keeps.
func gitWithGlobal(t *testing.T, content string, extra ...string) *Git {
	t.Helper()
	requireGit(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	write(t, global, content)
	g := New()
	env := append(isolatedEnv(t), "GIT_CONFIG_GLOBAL="+global)
	g.SetEnvironment(append(env, extra...))
	return g
}

// tg runs a git command for test setup in dir with the isolated
// environment and returns its output.
func tg(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = isolatedEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// tgFails runs a git command that is expected to fail.
func tgFails(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = isolatedEnv(t)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("git %v succeeded, want a failure\n%s", args, out)
	}
	return string(out)
}

func rev(t *testing.T, dir, r string) string {
	t.Helper()
	return strings.TrimSpace(tg(t, dir, "rev-parse", r))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newTestRepo creates a repository with one commit on main holding
// README.md, f.txt and other.txt, and a .gitignore that ignores build/.
func newTestRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	tg(t, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "README.md"), "hi\n")
	write(t, filepath.Join(dir, "f.txt"), "f1\n")
	write(t, filepath.Join(dir, "other.txt"), "o1\n")
	write(t, filepath.Join(dir, ".gitignore"), "build/\n")
	tg(t, dir, "add", ".")
	tg(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// commitOn commits files (path -> content) on branch, using a temporary
// worktree so the repository folder is not touched, and returns the commit.
func commitOn(t *testing.T, repo, branch, msg string, files map[string]string) string {
	t.Helper()
	if strings.TrimSpace(tg(t, repo, "branch", "--list", branch)) == "" {
		tg(t, repo, "branch", branch, "main")
	}
	// A branch checked out somewhere gets the commit there, touching only
	// the given files.
	wt, found, err := testGit(t).WorktreeOf(repo, branch)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		wt = filepath.Join(t.TempDir(), "c")
		tg(t, repo, "worktree", "add", "-q", wt, branch)
		defer tg(t, repo, "worktree", "remove", wt)
	}
	for p, c := range files {
		write(t, filepath.Join(wt, p), c)
		tg(t, wt, "add", "-f", "--", p)
	}
	tg(t, wt, "commit", "-q", "-m", msg, "--")
	return rev(t, wt, "HEAD")
}

// snapshot captures everything a merge must not change in a folder: HEAD,
// the branch, the status and the bytes of every file outside .git.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	s := map[string]string{
		"HEAD":   rev(t, dir, "HEAD"),
		"status": tg(t, dir, "status", "--porcelain=v1", "--untracked-files=all", "--ignored"),
	}
	s["branch"] = strings.TrimSpace(tg(t, dir, "branch", "--show-current"))
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			s["file:"+filepath.ToSlash(rel)] = read(t, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sameSnapshot(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s: %s changed:\nbefore: %q\nafter:  %q", what, k, v, after[k])
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("%s: %s appeared", what, k)
		}
	}
}

// refs lists every ref of the repository with its value.
func refs(t *testing.T, repo string) string {
	t.Helper()
	return tg(t, repo, "for-each-ref", "--format=%(refname) %(objectname)")
}

// noLeftovers fails when git state files of an interrupted operation exist
// in the git directory of dir.
func noLeftovers(t *testing.T, dir string) {
	t.Helper()
	gitDir := strings.TrimSpace(tg(t, dir, "rev-parse", "--absolute-git-dir"))
	for _, f := range []string{"index.lock", "HEAD.lock", "MERGE_HEAD", "MERGE_MSG", "AUTO_MERGE"} {
		if _, err := os.Stat(filepath.Join(gitDir, f)); err == nil {
			t.Errorf("%s left behind in %s", f, gitDir)
		}
	}
	locks, _ := filepath.Glob(filepath.Join(gitDir, "refs", "heads", "*.lock"))
	if len(locks) > 0 {
		t.Errorf("ref locks left behind: %v", locks)
	}
}

// addNewWorktree creates branch from base and checks it out in dir, as omo
// does for a ticket (AddWorktreeNewBranch with a fixed name).
func addNewWorktree(g *Git, repo, dir, branch, base string) error {
	_, err := g.AddWorktreeNewBranch(repo, dir, base, func(func(string) (bool, error)) (string, error) { return branch, nil })
	return err
}
