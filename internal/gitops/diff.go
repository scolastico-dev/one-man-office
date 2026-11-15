package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// DiffLimits bounds what a diff result carries. A zero or negative value
// means the default of DefaultDiffLimits.
type DiffLimits struct {
	// MaxFiles is the number of files listed.
	MaxFiles int
	// MaxTotalPatch is the sum of all patch bytes returned.
	MaxTotalPatch int
	// MaxFilePatch is the patch bytes returned for one file.
	MaxFilePatch int
}

// DefaultDiffLimits is what the board shows in the list view.
var DefaultDiffLimits = DiffLimits{MaxFiles: 3000, MaxTotalPatch: 2 << 20, MaxFilePatch: 256 << 10}

const (
	// MaxSingleFilePatch bounds the patch of a single-file request.
	MaxSingleFilePatch = 1 << 20
	// DiffTimeout bounds every git call of this file.
	DiffTimeout = 20 * time.Second
	// MaxPatchLine bounds one line of a patch (the sign included); a longer
	// line is cut and its file marked Truncated.
	MaxPatchLine = 4096

	// listReadCap bounds the bytes read from a file list.
	listReadCap = 8 << 20
	// statusReadCap bounds the bytes read from a status listing.
	statusReadCap = 8 << 20
	// hardPatchCap bounds the bytes read from one patch call whatever the
	// limits say.
	hardPatchCap = 64 << 20
)

// ErrNotInDiff is returned for a file that git's own list of the diff does
// not hold.
var ErrNotInDiff = errors.New("the file is not part of this diff")

// ErrNoBranch is wrapped by the error for a branch (or base) that does not
// exist in the repository.
var ErrNoBranch = errors.New("no such branch")

// FileChange is one file of a diff.
type FileChange struct {
	// Path is the file's path as git lists it (its new path for a rename).
	// It is raw bytes from the repository: it may hold control characters,
	// quotes, bidirectional overrides or bytes that are not UTF-8.
	Path string
	// OldPath is set for a rename or copy.
	OldPath string
	// Status is added, modified, deleted, renamed, copied, type_changed or
	// untracked.
	Status string
	// Binary marks a file git treats as binary; it has no patch.
	Binary    bool
	Additions int
	Deletions int
	// Patch is the unified patch, valid UTF-8 with no line over
	// MaxPatchLine bytes. It is nil for a binary file, an untracked file and
	// a file left out (see Truncated).
	Patch []byte
	// Truncated says the patch is cut short or left out because of a limit
	// (or because it could not be matched safely to this file).
	Truncated bool
}

// DiffResult is a bounded diff.
type DiffResult struct {
	Files []FileChange
	// MoreFiles says files beyond the listed ones exist.
	MoreFiles bool
	// MergeBase is the common ancestor a branch diff starts at; empty for a
	// worktree diff.
	MergeBase string
	// Head is the commit the diff ends at: the branch tip, or HEAD of a
	// worktree.
	Head string
}

func (l DiffLimits) fill() DiffLimits {
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultDiffLimits.MaxFiles
	}
	if l.MaxTotalPatch <= 0 {
		l.MaxTotalPatch = DefaultDiffLimits.MaxTotalPatch
	}
	if l.MaxFilePatch <= 0 {
		l.MaxFilePatch = DefaultDiffLimits.MaxFilePatch
	}
	return l
}

// diffGitPrefix is put before the subcommand of every call of this file: no
// optional locks (so nothing refreshes the index), no file system monitor,
// no hooks, literal output of file names.
var diffGitPrefix = []string{
	"--no-optional-locks",

	"-c", "core.quotePath=off", "-c", "core.fsmonitor=false",
	"-c", "core.hooksPath=" + os.DevNull,
	"-c", "diff.noprefix=false", "-c", "diff.mnemonicPrefix=false",
	"-c", "diff.submodule=short",
}

// diffFlags is what every diff call carries after the subcommand.
var diffFlags = []string{
	"--no-ext-diff", "--no-textconv", "--no-color", "-M", "--ignore-submodules=dirty",
	"--src-prefix=a/", "--dst-prefix=b/",
}

