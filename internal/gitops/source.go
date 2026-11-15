package gitops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Sources are git repositories the user names (skill sets, plugins). omo
// checks one out into a folder of its own and reads files from it; the
// repository's owner is not trusted. Nothing in a repository can make the
// fetch or the checkout run a command:
//
//   - the address is https, ssh, scp-like or an absolute local path; no
//     "<transport>::" helper, no file://, git:// or http://, nothing that
//     starts with "-" (ValidSourceURL); git itself is limited to those
//     transports (GIT_ALLOW_PROTOCOL), also for redirects;
//   - the repository is created by omo (git init without templates) and
//     filled by git fetch, so no hook, config or alternates of the remote
//     arrive; hooks are switched off on the command line anyway;
//   - .git/info/attributes, which takes precedence over the repository's
//     own .gitattributes, unsets every filter, so no clean or smudge
//     program of the user's config (git-lfs included) runs on the content;
//   - no submodules, no file system monitor, no terminal or askpass prompt;
//   - links are checked out as plain files holding the link's target
//     (core.symlinks=false), so a checkout holds no link out of itself;
//   - the fetch is bounded in time (the operation deadline, 5 minutes) and
//     the new folder in size (MaxSourceBytes).
//
// Callers read files of a checkout with homefile.Read (bounded, regular
// files only, no link at the last element): the checkout lies in the omo
// home, which agents can write to.

// MaxSourceBytes bounds the size of a checkout while it is fetched. It is a
// variable so tests can lower it.
var MaxSourceBytes int64 = 256 << 20

var (
	// ErrBadSource marks an address omo does not fetch from.
	ErrBadSource = errors.New("not an address omo fetches from")
	// ErrBadRef marks a branch or tag name omo does not use.
	ErrBadRef = errors.New("not a branch or tag name omo uses")
	// ErrSourceTooLarge marks a fetch stopped at MaxSourceBytes.
	ErrSourceTooLarge = errors.New("the repository is larger than omo checks out")
	// ErrNoSourceCommit marks a folder that holds no checkout FetchSource
	// made.
	ErrNoSourceCommit = errors.New("no checked-out commit")
)

// allowedProtocols is GIT_ALLOW_PROTOCOL for every fetch and push: git
// refuses every other transport, also when a server redirects there.
const allowedProtocols = "https:ssh:file"

// scpLike is [user@]host:path, git's short form of an ssh address.
var scpLike = regexp.MustCompile(`^(?:([A-Za-z0-9._~+-]+)@)?([A-Za-z0-9._-]+|\[[0-9A-Fa-f:.]+\]):(.+)$`)

// ValidSourceURL accepts https://, ssh://, scp-like user@host:path and
// absolute local paths; it refuses ext::, fd::, any "<transport>::" address,
// file://, git:// and http:// (credentials travel in clear), a leading "-",
// and control or hidden characters (textsafe). Errors never repeat the
// address's user or password.
func ValidSourceURL(u string) error {
	bad := func(why string) error {
		return fmt.Errorf("%w: %s: %s", ErrBadSource, HideCredentials(oneLine(u)), why)
	}
	if u == "" {
		return fmt.Errorf("%w: the address is empty", ErrBadSource)
	}
	for _, r := range u {
		if textsafe.IsHidden(r) {
			return fmt.Errorf("%w: the address holds a control or hidden character", ErrBadSource)
		}
	}
	if strings.HasPrefix(u, "-") {
		return bad("it starts with \"-\"")
	}
	if transportHelper(u) {
		return bad("addresses of the form <transport>::<address> run a program")
	}
	if i := strings.Index(u, "://"); i >= 0 {
		scheme := u[:i]
		switch scheme {
		case "https", "ssh":
		case "http", "git":
			return bad(scheme + ":// sends everything, credentials included, in clear; use https:// or ssh://")
		case "file":
			return bad("give a local repository as an absolute path")
		default:
			return bad("only https://, ssh://, user@host:path and absolute paths are fetched")
		}
		p, err := url.Parse(u)
		if err != nil || p.Host == "" || p.Opaque != "" {
			return bad("it is not a valid address")
		}
		if strings.HasPrefix(p.Hostname(), "-") || (p.User != nil && strings.HasPrefix(p.User.Username(), "-")) {
			return bad("its host or user starts with \"-\"")
		}
		return nil
	}
	if isAbsLocal(u) {
		return nil
	}
	if m := scpLike.FindStringSubmatch(u); m != nil && !strings.Contains(m[2], "/") {
		if strings.HasPrefix(m[1], "-") || strings.HasPrefix(m[2], "-") {
			return bad("its host or user starts with \"-\"")
		}
		return nil
	}
	return bad("only https://, ssh://, user@host:path and absolute paths are fetched")
}

