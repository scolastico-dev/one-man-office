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
}

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
	out := make([]diffTarget, 0, len(t.Projects))
	for _, p := range t.Projects {
		base := p.BaseBranch
		if storyBranch != nil {
			base = storyBranch[p.Project]
		}
		out = append(out, diffTarget{project: p, base: base})
	}
	return out, nil
}

// inDiffSlot runs fn when one of the diffSlots is free, with the time limit
// of a diff. Waiting for a slot ends with ctx.
func (s *Server) inDiffSlot(ctx context.Context, fn func(ctx context.Context) error) error {
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
	targets, err := s.diffTargets(r, n)
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := api.TicketDiff{Ticket: int(n), Projects: make([]api.ProjectDiff, 0, len(targets))}
	// One project after the other: the semaphore bounds the server, not one
	// request.
	for _, tg := range targets {
		out.Projects = append(out.Projects, s.projectDiff(r.Context(), tg))
		if r.Context().Err() != nil {
			return // the client left
		}
	}
	out.ComputedAt = time.Now().UTC()
	writeJSON(w, http.StatusOK, out)
}

// projectDiff computes the diff of one project; every way it can fail is a
// state of the answer.
func (s *Server) projectDiff(ctx context.Context, tg diffTarget) api.ProjectDiff {
	pd := api.ProjectDiff{
		Project:     tg.project.Project,
		Base:        escapeDiffPath(tg.base),
		Branch:      escapeDiffPath(tg.project.Branch),
		State:       "ok",
		Files:       []api.DiffFile{},
		Uncommitted: []api.DiffFile{},
	}
	proj, ok := s.settings.Project(tg.project.Project)
	switch {
	case !ok:
		pd.State, pd.Message = "project_unavailable", "The project is not registered any more."
		return pd
	case proj.Missing:
		pd.State, pd.Message = "project_unavailable", "The project's folder is missing."
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
		var err error
		res, err = s.git.BranchDiff(ctx, proj.Path, tg.base, tg.project.Branch, gitops.DefaultDiffLimits)
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
	case ctx.Err() != nil:
		return pd // the client left; nobody reads this
	case errors.Is(err, gitops.ErrNoBranch) && goneBranch:
		pd.State = "branch_gone"
		pd.Message = fmt.Sprintf("The branch %s no longer exists; it was removed after the merge.", escapeDiffPath(tg.project.Branch))
	case errors.Is(err, gitops.ErrNoBranch):
		pd.State = "branch_gone"
		pd.Message = fmt.Sprintf("The branch to compare with, %s, no longer exists.", escapeDiffPath(tg.base))
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
	if !ok || proj.Missing || tg.project.Branch == "" || tg.base == "" {
		writeError(w, r, api.Errorf(api.CodeNotFound, "there is no diff for this project"))
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
			fc, err = s.git.WorktreeFileDiff(ctx, proj.Path, tg.project.Worktree, raw)
		} else {
			fc, err = s.git.BranchFileDiff(ctx, proj.Path, tg.base, tg.project.Branch, raw)
		}
		return err
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, diffFile(fc))
	case r.Context().Err() != nil:
		return // the client left
	case errors.Is(err, gitops.ErrNotInDiff):
		writeError(w, r, errNotInDiff)
	case errors.Is(err, gitops.ErrNoBranch):
		writeError(w, r, api.Errorf(api.CodeNotFound, "a branch of this diff no longer exists"))
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