// limitedWriter keeps at most max bytes and, once more arrives, drops the
// rest and calls onCut once.
type limitedWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	max   int
	cut   bool
	onCut func()
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cut {
		return len(p), nil
	}
	room := w.max - w.buf.Len()
	if len(p) > room {
		w.buf.Write(p[:room])
		w.cut = true
		if w.onCut != nil {
			w.onCut()
		}
		return len(p), nil
	}
	w.buf.Write(p)
	return len(p), nil
}

// runCapped runs a read-only git call in the repository folder dir and
// keeps at most max bytes of its output. When more would come, it ends the
// process and reports cut (not an error). The call ends with ctx or after
// DiffTimeout, its process group with it; a timeout is an error wrapping
// ErrTimeout, an ended ctx one wrapping ctx's error.
func (g *Git) runCapped(ctx context.Context, dir string, max int, args ...string) (out []byte, cut bool, err error) {
	return g.runCappedFor(ctx, DiffTimeout, nil, inRepo(dir), max, args...)
}

// runCappedAt is runCapped at a repository folder or resolved worktree, with
// extra environment entries.
func (g *Git) runCappedAt(ctx context.Context, at where, env []string, max int, args ...string) (out []byte, cut bool, err error) {
	return g.runCappedFor(ctx, DiffTimeout, env, at, max, args...)
}

func (g *Git) runCappedFor(ctx context.Context, d time.Duration, extraEnv []string, at where, max int, args ...string) (out []byte, cut bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	tctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	full := append(append([]string(nil), diffGitPrefix...), args...)
	env := append([]string{"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_EXTERNAL_DIFF=", "LC_ALL=C", "LANGUAGE="}, extraEnv...)
	cmd, err := g.command(tctx, at, env, full)
	if err != nil {
		return nil, false, err
	}
	stdout := &limitedWriter{max: max, onCut: cancel}
	stderr := &limitedWriter{max: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	afterWait := killWithChildren(cmd, g.grace)
	cmd.WaitDelay = g.grace
	runErr := cmd.Run()
	afterWait()
	stdout.mu.Lock()
	out, cut = append([]byte(nil), stdout.buf.Bytes()...), stdout.cut
	stdout.mu.Unlock()
	if cut {
		return out, true, nil
	}
	if runErr != nil {
		if perr := ctx.Err(); perr != nil {
			return nil, false, fmt.Errorf("git %s: %w", subcommand(args), perr)
		}
		if errors.Is(tctx.Err(), context.DeadlineExceeded) {
			return nil, false, fmt.Errorf("git %s: %w after %s", subcommand(args), ErrTimeout, d)
		}
		stderr.mu.Lock()
		msg := strings.TrimSpace(stderr.buf.String())
		stderr.mu.Unlock()
		return nil, false, fmt.Errorf("git %s: %w: %s", subcommand(args), runErr, msg)
	}
	return out, false, nil
}

// subcommand names the git subcommand of args (skipping -c pairs).
func subcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		if !strings.HasPrefix(args[i], "-") {
			return args[i]
		}
	}
	return "?"
}