// transportHelper reports whether git would read u as <transport>::<address>
// and hand it to the program git-remote-<transport>: a run of URL scheme
// characters at the start followed by "::".
func transportHelper(u string) bool {
	i := 0
	for i < len(u) {
		c := u[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && (c >= '0' && c <= '9' || c == '+' || c == '.' || c == '-') {
			i++
			continue
		}
		break
	}
	return i > 0 && strings.HasPrefix(u[i:], "::")
}

// isAbsLocal reports whether u is an absolute path of this system.
func isAbsLocal(u string) bool {
	return filepath.IsAbs(u) && !strings.Contains(u, "://")
}

// ValidRef accepts "" (the remote's default branch) or a name
// check-ref-format accepts as a branch, not starting with "-" or "+",
// without "@{" or "..", and without control or hidden characters.
func ValidRef(ref string) error {
	if ref == "" {
		return nil
	}
	if !validRefName(ref) {
		return fmt.Errorf("%w: %q", ErrBadRef, oneLine(ref))
	}
	return nil
}

// validRefName applies the rules of git check-ref-format to refs/heads/<ref>
// (see git help check-ref-format), and refuses a leading "-" or "+" and
// every character of textsafe.
func validRefName(ref string) bool {
	if ref == "" || len(ref) > 255 || ref == "@" {
		return false
	}
	if strings.HasPrefix(ref, "-") || strings.HasPrefix(ref, "+") || strings.HasPrefix(ref, "/") ||
		strings.HasSuffix(ref, "/") || strings.HasSuffix(ref, ".") ||
		strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.Contains(ref, "//") {
		return false
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f || textsafe.IsHidden(r) || strings.ContainsRune(" ~^:?*[\\", r) {
			return false
		}
	}
	for _, c := range strings.Split(ref, "/") {
		if c == "" || strings.HasPrefix(c, ".") || strings.HasSuffix(c, ".lock") {
			return false
		}
	}
	return true
}

// commitID matches a full SHA-1 or SHA-256 object id in lower case.
var commitID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// sourceConfig is put before the subcommand of every git command run on a
// checkout FetchSource makes.
var sourceConfig = []string{
	"-c", "core.hooksPath=" + os.DevNull,
	"-c", "core.fsmonitor=false",
	"-c", "core.symlinks=false",
	"-c", "core.askPass=",
	"-c", "submodule.recurse=false",
	"-c", "fetch.recurseSubmodules=false",
	"-c", "fetch.fsckObjects=true",
	"-c", "transfer.fsckObjects=true",
	"-c", "advice.detachedHead=false",
}

// noPromptEnv keeps git, ssh and credential managers from asking anyone
// for anything: a fetch or push that needs a password the user's
// credential helpers do not hold fails instead of hanging the server.
var noPromptEnv = []string{
	"GIT_TERMINAL_PROMPT=0",
	"GIT_ASKPASS=",
	"SSH_ASKPASS=",
	"SSH_ASKPASS_REQUIRE=never",
	"GCM_INTERACTIVE=never",
	"GIT_ALLOW_PROTOCOL=" + allowedProtocols,
}

// checkoutAttributes, written to .git/info/attributes, takes precedence
// over the repository's own attributes and unsets every filter (its clean,
// smudge and process programs), line-ending and encoding conversion.
const checkoutAttributes = "* -filter -text -eol -ident -working-tree-encoding\n"

// FetchSource clones ref of url shallowly into a new folder next to dir and
// renames it over dir (the old folder is moved aside first and removed
// after); it returns the checked-out commit. ref may also be a full commit
// id, which is fetched and checked to be what was checked out. No hooks, no
// submodules, no filters, no links, no prompt, GIT_ALLOW_PROTOCOL=
// https:ssh:file, the operation deadline (5 minutes) and MaxSourceBytes.
// dir's parent must exist and be a folder omo owns outside the folders
// agents control (ErrAgentFolder). Errors never repeat the address's user
// or password.
func (g *Git) FetchSource(ctx context.Context, url, ref, dir string) (commit string, err error) {
	if err := ValidSourceURL(url); err != nil {
		return "", err
	}
	if err := ValidRef(ref); err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("the checkout folder %s is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	parent, err := plainFolder(filepath.Dir(dir))
	if err != nil {
		return "", fmt.Errorf("the folder for the checkout: %w", err)
	}
	dir = filepath.Join(parent, filepath.Base(dir))
	if err := g.checkDiscovery(dir); err != nil {
		return "", err
	}
	if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
		return "", fmt.Errorf("%w: %s", ErrNotAFolder, dir)
	}

	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(dir)+".new-")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(tmp)
		}
	}()

	ctx, cancel := context.WithTimeoutCause(ctx, g.op, ErrTimeout)
	defer cancel()
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	watchDone := make(chan struct{})
	go watchSize(ctx, tmp, MaxSourceBytes, stop, watchDone)
	defer func() { stop(nil); <-watchDone }()

	commit, err = g.fetchInto(ctx, tmp, url, ref)
	if err != nil {
		return "", sourceError(ctx, url, err)
	}
	stop(nil)
	<-watchDone
	if size, _ := folderSize(tmp, MaxSourceBytes); size > MaxSourceBytes {
		return "", fmt.Errorf("%w (more than %d bytes)", ErrSourceTooLarge, MaxSourceBytes)
	}
	if err := swapIn(tmp, dir); err != nil {
		return "", err
	}
	keep = true
	return commit, nil
}

