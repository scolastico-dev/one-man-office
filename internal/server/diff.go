package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// diffSlots bounds the diffs read from git at once, server-wide.
const diffSlots = 4

// maxDiffMessage is the longest message of a failed state, in bytes.
const maxDiffMessage = 500

// Values of the base parameter of the diff routes.
const (
	compareLocal  = "local"
	compareRemote = "remote"
)

// diffBase reads the base parameter of the diff routes: local (the default)
// compares with the project's local base branch, remote with its
// remote-tracking branch.
func diffBase(r *http.Request) (remote bool, err error) {
	switch r.URL.Query().Get("base") {
	case "", compareLocal:
		return false, nil
	case compareRemote:
		return true, nil
	}
	return false, api.Errorf(api.CodeBadRequest, "base must be local or remote")
}

// errNoRemoteBase refuses base=remote for a base branch without a
// remote-tracking branch.
func errNoRemoteBase(base string) error {
	return api.Errorf(api.CodeValidation, "The base branch %s has no remote-tracking branch in this repository, so the diff cannot be compared with one.", escapeDiffPath(base))
}

// diffContext reads the context parameter of the single-file route: absent,
// the file's diff as in the list (cut to fit); a number of lines of context
// from 0 to gitops.MaxDiffContext, or "all" for the whole file, the patch
// read again with that much context and refused when it does not fit.
func diffContext(r *http.Request) (lines int, set bool, err error) {
	q := r.URL.Query()
	if !q.Has("context") {
		return 0, false, nil
	}
	v := q.Get("context")
	if v == "all" {
		return gitops.MaxDiffContext, true, nil
	}
	n, convErr := strconv.Atoi(v)
	if convErr != nil || n < 0 || n > gitops.MaxDiffContext || strconv.Itoa(n) != v {
		return 0, false, api.Errorf(api.CodeBadRequest, "context must be all or a number of lines from 0 to %d", gitops.MaxDiffContext)
	}
	return n, true, nil
}

// errTooLargeToExpand refuses a context request whose patch does not fit.
var errTooLargeToExpand = api.Errorf(api.CodeValidation, "This file is too large to show with that much context: its diff would be over %d MiB.", gitops.MaxSingleFilePatch>>20)

func (s *Server) registerDiff(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tickets/{n}/diff", s.getDiff)
	mux.HandleFunc("GET /api/tickets/{n}/diff/file", s.getDiffFile)
}

// diffTarget is one project of a ticket with what its diff is compared
// against: the base branch of a standalone task or story, the story's
// integration branch of the same project for a task in a story.
type diffTarget struct {
	project api.TicketProject
	base    string
	// closed is set for a ticket done as completed or exported: its diff
	// is the one recorded then (project.StartCommit, EndCommit).
	closed bool
}

// recorded reports whether the diff of tg runs between recorded commits.
func (tg diffTarget) recorded() bool { return tg.project.EndCommit != "" }

// errCommitsGone is the message of a recorded diff whose commits the
// repository lacks.
func errCommitsGone(p api.TicketProject) string {
	return fmt.Sprintf("The commits recorded for this diff when the ticket was done (%s..%s) are no longer in the project's repository.",
		shortID(p.StartCommit), shortID(p.EndCommit))
}

// shortID abbreviates a commit id for a message.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// notRecorded is said for a done ticket whose diff has no recorded commits.
const notRecorded = "omo recorded no commits for this diff when the ticket was done (it was done before omo recorded them)"

// diffTargets reads the ticket as the caller (so the caller's read scope
// decides) and returns its projects in order.
func (s *Server) diffTargets(r *http.Request, n int64) ([]diffTarget, error) {
	t, err := s.board.Get(r.Context(), actorFrom(r), n)
	if err != nil {
		return nil, err
	}
	var storyBranch map[string]string
	if t.Story != nil {
		// The task's scope already allows reading its story.
		story, err := s.board.Get(r.Context(), board.System, int64(*t.Story))
		if err != nil {
			return nil, err
		}
		storyBranch = map[string]string{}
		for _, p := range story.Projects {
			storyBranch[p.Project] = p.Branch
		}
	}
	closed := t.Column == api.ColumnDone && t.Resolution != api.ResolutionCancelled
	out := make([]diffTarget, 0, len(t.Projects))
	for _, p := range t.Projects {
		base := p.BaseBranch
		if storyBranch != nil {
			base = storyBranch[p.Project]
		}
		out = append(out, diffTarget{project: p, base: base, closed: closed})
	}
	return out, nil
}