// resolveCommit resolves a branch or revision name to a commit id. A name
// that does not exist wraps ErrNoBranch.
func (g *Git) resolveCommit(ctx context.Context, repo, name string) (string, error) {
	if err := g.checkBranchName(repo, name); err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoBranch, err)
	}
	out, _, err := g.runCapped(ctx, repo, 4096, "rev-parse", "--verify", "--quiet", "--end-of-options", name+"^{commit}")
	if err != nil {
		if exitCode(err) == 1 {
			return "", fmt.Errorf("%w: %s", ErrNoBranch, displayPath(name))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// BranchDiff lists what branch changed since it left base (the diff from
// their merge base to the branch tip) with bounded patches. It only reads.
func (g *Git) BranchDiff(ctx context.Context, repo, base, branch string, lim DiffLimits) (DiffResult, error) {
	lim = lim.fill()
	mb, head, err := g.branchEnds(ctx, repo, base, branch)
	if err != nil {
		return DiffResult{}, err
	}
	files, more, err := g.collect(ctx, inRepo(repo), nil, []string{"--end-of-options", mb, head}, lim, nil)
	if err != nil {
		return DiffResult{}, err
	}
	return DiffResult{Files: files, MoreFiles: more, MergeBase: mb, Head: head}, nil
}

func (g *Git) branchEnds(ctx context.Context, repo, base, branch string) (mb, head string, err error) {
	baseID, err := g.resolveCommit(ctx, repo, base)
	if err != nil {
		return "", "", err
	}
	head, err = g.resolveCommit(ctx, repo, branch)
	if err != nil {
		return "", "", err
	}
	out, _, err := g.runCapped(ctx, repo, 4096, "merge-base", "--end-of-options", baseID, head)
	if err != nil {
		if exitCode(err) == 1 {
			return "", "", fmt.Errorf("%s and %s have no common history", displayPath(branch), displayPath(base))
		}
		return "", "", err
	}
	return strings.TrimSpace(string(out)), head, nil
}

// RemoteBase is the remote-tracking branch of a local branch as the
// repository knows it (nothing is fetched), and how far apart they are.
type RemoteBase struct {
	// Ref is the full name (refs/remotes/origin/main), Name the short one
	// (origin/main). Ref can be given to BranchDiff and BranchFileDiff as
	// the base.
	Ref, Name string
	// Behind counts the commits of Ref the local branch does not have,
	// Ahead the local branch's commits Ref does not have.
	Behind, Ahead int
}

// BaseRemote returns the remote-tracking branch that is the upstream of the
// local branch base in the repository at repo, from what the repository
// already has; it never fetches. ok is false when base has no upstream, the
// upstream is not a remote-tracking branch or the repository does not have
// it. A base that is no local branch wraps ErrNoBranch. It only reads.
func (g *Git) BaseRemote(ctx context.Context, repo, base string) (rb RemoteBase, ok bool, err error) {
	if err := g.checkBranchName(repo, base); err != nil {
		return RemoteBase{}, false, fmt.Errorf("%w: %v", ErrNoBranch, err)
	}
	local := "refs/heads/" + base
	// The pattern also matches branches below base (base/x); the line of
	// base itself is picked by its full name.
	out, _, err := g.runCapped(ctx, repo, 64<<10, "for-each-ref", "--format=%(refname)%00%(upstream)", "--end-of-options", local)
	if err != nil {
		return RemoteBase{}, false, err
	}
	found := false
	var upstream string
	for _, line := range strings.Split(string(out), "\n") {
		if name, up, cut := strings.Cut(line, "\x00"); cut && name == local {
			found, upstream = true, up
		}
	}
	if !found {
		return RemoteBase{}, false, fmt.Errorf("%w: %s", ErrNoBranch, displayPath(base))
	}
	short, isRemote := strings.CutPrefix(upstream, "refs/remotes/")
	if !isRemote || short == "" {
		return RemoteBase{}, false, nil
	}
	localID, err := g.resolveCommit(ctx, repo, local)
	if err != nil {
		return RemoteBase{}, false, err
	}
	upID, err := g.resolveCommit(ctx, repo, upstream)
	if errors.Is(err, ErrNoBranch) {
		return RemoteBase{}, false, nil
	}
	if err != nil {
		return RemoteBase{}, false, err
	}
	counts, _, err := g.runCapped(ctx, repo, 4096, "rev-list", "--left-right", "--count", "--end-of-options", localID+"..."+upID)
	if err != nil {
		return RemoteBase{}, false, err
	}
	ahead, behind, cut := strings.Cut(strings.TrimSpace(string(counts)), "\t")
	a, aerr := strconv.Atoi(ahead)
	b, berr := strconv.Atoi(behind)
	if !cut || aerr != nil || berr != nil {
		return RemoteBase{}, false, fmt.Errorf("git rev-list: unexpected count %q", strings.TrimSpace(string(counts)))
	}
	return RemoteBase{Ref: upstream, Name: short, Behind: b, Ahead: a}, true, nil
}

// BranchFileDiff returns the change of one file of BranchDiff's list, with
// a patch of up to MaxSingleFilePatch bytes. A path that git's own list of
// the diff does not hold gives ErrNotInDiff.
func (g *Git) BranchFileDiff(ctx context.Context, repo, base, branch, path string) (FileChange, error) {
	mb, head, err := g.branchEnds(ctx, repo, base, branch)
	if err != nil {
		return FileChange{}, err
	}
	return g.fileDiff(ctx, inRepo(repo), nil, []string{"--end-of-options", mb, head}, path, nil)
}

// WorktreeChanges lists the uncommitted changes of the worktree at dir of
// the repository at repo: tracked files (staged or not) against HEAD with
// bounded patches, then untracked files by name only (never read, ignored
// files left out). It only reads and refreshes nothing. The worktree is
// resolved from repo's records (see inWorktree): a folder repo does not
// record, a link and a worktree whose own config sets commands are refused.
func (g *Git) WorktreeChanges(ctx context.Context, repo, dir string, lim DiffLimits) (DiffResult, error) {
	lim = lim.fill()
	wt, head, env, cleanup, err := g.worktreeDiffSetup(ctx, repo, dir)
	if err != nil {
		return DiffResult{}, err
	}
	defer cleanup()
	at := wt.at()
	untracked := func(c context.Context, max int) ([]FileChange, bool, error) { return g.untracked(c, at, env, max) }
	files, more, err := g.collect(ctx, at, env, []string{"--end-of-options", head}, lim, untracked)
	if err != nil {
		return DiffResult{}, err
	}
	return DiffResult{Files: files, MoreFiles: more, Head: head}, nil
}

// WorktreeFileDiff returns the change of one file of WorktreeChanges' list.
// An untracked file comes back without a patch. A path that git's own list
// does not hold gives ErrNotInDiff.
func (g *Git) WorktreeFileDiff(ctx context.Context, repo, dir, path string) (FileChange, error) {
	wt, head, env, cleanup, err := g.worktreeDiffSetup(ctx, repo, dir)
	if err != nil {
		return FileChange{}, err
	}
	defer cleanup()
	at := wt.at()
	untracked := func(c context.Context, max int) ([]FileChange, bool, error) { return g.untracked(c, at, env, max) }
	return g.fileDiff(ctx, at, env, []string{"--end-of-options", head}, path, untracked)
}

// worktreeDiffSetup resolves the worktree, reads its HEAD and makes the
// private copy of its index.
func (g *Git) worktreeDiffSetup(ctx context.Context, repo, dir string) (wt *worktreeGit, head string, env []string, cleanup func(), err error) {
	wt, err = g.inWorktree(ctx, repo, dir)
	if err != nil {
		return nil, "", nil, nil, err
	}
	out, _, err := g.runCappedAt(ctx, wt.at(), nil, 4096, "rev-parse", "--verify", "--quiet", "--end-of-options", "HEAD^{commit}")
	if err != nil {
		if exitCode(err) == 1 {
			return nil, "", nil, nil, fmt.Errorf("%w: HEAD of %s", ErrNoBranch, displayPath(dir))
		}
		return nil, "", nil, nil, err
	}
	env, cleanup, err = scratchIndex(ctx, wt)
	if err != nil {
		return nil, "", nil, nil, err
	}
	return wt, strings.TrimSpace(string(out)), env, cleanup, nil
}

type untrackedFn func(ctx context.Context, max int) ([]FileChange, bool, error)

const singleListFiles = 200000

// fileDiff finds path in the list of the diff of revs and reads its patch.
func (g *Git) fileDiff(ctx context.Context, at where, env []string, revs []string, path string, untracked untrackedFn) (FileChange, error) {
	if path == "" || strings.ContainsRune(path, 0) {
		return FileChange{}, ErrNotInDiff
	}
	files, _, err := g.list(ctx, at, env, revs)
	if err != nil {
		return FileChange{}, err
	}
	for _, f := range files {
		if f.Path != path {
			continue
		}
		fc := f.FileChange
		if fc.Binary {
			return fc, nil
		}
		specs := []string{"--", ":(literal)" + fc.Path}
		if fc.OldPath != "" {
			specs = append(specs, ":(literal)"+fc.OldPath)
		}
		args := append(append([]string{"diff", "--patch", "--unified=3"}, diffFlags...), revs...)
		out, cut, err := g.runCappedAt(ctx, at, env, MaxSingleFilePatch*2+(1<<16), append(args, specs...)...)
		if err != nil {
			return FileChange{}, err
		}
		secs := splitSections(out)
		if len(secs) != 1 || (!cut && !headerMatches(secs[0], fc)) {
			fc.Truncated = true
			return fc, nil
		}
		p, trunc := boundSection(secs[0], MaxSingleFilePatch)
		fc.Patch, fc.Truncated = p, trunc || cut
		return fc, nil
	}
	if untracked != nil {
		ut, _, err := untracked(ctx, statusReadCap)
		if err != nil {
			return FileChange{}, err
		}
		for _, f := range ut {
			if f.Path == path {
				return f, nil
			}
		}
	}
	return FileChange{}, ErrNotInDiff
}

// listed is a file of git's list with what matching a patch needs.
type listed struct {
	FileChange
}

// list reads the file list of `git diff revs` (raw status and numstat, both
// NUL-separated). more is set when the output was cut.
func (g *Git) list(ctx context.Context, at where, env []string, revs []string) (files []listed, more bool, err error) {
	base := append([]string{"diff", "-z"}, diffFlags...)
	rawOut, rawCut, err := g.runCappedAt(ctx, at, env, listReadCap, append(append(append([]string(nil), base...), "--raw", "--no-abbrev"), append(revs, "--")...)...)
	if err != nil {
		return nil, false, err
	}
	numOut, numCut, err := g.runCappedAt(ctx, at, env, listReadCap, append(append(append([]string(nil), base...), "--numstat"), append(revs, "--")...)...)
	if err != nil {
		return nil, false, err
	}
	raws := parseRaw(rawOut)
	nums := parseNumstat(numOut)
	n := len(raws)
	if len(nums) < n {
		n = len(nums)
	}
	more = rawCut || numCut
	if len(raws) != len(nums) && !more {
		return nil, false, errors.New("the changes moved while they were read; try again")
	}
	for i := 0; i < n; i++ {
		r, m := raws[i], nums[i]
		if r.path != m.path {
			return nil, false, errors.New("the changes moved while they were read; try again")
		}
		fc := FileChange{Path: r.path, OldPath: r.old, Status: r.status, Binary: m.binary, Additions: m.add, Deletions: m.del}
		files = append(files, listed{fc})
	}
	return files, more, nil
}

type rawEntry struct {
	status    string
	old, path string
}

func statusName(letter byte) string {
	switch letter {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'T':
		return "type_changed"
	}
	return "modified"
}

// parseRaw reads `diff --raw -z` output; an incomplete last record (output
// that was cut) is dropped.
func parseRaw(out []byte) []rawEntry {
	toks := strings.Split(string(out), "\x00")
	toks = toks[:len(toks)-1] // a partial or empty remainder
	var res []rawEntry
	for i := 0; i < len(toks); {
		meta := strings.Fields(toks[i])
		if len(meta) < 5 || !strings.HasPrefix(meta[0], ":") || meta[4] == "" {
			break
		}
		letter := meta[4][0]
		if letter == 'R' || letter == 'C' {
			if i+2 >= len(toks) {
				break
			}
			res = append(res, rawEntry{status: statusName(letter), old: toks[i+1], path: toks[i+2]})
			i += 3
			continue
		}
		if i+1 >= len(toks) {
			break
		}
		res = append(res, rawEntry{status: statusName(letter), path: toks[i+1]})
		i += 2
	}
	return res
}

type numEntry struct {
	add, del int
	binary   bool
	path     string
}

// parseNumstat reads `diff --numstat -z` output; an incomplete last record
// is dropped.
func parseNumstat(out []byte) []numEntry {
	toks := strings.Split(string(out), "\x00")
	toks = toks[:len(toks)-1]
	var res []numEntry
	for i := 0; i < len(toks); {
		parts := strings.SplitN(toks[i], "\t", 3)
		if len(parts) != 3 {
			break
		}
		e := numEntry{}
		if parts[0] == "-" && parts[1] == "-" {
			e.binary = true
		} else {
			e.add, _ = strconv.Atoi(parts[0])
			e.del, _ = strconv.Atoi(parts[1])
		}
		if parts[2] == "" { // rename or copy: old and new follow
			if i+2 >= len(toks) {
				break
			}
			e.path = toks[i+2]
			i += 3
		} else {
			e.path = parts[2]
			i++
		}
		res = append(res, e)
	}
	return res
}

// collect lists the files of the diff of revs (up to lim.MaxFiles, then the
// untracked ones from extra), reads the patches and attaches them.
func (g *Git) collect(ctx context.Context, at where, env []string, revs []string, lim DiffLimits, extra untrackedFn) ([]FileChange, bool, error) {
	all, listCut, err := g.list(ctx, at, env, revs)
	if err != nil {
		return nil, false, err
	}
	more := listCut
	total := len(all)
	if total > lim.MaxFiles {
		all = all[:lim.MaxFiles]
		more = true
	}
	files := make([]FileChange, len(all))
	for i := range all {
		files[i] = all[i].FileChange
	}
	if len(files) > 0 {
		args := append(append([]string{"diff", "--patch", "--unified=3"}, diffFlags...), append(revs, "--")...)
		readCap := 4*lim.MaxTotalPatch + 1<<20
		if readCap > hardPatchCap || readCap < 0 {
			readCap = hardPatchCap
		}
		out, cut, err := g.runCappedAt(ctx, at, env, readCap, args...)
		if err != nil {
			return nil, false, err
		}
		attachPatches(files, total, out, cut || listCut, lim)
	}
	if extra != nil {
		ut, utCut, err := extra(ctx, statusReadCap)
		if err != nil {
			return nil, false, err
		}
		room := lim.MaxFiles - len(files)
		if utCut || len(ut) > room {
			more = true
		}
		if len(ut) > room {
			ut = ut[:room]
		}
		files = append(files, ut...)
	}
	return files, more, nil
}

// attachPatches splits the patch output into one section per file and
// attaches each to its file, in order. total is the number of files git
// listed (files may hold fewer). When the sections cannot be matched with
// certainty, no file gets a patch and each is marked Truncated.
func attachPatches(files []FileChange, total int, out []byte, partial bool, lim DiffLimits) {
	secs := splitSections(out)
	ok := true
	switch {
	case !partial && len(secs) != total:
		ok = false
	case len(secs) > total:
		ok = false
	}
	if ok {
		for i := range secs {
			if i >= len(files) {
				break
			}
			// A section cut by the read cap is not checked for its header
			// text beyond what arrived; headerMatches only needs line one.
			if !headerMatches(secs[i], files[i]) {
				ok = false
				break
			}
		}
	}
	if !ok {
		for i := range files {
			if !files[i].Binary {
				files[i].Truncated = true
			}
		}
		return
	}
	budget := lim.MaxTotalPatch
	for i := range files {
		f := &files[i]
		if f.Binary {
			continue
		}
		if i >= len(secs) {
			f.Truncated = true
			continue
		}
		p, trunc := boundSection(secs[i], lim.MaxFilePatch)
		// The last section of a cut read is itself cut.
		if partial && i == len(secs)-1 {
			trunc = true
		}
		if len(p) > budget {
			f.Truncated = true
			continue
		}
		budget -= len(p)
		f.Patch, f.Truncated = p, trunc
	}
}

var sectionMark = []byte("diff --git ")

// splitSections splits patch output at lines that start with "diff --git ".
// Content lines of a patch always start with a space, '+', '-' or a
// backslash, so a file's own text never looks like a header.
func splitSections(out []byte) [][]byte {
	var secs [][]byte
	start := -1
	for pos := 0; pos < len(out); {
		end := bytes.IndexByte(out[pos:], '\n')
		next := len(out)
		if end >= 0 {
			next = pos + end + 1
		}
		if bytes.HasPrefix(out[pos:next], sectionMark) {
			if start >= 0 {
				secs = append(secs, out[start:pos])
			}
			start = pos
		}
		pos = next
	}
	if start >= 0 {
		secs = append(secs, out[start:])
	}
	return secs
}

// plainName reports whether git prints name in a header without quoting or
// escapes.
func plainName(name string) bool {
	for i := 0; i < len(name); i++ {
		if c := name[i]; c < 0x20 || c == 0x7f || c == '"' || c == '\\' {
			return false
		}
	}
	return name != ""
}

// headerMatches checks the "diff --git" line of a section against its file
// when the names are plain; for other names it only checks the line start.
func headerMatches(sec []byte, f FileChange) bool {
	line := sec
	complete := false
	if i := bytes.IndexByte(sec, '\n'); i >= 0 {
		line, complete = sec[:i], true
	}
	if !bytes.HasPrefix(line, sectionMark) {
		return false
	}
	old := f.OldPath
	if old == "" {
		old = f.Path
	}
	if complete && plainName(f.Path) && plainName(old) {
		return string(line) == "diff --git a/"+old+" b/"+f.Path
	}
	return true
}

// boundSection makes a section fit: no line over MaxPatchLine bytes, valid
// UTF-8, at most max bytes, cut at a line end. trunc says something was cut.
func boundSection(sec []byte, max int) (patch []byte, trunc bool) {
	out := make([]byte, 0, min(len(sec), max))
	for pos := 0; pos < len(sec); {
		end := bytes.IndexByte(sec[pos:], '\n')
		next := len(sec)
		if end >= 0 {
			next = pos + end + 1
		}
		line := sec[pos:next]
		pos = next
		body, nl := line, ""
		if body[len(body)-1] == '\n' {
			body, nl = body[:len(body)-1], "\n"
		}
		if len(body) > MaxPatchLine {
			cutAt := MaxPatchLine
			for cutAt > 0 && !utf8.RuneStart(body[cutAt]) {
				cutAt--
			}
			body = body[:cutAt]
			trunc = true
		}
		clean := strings.ToValidUTF8(string(body), "�") + nl
		if len(out)+len(clean) > max {
			return out, true
		}
		out = append(out, clean...)
	}
	return out, trunc
}

// untracked lists the untracked, not ignored files of the worktree at.
func (g *Git) untracked(ctx context.Context, at where, env []string, max int) ([]FileChange, bool, error) {
	out, cut, err := g.runCappedAt(ctx, at, env, max, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=dirty", "--no-renames")
	if err != nil {
		return nil, false, err
	}
	toks := strings.Split(string(out), "\x00")
	toks = toks[:len(toks)-1]
	var files []FileChange
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if len(t) < 4 {
			continue
		}
		if t[0] == 'R' || t[0] == 'C' || t[1] == 'R' || t[1] == 'C' {
			i++ // the original name follows
		}
		if t[0] == '?' && t[1] == '?' {
			files = append(files, FileChange{Path: t[3:], Status: "untracked"})
		}
	}
	return files, cut, nil
}

// MaxIndexSize bounds the index file of a worktree that omo copies.
const MaxIndexSize = 256 << 20

// scratchIndex points git at a private copy of the index of the resolved
// worktree wt. A diff against the working tree refreshes the index it reads
// (and writes it back, even when told to take no optional locks); on the
// copy that write goes nowhere, so the repository is not touched. The index
// is the one in the git directory the repository records for the worktree;
// it must be a regular file (no link, device or pipe) of at most
// MaxIndexSize bytes, and the copy stops when ctx ends. The copy keeps the
// index's modification time. cleanup removes the copy. A worktree without
// an index file gets no copy.
func scratchIndex(ctx context.Context, wt *worktreeGit) (env []string, cleanup func(), err error) {
	src := filepath.Join(wt.gitDir, "index")
	before, err := os.Lstat(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil, func() {}, nil
	}
	tmp, err := os.MkdirTemp("", "omo-index-")
	if err != nil {
		return nil, nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmp) }
	path := filepath.Join(tmp, "index")
	if err := copyRegular(ctx, src, path, MaxIndexSize); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("the index of %s: %w", wt.dir, err)
	}
	// The copy keeps the index's time, read before the copy: git trusts a
	// file whose size and time match its entry only when the file is older
	// than the index, and a newer time on the copy would hide a same-size
	// change made in the second the index was written. Should the index
	// change during the copy, the older time only makes git look at more
	// files' contents.
	if before != nil {
		if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
			cleanup()
			return nil, nil, err
		}
	}
	return []string{"GIT_INDEX_FILE=" + path}, cleanup, nil
}