// fetchInto makes a repository in the empty folder tmp, fetches ref of url
// into it and checks the commit out, detached.
func (g *Git) fetchInto(ctx context.Context, tmp, url, ref string) (string, error) {
	gitDir := filepath.Join(tmp, ".git")
	env := append([]string{
		"GIT_DIR=" + gitDir,
		"GIT_WORK_TREE=" + tmp,
		"GIT_CEILING_DIRECTORIES=" + filepath.Dir(tmp),
		"GIT_LFS_SKIP_SMUDGE=1",
	}, noPromptEnv...)
	run := func(args ...string) (string, error) {
		out, _, err := g.exec(cmdOpts{ctx: ctx, timeout: g.op, dir: tmp, env: env, cLocale: true},
			append(append([]string(nil), sourceConfig...), args...)...)
		return out, err
	}
	if _, err := run("init", "--quiet", "--template="); err != nil {
		return "", err
	}
	info := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(info, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(info, "attributes"), []byte(checkoutAttributes), 0o600); err != nil {
		return "", err
	}
	want := ref
	if want == "" {
		want = "HEAD"
	}
	if _, err := run("fetch", "--quiet", "--depth=1", "--no-tags", "--no-recurse-submodules", "--no-write-commit-graph",
		"--end-of-options", url, want); err != nil {
		return "", err
	}
	out, err := run("rev-parse", "--verify", "--quiet", "--end-of-options", "FETCH_HEAD^{commit}")
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(out)
	if !commitID.MatchString(commit) {
		return "", fmt.Errorf("git named %q as the fetched commit", oneLine(commit))
	}
	if commitID.MatchString(ref) && commit != ref {
		return "", fmt.Errorf("the remote gave commit %s for %s", commit, ref)
	}
	if _, err := run("checkout", "--quiet", "--detach", "--end-of-options", commit); err != nil {
		return "", err
	}
	got, err := readHead(gitDir)
	if err != nil {
		return "", err
	}
	if got != commit {
		return "", fmt.Errorf("the checkout is at %s, not at the fetched commit %s", got, commit)
	}
	return commit, nil
}

// sourceError turns a failed fetch into an error that names the address
// without credentials and keeps the cause (ErrSourceTooLarge, ErrTimeout,
// the caller's context) for errors.Is. git's own message is kept with every
// URL in it cleaned.
func sourceError(ctx context.Context, url string, err error) error {
	where := HideCredentials(oneLine(url))
	if cause := context.Cause(ctx); cause != nil {
		switch {
		case errors.Is(cause, ErrSourceTooLarge):
			return fmt.Errorf("fetching %s: %w (more than %d bytes)", where, ErrSourceTooLarge, MaxSourceBytes)
		case errors.Is(cause, ErrTimeout):
			return &cleanError{msg: fmt.Sprintf("fetching %s: timed out", where), err: ErrTimeout}
		default:
			return &cleanError{msg: fmt.Sprintf("fetching %s: %v", where, cause), err: cause}
		}
	}
	return &cleanError{msg: fmt.Sprintf("fetching %s failed: %s", where, gitMessage(err))}
}