// inDiffSlot runs fn when one of the diffSlots is free, with the time limit
// of a diff. Waiting for a slot ends with ctx.
func (s *Server) inDiffSlot(ctx context.Context, fn func(ctx context.Context) error) error {
	if s.diffQueued != nil {
		s.diffQueued()
	}
	select {
	case s.diffSem <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.diffSem }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.diffHook != nil {
		s.diffHook(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, gitops.DiffTimeout)
	defer cancel()
	return fn(ctx)
}

func (s *Server) getDiff(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	remote, err := diffBase(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	targets, err := s.diffTargets(r, n)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := api.TicketDiff{Ticket: int(n), Projects: make([]api.ProjectDiff, 0, len(targets))}
	// One project after the other: the semaphore bounds the server, not one
	// request.
	anyRemote := false
	for _, tg := range targets {
		pd := s.projectDiff(r.Context(), tg, remote)
		out.Projects = append(out.Projects, pd)
		if r.Context().Err() != nil {
			return // the client left
		}
		anyRemote = anyRemote || pd.RemoteBase != nil
	}
	if remote && !anyRemote {
		writeError(w, r, api.Errorf(api.CodeValidation, "No project of this ticket has a base branch with a remote-tracking branch in its repository, so the diff cannot be compared with one."))
		return
	}
	out.ComputedAt = time.Now().UTC()
	writeJSON(w, http.StatusOK, out)
}

// projectDiff computes the diff of one project; every way it can fail is a
// state of the answer. With remote it compares with the remote-tracking
// branch of the base branch when the repository has one, otherwise with the
// base branch.
func (s *Server) projectDiff(ctx context.Context, tg diffTarget, remote bool) api.ProjectDiff {
	pd := api.ProjectDiff{
		Project:      tg.project.Project,
		Base:         escapeDiffPath(tg.base),
		Branch:       escapeDiffPath(tg.project.Branch),
		State:        "ok",
		Files:        []api.DiffFile{},
		Uncommitted:  []api.DiffFile{},
		ComparedWith: compareLocal,
	}
	proj, ok := s.settings.Project(tg.project.Project)
	switch {
	case !ok:
		pd.State, pd.Message = "project_unavailable", "The project is not registered any more."
		return pd
	case proj.Missing:
		pd.State, pd.Message = "project_unavailable", "The project's folder is missing."
		return pd
	case tg.recorded():
		return s.recordedDiff(ctx, proj.Path, tg, pd)
	case tg.project.Branch == "" && tg.closed:
		// omo forgets a branch it deleted after the merge.
		pd.State, pd.Message = "branch_gone", "The branch of this ticket was deleted and "+notRecorded+", so the diff cannot be shown."
		return pd
	case tg.project.Branch == "":
		pd.State, pd.Message = "not_started", "Work on this ticket has not started in this project."
		return pd
	case tg.base == "":
		pd.State, pd.Message = "failed", "The branch to compare with is not known."
		return pd
	}

	var res gitops.DiffResult
	var goneBranch bool
	err := s.inDiffSlot(ctx, func(ctx context.Context) error {
		base := gitops.BranchRef(tg.base)
		// The remote-tracking branch as the repository knows it; a failure
		// to read it leaves it out.
		if rb, ok, err := s.git.BaseRemote(ctx, proj.Path, tg.base); err == nil && ok {
			pd.RemoteBase = &api.RemoteBase{Local: escapeDiffPath(tg.base), Name: escapeDiffPath(rb.Name), Behind: rb.Behind, Ahead: rb.Ahead}
			if remote {
				base = rb.Ref
				pd.Base, pd.ComparedWith = escapeDiffPath(rb.Name), compareRemote
			}
		}
		var err error
		res, err = s.git.BranchDiff(ctx, proj.Path, base, tg.project.Branch, gitops.DefaultDiffLimits)
		if errors.Is(err, gitops.ErrNoBranch) {
			exists, _ := s.git.BranchExists(proj.Path, tg.project.Branch)
			goneBranch = !exists
		}
		return err
	})
	switch {
	case err == nil:
		pd.BaseCommit, pd.HeadCommit = res.MergeBase, res.Head
		pd.Files, pd.FilesTruncated = diffFiles(res.Files), res.MoreFiles
		if tg.closed {
			pd.Message = "This compares the branch as it is now: " + notRecorded + "."
		}
	case ctx.Err() != nil:
		return pd // the client left; nobody reads this
	case errors.Is(err, gitops.ErrNoBranch) && goneBranch && tg.closed:
		pd.State = "branch_gone"
		pd.Message = fmt.Sprintf("The branch %s no longer exists and %s, so the diff cannot be shown.", escapeDiffPath(tg.project.Branch), notRecorded)
		return pd
	case errors.Is(err, gitops.ErrNoBranch) && goneBranch:
		pd.State = "branch_gone"
		pd.Message = fmt.Sprintf("The branch %s no longer exists; it was removed after the merge.", escapeDiffPath(tg.project.Branch))
	case errors.Is(err, gitops.ErrNoBranch):
		pd.State = "branch_gone"
		pd.Message = fmt.Sprintf("The branch to compare with, %s, no longer exists.", pd.Base)
	default:
		pd.State, pd.Message = "failed", oneLine(err.Error())
		return pd
	}

	if worktreeExists(tg.project.Worktree) {
		var wres gitops.DiffResult
		err := s.inDiffSlot(ctx, func(ctx context.Context) error {
			var err error
			wres, err = s.git.WorktreeChanges(ctx, proj.Path, tg.project.Worktree, gitops.DefaultDiffLimits)
			return err
		})
		switch {
		case err == nil:
			pd.Uncommitted, pd.UncommittedTruncated = diffFiles(wres.Files), wres.MoreFiles
		case ctx.Err() == nil && pd.State == "ok":
			pd.Message = "The uncommitted changes could not be read: " + oneLine(err.Error())
		}
	}
	return pd
}

// recordedDiff is projectDiff for a done ticket's recorded commits, read
// from the project's repository at repo: the branch, the worktree and the
// base branch as they are now play no part.
func (s *Server) recordedDiff(ctx context.Context, repo string, tg diffTarget, pd api.ProjectDiff) api.ProjectDiff {
	pd.Recorded = true
	pd.BaseCommit, pd.HeadCommit = tg.project.StartCommit, tg.project.EndCommit
	var res gitops.DiffResult
	err := s.inDiffSlot(ctx, func(ctx context.Context) error {
		var err error
		res, err = s.git.RangeDiff(ctx, repo, tg.project.StartCommit, tg.project.EndCommit, gitops.DefaultDiffLimits)
		return err
	})
	switch {
	case err == nil:
		pd.Files, pd.FilesTruncated = diffFiles(res.Files), res.MoreFiles
	case ctx.Err() != nil:
	case errors.Is(err, gitops.ErrNoCommit):
		pd.State, pd.Message = "commits_gone", errCommitsGone(tg.project)
	default:
		pd.State, pd.Message = "failed", oneLine(err.Error())
	}
	return pd
}

func (s *Server) getDiffFile(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	q := r.URL.Query()
	uncommitted := false
	switch q.Get("uncommitted") {
	case "", "false":
	case "true":
		uncommitted = true
	default:
		writeError(w, r, api.Errorf(api.CodeBadRequest, "uncommitted must be true or false"))
		return
	}
	remote, err := diffBase(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	lines, withContext, err := diffContext(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	shown := q.Get("path")
	if shown == "" {
		writeError(w, r, api.Errorf(api.CodeBadRequest, "path is required"))
		return
	}
	targets, err := s.diffTargets(r, n)
	if err != nil {
		writeError(w, r, err)
		return
	}
	// The project is looked up among the ticket's projects; nothing of the
	// request becomes a folder or a ref.
	var tg *diffTarget
	for i := range targets {
		if targets[i].project.Project == q.Get("project") {
			tg = &targets[i]
		}
	}
	if tg == nil {
		writeError(w, r, api.Errorf(api.CodeNotFound, "the ticket has no such project"))
		return
	}
	raw, ok := unescapeDiffPath(shown)
	if !ok {
		writeError(w, r, errNotInDiff)
		return
	}
	proj, ok := s.settings.Project(tg.project.Project)
	if !ok || proj.Missing || (!tg.recorded() && (tg.project.Branch == "" || tg.base == "")) {
		writeError(w, r, api.Errorf(api.CodeNotFound, "there is no diff for this project"))
		return
	}
	if uncommitted && tg.recorded() {
		writeError(w, r, api.Errorf(api.CodeNotFound, "the recorded diff of a done ticket has no uncommitted changes"))
		return
	}
	if uncommitted && !worktreeExists(tg.project.Worktree) {
		writeError(w, r, api.Errorf(api.CodeNotFound, "the ticket has no worktree"))
		return
	}

	var fc gitops.FileChange
	err = s.inDiffSlot(r.Context(), func(ctx context.Context) error {
		var err error
		if uncommitted {
			// The worktree is resolved from the project's records: a
			// folder the agent replaced by a link is refused.
			if withContext {
				fc, err = s.git.WorktreeFileContext(ctx, proj.Path, tg.project.Worktree, raw, lines)
			} else {
				fc, err = s.git.WorktreeFileDiff(ctx, proj.Path, tg.project.Worktree, raw)
			}
			return err
		}
		if tg.recorded() {
			// What the list showed: the recorded commits, whatever base
			// was asked for.
			if withContext {
				fc, err = s.git.RangeFileContext(ctx, proj.Path, tg.project.StartCommit, tg.project.EndCommit, raw, lines)
			} else {
				fc, err = s.git.RangeFileDiff(ctx, proj.Path, tg.project.StartCommit, tg.project.EndCommit, raw)
			}
			return err
		}
		base := gitops.BranchRef(tg.base)
		if remote {
			rb, ok, err := s.git.BaseRemote(ctx, proj.Path, tg.base)
			if err != nil {
				return err
			}
			if !ok {
				return errNoRemoteBase(tg.base)
			}
			base = rb.Ref
		}
		if withContext {
			fc, err = s.git.BranchFileContext(ctx, proj.Path, base, tg.project.Branch, raw, lines)
		} else {
			fc, err = s.git.BranchFileDiff(ctx, proj.Path, base, tg.project.Branch, raw)
		}
		return err
	})
	var apiErr *api.Error
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, diffFile(fc))
	case r.Context().Err() != nil:
		return // the client left
	case errors.As(err, &apiErr):
		writeError(w, r, err)
	case errors.Is(err, gitops.ErrNotInDiff):
		writeError(w, r, errNotInDiff)
	case errors.Is(err, gitops.ErrTooLarge):
		writeError(w, r, errTooLargeToExpand)
	case errors.Is(err, gitops.ErrNoBranch):
		writeError(w, r, api.Errorf(api.CodeNotFound, "a branch of this diff no longer exists"))
	case errors.Is(err, gitops.ErrNoCommit):
		writeError(w, r, api.Errorf(api.CodeNotFound, "%s", errCommitsGone(tg.project)))
	default:
		writeError(w, r, api.Errorf(api.CodeUnavailable, "the diff could not be read: %s", oneLine(err.Error())))
	}
}

var errNotInDiff = api.Errorf(api.CodeNotFound, "the file is not part of this diff")

func worktreeExists(dir string) bool {
	if dir == "" {
		return false
	}
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

func diffFiles(in []gitops.FileChange) []api.DiffFile {
	out := make([]api.DiffFile, 0, len(in))
	for _, f := range in {
		out = append(out, diffFile(f))
	}
	return out
}

// diffFile converts one file. Its names are made safe (escapeDiffPath) and
// its patch is text; bytes that are not UTF-8 become U+FFFD.
func diffFile(f gitops.FileChange) api.DiffFile {
	d := api.DiffFile{
		Path:      escapeDiffPath(f.Path),
		Status:    f.Status,
		Binary:    f.Binary,
		Additions: f.Additions,
		Deletions: f.Deletions,
		Truncated: f.Truncated,
	}
	if f.OldPath != "" {
		d.OldPath = escapeDiffPath(f.OldPath)
	}
	if f.Patch != nil {
		p := strings.ToValidUTF8(string(f.Patch), "�")
		d.Patch = &p
	}
	return d
}

// oneLine makes git's message fit a state: valid UTF-8, one line, none of
// the characters textsafe.IsHidden names, at most maxDiffMessage bytes.
func oneLine(msg string) string {
	msg = strings.ToValidUTF8(msg, "�")
	msg = strings.Map(func(r rune) rune {
		if textsafe.IsHidden(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(msg))
	if len(msg) > maxDiffMessage {
		msg = msg[:maxDiffMessage]
		for !utf8.ValidString(msg) {
			msg = msg[:len(msg)-1]
		}
	}
	return msg
}

// needsEscape reports whether r must not reach the browser as it is: the
// characters textsafe.IsHidden names and every other invisible format
// character (the joiners among them, which a file name rarely needs).
func needsEscape(r rune) bool {
	return textsafe.IsHidden(r) || unicode.Is(unicode.Cf, r)
}

// escapeDiffPath is how a file name leaves the server: the name itself when
// it is plain text, otherwise with a backslash written as \\, every byte that
// is not UTF-8 as \xNN and every control or invisible format character as
// \u{N} (lower-case hexadecimal). The result is valid UTF-8 without control
// characters and, because a backslash is always escaped, it maps back to
// exactly one name: unescapeDiffPath is its inverse and the single-file
// route takes the escaped form as the file's key.
func escapeDiffPath(raw string) string {
	var sb strings.Builder
	for i := 0; i < len(raw); {
		r, size := utf8.DecodeRuneInString(raw[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&sb, `\x%02x`, raw[i])
		case r == '\\':
			sb.WriteString(`\\`)
		case needsEscape(r):
			fmt.Fprintf(&sb, `\u{%x}`, r)
		default:
			sb.WriteString(raw[i : i+size])
		}
		i += size
	}
	return sb.String()
}

// unescapeDiffPath returns the name that escapeDiffPath turned into shown.
// It accepts nothing but the exact output of escapeDiffPath.
func unescapeDiffPath(shown string) (string, bool) {
	out := make([]byte, 0, len(shown))
	for i := 0; i < len(shown); {
		if shown[i] != '\\' {
			out = append(out, shown[i])
			i++
			continue
		}
		if i+1 >= len(shown) {
			return "", false
		}
		switch shown[i+1] {
		case '\\':
			out = append(out, '\\')
			i += 2
		case 'x':
			if i+4 > len(shown) {
				return "", false
			}
			v, err := strconv.ParseUint(shown[i+2:i+4], 16, 8)
			if err != nil {
				return "", false
			}
			out = append(out, byte(v))
			i += 4
		case 'u':
			end := strings.IndexByte(shown[i:], '}')
			if !strings.HasPrefix(shown[i:], `\u{`) || end < 0 {
				return "", false
			}
			v, err := strconv.ParseUint(shown[i+3:i+end], 16, 32)
			if err != nil || v > unicode.MaxRune || (v >= 0xd800 && v <= 0xdfff) {
				return "", false
			}
			out = utf8.AppendRune(out, rune(v))
			i += end + 1
		default:
			return "", false
		}
	}
	raw := string(out)
	if escapeDiffPath(raw) != shown {
		return "", false
	}
	return raw, true
}