// cleanError is an error whose text was cleaned of credentials. It wraps
// only a sentinel, never git's error (which repeats its arguments).
type cleanError struct {
	msg string
	err error
}

func (e *cleanError) Error() string { return e.msg }
func (e *cleanError) Unwrap() error { return e.err }

// gitMessage is what git said in err (from exec: the arguments line first,
// then stderr and stdout), without the arguments and with every URL
// cleaned of credentials, cut to a few lines.
func gitMessage(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if _, rest, ok := strings.Cut(text, "\n"); ok {
		text = rest
	} else if i := strings.LastIndex(text, ": "); i >= 0 {
		text = text[i+2:]
	}
	return gitText(text)
}

// gitText is git's output text with every URL cleaned of credentials,
// hints left out, cut to a few lines.
func gitText(text string) string {
	var lines []string
	for _, l := range strings.Split(HideCredentials(text), "\n") {
		l = strings.TrimSpace(oneLine(l))
		if l == "" || strings.HasPrefix(l, "hint:") {
			continue
		}
		lines = append(lines, l)
		if len(lines) == 6 {
			break
		}
	}
	if len(lines) == 0 {
		return "git failed"
	}
	return strings.Join(lines, "; ")
}

// oneLine replaces every textsafe character of s by a space.
func oneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if textsafe.IsHidden(r) {
			return ' '
		}
		return r
	}, s)
}

// watchSize stops the fetch (stop with ErrSourceTooLarge) when the folder
// dir grows past max; it returns when ctx ends, closing done.
func watchSize(ctx context.Context, dir string, max int64, stop context.CancelCauseFunc, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if size, _ := folderSize(dir, max); size > max {
				stop(ErrSourceTooLarge)
				return
			}
		}
	}
}

// folderSize adds up the sizes of the files below dir, stopping once the sum
// passes max. Files that vanish while it walks are skipped.
func folderSize(dir string, max int64) (int64, error) {
	var sum int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				sum += info.Size()
			}
		}
		if sum > max {
			return fs.SkipAll
		}
		return nil
	})
	return sum, err
}

// swapIn puts the folder tmp at dir: an existing dir is renamed aside
// first, tmp renamed to dir, the old folder removed after. When the second
// rename fails the old folder is put back.
func swapIn(tmp, dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return os.Rename(tmp, dir)
	} else if err != nil {
		return err
	}
	aside, err := os.MkdirTemp(filepath.Dir(dir), "."+filepath.Base(dir)+".old-")
	if err != nil {
		return err
	}
	old := filepath.Join(aside, "old")
	if err := os.Rename(dir, old); err != nil {
		_ = os.Remove(aside)
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if rerr := os.Rename(old, dir); rerr != nil {
			return fmt.Errorf("%v; the previous checkout is kept at %s", err, old)
		}
		_ = os.Remove(aside)
		return err
	}
	return os.RemoveAll(aside)
}

// SourceCommit reads the commit of a checkout made by FetchSource. It reads
// the checkout's .git/HEAD as a bounded regular file and runs no git, so a
// changed checkout cannot make it run anything.
func (g *Git) SourceCommit(ctx context.Context, dir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	gitDir := filepath.Join(dir, ".git")
	fi, err := os.Lstat(gitDir)
	if err != nil {
		return "", fmt.Errorf("%w in %s: %v", ErrNoSourceCommit, dir, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%w in %s: .git is not a folder", ErrNoSourceCommit, dir)
	}
	return readHead(gitDir)
}

// readHead reads the detached HEAD of gitDir.
func readHead(gitDir string) (string, error) {
	raw, err := readSmallFile(filepath.Join(gitDir, "HEAD"), 4096)
	if err != nil {
		return "", fmt.Errorf("%w in %s: %v", ErrNoSourceCommit, filepath.Dir(gitDir), err)
	}
	head := strings.TrimSpace(string(raw))
	if !commitID.MatchString(head) {
		return "", fmt.Errorf("%w in %s: HEAD is not a detached commit", ErrNoSourceCommit, filepath.Dir(gitDir))
	}
	return head, nil
}
