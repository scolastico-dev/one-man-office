package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/branchname"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/session"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// startJob is one agent start a worker carries out. session is set (under
// Engine.mu) once the worker created the session.
type startJob struct {
	action  action
	ticket  int64
	role    api.Role
	session string
}

// startProblem is a start that cannot go on without a person; reason says
// what to do.
type startProblem struct{ reason string }

func (p *startProblem) Error() string { return p.reason }

func problemf(format string, args ...any) error {
	return &startProblem{reason: fmt.Sprintf(format, args...)}
}

// cutBranch is a branch and worktree one start created.
type cutBranch struct {
	project, repo, dir, branch string
	// base is the commit the branch was cut from; the branch is deleted
	// on undo only while it still points there.
	base     string
	recorded bool
}

// starter carries one start through its steps and undoes what it did when
// a step fails.
type starter struct {
	e   *Engine
	ctx context.Context
	job *startJob
	// claimed is set while the start holds the ticket's claim.
	claimed bool
	// cuts are the branches and worktrees this start created.
	cuts []cutBranch
	// recordedWorkdir is set once this start recorded the working folder
	// of a task without a project, madeWorkdir once it created it.
	recordedWorkdir bool
	madeWorkdir     string
	// begun is set once the ticket moved to In Progress: from then on the
	// ticket owns what was cut.
	begun bool
}

// startWork starts the agent a planner start asks for. A gated start
// claims its Ready ticket, cuts the branches and worktrees it needs, records
// them and moves the ticket to In Progress; a continuation makes sure the
// worktree it works in exists. Then the session is created and the agent
// started. Whatever fails, nothing is left that the ticket does not own: a
// branch and worktree cut by this start are removed again while they hold
// nothing, a created session is ended, and the claim always ends (released,
// or ended by the failed step that tells a person what to do).
//
// It returns the error of a start that failed without a failed step, which
// a later pass may try again (see settleAttempt); nil when the agent
// started, when the ticket no longer needs it, or when the failure is
// recorded on the ticket or counted against the agent's slot.
func (e *Engine) startWork(ctx context.Context, job *startJob) error {
	s := &starter{e: e, ctx: ctx, job: job}
	defer s.finish()
	a := job.action
	if a.gated {
		if err := e.b.ClaimStart(ctx, a.ticket); err != nil {
			e.log.Printf("engine: claim #%d to start it: %v", a.ticket, err)
			return fmt.Errorf("claim #%d: %w", a.ticket, err)
		}
		s.claimed = true
		e.at("claimed")
	}
	t, snap, ok, err := e.read(ctx, a.ticket)
	if err != nil {
		e.log.Printf("engine: read #%d: %v", a.ticket, err)
		return fmt.Errorf("read the board: %w", err)
	}
	if !ok || !s.wanted(t) {
		return nil
	}
	if !a.gated {
		reason, err := e.tooManyBounces(ctx, t.ID)
		if err != nil {
			e.log.Printf("engine: read the history of #%d: %v", a.ticket, err)
			return fmt.Errorf("read the history of #%d: %w", a.ticket, err)
		}
		if reason != "" {
			if err := e.b.SetFailed(ctx, t.ID, api.FailedStart, reason); err != nil {
				e.log.Printf("engine: flag #%d: %v", t.ID, err)
				return fmt.Errorf("record the failed start: %w", err)
			}
			return nil
		}
	}
	model, err := s.pickModel(t)
	if errors.Is(err, errUsageWait) {
		// The model's usage rose since the pass planned the start: nothing
		// was done, and the next pass shows the ticket as waiting for usage.
		return nil
	}
	if err != nil {
		e.log.Printf("engine: start %s on #%d: %v", a.role, a.ticket, err)
		return fmt.Errorf("pick a model: %w", err)
	}
	dir, err := s.prepare(t, snap)
	if errors.Is(err, errExportFailed) {
		return nil // recorded for its project; the other projects go on
	}
	if err != nil {
		return s.fail(err)
	}
	e.at("prepared")
	if !a.gated {
		// The ticket has started work: what was cut is recorded on it and
		// stays for its next agent.
		s.begun = true
	}
	if a.gated {
		if e.isStopping() {
			return nil // nothing was started; what was cut is removed again
		}
		if err := e.b.BeginWork(ctx, a.ticket); err != nil {
			e.log.Printf("engine: begin work on #%d: %v", a.ticket, err)
			return fmt.Errorf("move #%d to in progress: %w", a.ticket, err)
		}
		s.claimed, s.begun = false, true
		e.at("begun")
	}
	// The ticket may have changed while the branches were cut (a cancel,
	// once the claim ended); start nothing for a ticket that no longer
	// needs the agent.
	t, snap, ok, err = e.read(ctx, a.ticket)
	if err != nil {
		e.log.Printf("engine: read #%d: %v", a.ticket, err)
		return fmt.Errorf("read the board: %w", err)
	}
	if !ok || !s.wanted(t) || e.isStopping() || snap.Paused {
		return nil
	}
	e.at("spawn")
	round := 0
	if a.role == api.RoleReviewer {
		round = t.ReviewRound
	}
	project := a.project
	if project == "" && a.role == api.RoleReviewer && len(t.Projects) == 1 {
		project = t.Projects[0].ID
	}
	sess, err := e.spawn(ctx, spawnRequest{
		role: a.role, ticket: a.ticket, project: project, purpose: purposeOrWork(a.purpose), round: round,
		model: model, workdir: dir, job: job, sessions: snap.Sessions,
	})
	var counted *countedError
	if err != nil {
		e.log.Printf("engine: start %s on #%d: %v", a.role, a.ticket, err)
		if !errors.As(err, &counted) {
			return fmt.Errorf("start the agent: %w", err)
		}
		return nil
	}
	// While paused nothing starts: an agent whose start was under way when
	// omo was paused (by Stop all agents, say) ends again at once, so no
	// stop misses it. Lifting the pause starts a fresh one.
	if paused, err := e.b.Paused(ctx); err != nil {
		e.log.Printf("engine: read whether omo is paused after starting %s on #%d: %v", a.role, a.ticket, err)
	} else if paused {
		e.end(ctx, sess.ID, pausedStartReason)
		return nil
	}
	if a.role == workingRole(t) && purposeOrWork(a.purpose) == api.PurposeWork {
		// "Start anyway" holds for one start.
		e.clearForced(a.ticket)
	}
	return nil
}

// countedError is a failed agent start that was already counted against the
// agent's slot or recorded on its ticket (see startFailed).
type countedError struct{ error }

func (c *countedError) Unwrap() error { return c.error }

func purposeOrWork(p api.Purpose) api.Purpose {
	if p == "" {
		return api.PurposeWork
	}
	return p
}

// isStopping reports whether the engine stops.
func (e *Engine) isStopping() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopping
}

// wanted reports whether t still needs the agent the job starts.
func (s *starter) wanted(t board.EngineTicket) bool {
	a := s.job.action
	if t.FailedStep != api.FailedNone {
		return false
	}
	if a.gated && !s.begun {
		return t.Column == api.ColumnReady && t.Busy
	}
	switch {
	case a.role == api.RoleReviewer:
		if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageAgent {
			return false
		}
		p := a.project
		if p == "" && len(t.Projects) == 1 {
			p = t.Projects[0].ID
		}
		return slices.Contains(t.ReviewKeys(), p) && t.Verdicts[p] == ""
	case a.purpose == api.PurposeExport:
		if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting {
			return false
		}
		for _, p := range t.Projects {
			if p.ID == a.project {
				return p.ExportURL == ""
			}
		}
		return false
	}
	return t.Column == api.ColumnInProgress && a.role == workingRole(t)
}

// pickModel chooses the model of the agent: the ticket's own, else the
// model of the previous session of the slot (after a crash, the next one
// for a role in failover mode), else the role's assignment.
func (s *starter) pickModel(t board.EngineTicket) (string, error) {
	a := s.job.action
	e := s.e
	previous := ""
	if a.role == workingRole(t) && purposeOrWork(a.purpose) == api.PurposeWork {
		prev, found, err := e.b.PreviousSession(s.ctx, t.ID, a.role, "", api.PurposeWork)
		if err != nil {
			return "", err
		}
		if found {
			previous = prev.Model
		}
	}
	k := failKey{ticket: t.ID, role: a.role, purpose: purposeOrWork(a.purpose)}
	if a.role == api.RoleReviewer || k.purpose == api.PurposeExport {
		k.project = a.project
	}
	c := e.modelChoice(a.role, purposeOrWork(a.purpose), t, previous, e.attempts(k))
	c.forced = c.ticketModel != "" && e.isForced(t)
	return e.picker.pick(a.role, c, e.st, e.use)
}

// prepare makes sure the folder the agent works in exists and returns it:
// for a story's product manager the integration worktree of every project
// (cut from each project's base branch when missing), for a task's
// developer its task worktree (cut from the story's integration branch, or
// from the base branch for a standalone task), for a reviewer the existing
// worktree of its project. An export job never works in a worktree: the
// reviewed commit is pushed first and the job gets the folder omo made for
// it (see prepareExport). The developer and the reviewer of a task without
// a project work in its working folder (see workdir).
func (s *starter) prepare(t board.EngineTicket, snap board.EngineSnapshot) (string, error) {
	a := s.job.action
	if len(t.Projects) == 0 {
		if t.Type == api.TypeStory || purposeOrWork(a.purpose) != api.PurposeWork {
			return "", problemf("#%d has no project", t.ID)
		}
		return s.workdir(t)
	}
	switch {
	case a.role == api.RolePM:
		first := ""
		for _, p := range t.Projects {
			dir, err := s.worktree(t, p, p.Base)
			if err != nil {
				return "", err
			}
			if first == "" {
				first = dir
			}
		}
		return first, nil
	case a.role == api.RoleDeveloper && purposeOrWork(a.purpose) == api.PurposeWork:
		p := t.Projects[0]
		base := p.Base
		if t.StoryID != 0 {
			base = ""
			for _, st := range snap.Tickets {
				if st.ID != t.StoryID {
					continue
				}
				for _, sp := range st.Projects {
					if sp.ID == p.ID {
						base = sp.Branch
					}
				}
			}
			if base == "" && p.Branch == "" {
				return "", problemf("story #%d has no integration branch in project %s to cut the branch of #%d from", t.StoryID, p.ID, t.ID)
			}
		}
		return s.worktree(t, p, base)
	default: // a reviewer works where the work was done
		id := a.project
		if id == "" && len(t.Projects) == 1 {
			id = t.Projects[0].ID
		}
		for _, p := range t.Projects {
			if p.ID != id {
				continue
			}
			if p.Branch == "" {
				return "", problemf("#%d has no branch in project %s to work on", t.ID, id)
			}
			if purposeOrWork(a.purpose) == api.PurposeExport {
				return s.e.prepareExport(s.ctx, t, p.ID)
			}
			return s.worktree(t, p, "")
		}
		return "", problemf("project %s is not a project of #%d", id, t.ID)
	}
}

// workdir returns the working folder of t, a task without a project: the
// recorded one, made again (empty) when it is gone, or, for its developer's
// first start, a new folder below the working folders' folder, recorded on
// the ticket. It is a plain folder, not a git worktree, and omo runs no git
// in it. A folder in its place that omo did not record, or that is not a
// plain folder, is never touched.
func (s *starter) workdir(t board.EngineTicket) (string, error) {
	e := s.e
	dir := WorkdirDir(e.o.Layout.Workdirs, t.ID)
	if t.Workdir != "" {
		if filepath.Clean(t.Workdir) != dir {
			return "", problemf("the working folder recorded for #%d (%s) is not the one omo makes for it (%s); omo leaves it alone", t.ID, t.Workdir, dir)
		}
		fi, err := os.Lstat(dir)
		switch {
		case err == nil && fi.Mode().Type() == fs.ModeDir:
			return dir, nil
		case err == nil:
			return "", problemf("the working folder %s of #%d is not a plain folder; omo leaves it alone. Move or remove it, then retry.", dir, t.ID)
		case !errors.Is(err, fs.ErrNotExist):
			return "", problemf("cannot read the working folder %s of #%d: %v", dir, t.ID, err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", problemf("cannot make the working folder %s of #%d again: %v", dir, t.ID, err)
		}
		return dir, nil
	}
	if s.job.action.role != api.RoleDeveloper {
		return "", problemf("#%d has no working folder to work in", t.ID)
	}
	if _, err := os.Lstat(dir); err == nil {
		return "", problemf("the folder %s already exists and is not a working folder omo recorded for #%d; omo leaves it alone. Move or remove it, then retry.", dir, t.ID)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", problemf("cannot create the working folders' folder %s: %v", filepath.Dir(dir), err)
	}
	// Recorded before it is made: a start interrupted in between (omo
	// stopped) leaves a recorded folder, which the next start makes, never
	// an unrecorded one that would stop it.
	if err := e.b.SetWorkdir(s.ctx, t.ID, dir); err != nil {
		return "", fmt.Errorf("record the working folder of #%d: %w", t.ID, err)
	}
	s.recordedWorkdir = true
	e.at("workdir-recorded")
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", problemf("cannot create the working folder %s of #%d: %v", dir, t.ID, err)
	}
	s.madeWorkdir = dir
	return dir, nil
}

// worktree returns the worktree of ticket t in project p: the recorded one,
// recreated from its branch when the folder is gone, or a new branch cut
// from base with a new worktree when none is recorded.
func (s *starter) worktree(t board.EngineTicket, p board.EngineProject, base string) (string, error) {
	e := s.e
	proj, ok := e.st.Project(p.ID)
	if !ok || !proj.Enabled || proj.Missing {
		return "", problemf("project %s is missing or disabled", p.ID)
	}
	repo := proj.Path
	if p.Branch != "" {
		return s.ensure(t, p, repo)
	}
	if base == "" {
		return "", problemf("#%d has no base branch in project %s", t.ID, p.ID)
	}
	return s.cut(t, p, repo, base)
}

// cut creates a new branch for t from base in repo, checked out in a new
// worktree below the worktrees folder, and records both on the ticket.
func (s *starter) cut(t board.EngineTicket, p board.EngineProject, repo, base string) (string, error) {
	e := s.e
	dir := WorktreeDir(e.o.Layout.Worktrees, p.ID, t.ID)
	_, err := os.Lstat(dir)
	exists := err == nil
	baseID, err := e.git.RevParse(repo, "refs/heads/"+base)
	if err != nil {
		if exists {
			return "", problemf("the folder %s already exists and is not a worktree omo recorded for #%d; omo leaves it alone. Move or remove it, then retry.", dir, t.ID)
		}
		return "", problemf("the base branch %s of #%d does not exist in project %s", base, t.ID, p.ID)
	}
	// The name comes from the branch template; it is picked and the branch
	// created under the repository's lock (gitops), so tickets started in
	// the same pass never get one name. A name is taken by a local or
	// remote-tracking branch (or one below it), by the branch recorded on
	// any ticket in any project in this repository (its branch may be
	// deleted by now, and the name still belongs to that ticket's history),
	// and by the base branch
	// and the repository's default branches, so whatever the template
	// gives, omo never adopts a branch it did not create.
	tmpl := e.st.General().Branches.Template
	recorded, err := e.recordedBranches(s.ctx, repo)
	if err != nil {
		return "", fmt.Errorf("read the branches recorded for project %s: %w", p.ID, err)
	}
	protected, err := e.git.ProtectedBranches(repo)
	if err != nil {
		return "", problemf("cannot read the default branches of project %s: %v", p.ID, err)
	}
	protected = append(protected, base)
	if cur, err := e.git.CurrentBranch(repo); err == nil && cur != "" {
		protected = append(protected, cur)
	}
	vars := branchVars(t, p.ID)
	if exists {
		return s.adopt(t, p.ID, repo, dir, baseID, tmpl, vars, recorded, protected)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", problemf("cannot create the worktrees folder %s: %v", filepath.Dir(dir), err)
	}
	branch, err := e.git.AddWorktreeNewBranch(repo, dir, baseID, func(taken func(string) (bool, error)) (string, error) {
		return BranchName(tmpl, vars, func(name string) (bool, error) {
			if branchname.In(protected, name) {
				return true, nil
			}
			for _, r := range recorded {
				switch {
				case branchname.Same(r.Branch, name), branchname.Below(r.Branch, name):
					return true, nil
				case branchname.Below(name, r.Branch):
					return false, &gitops.PathConflictError{Name: name, Existing: r.Branch}
				}
			}
			return taken(name)
		})
	})
	c := cutBranch{project: p.ID, repo: repo, dir: dir, branch: branch, base: baseID}
	if err != nil {
		var failed *gitops.NewBranchError
		var conflict *gitops.PathConflictError
		if errors.As(err, &conflict) {
			return "", problemf("omo cannot create the branch of #%d in project %s: %v, then retry.", t.ID, p.ID, err)
		}
		if !errors.As(err, &failed) {
			return "", problemf("omo could not pick a branch name for #%d in project %s: %v. Fix that in the repository or change the branch template (Settings, General), then retry.", t.ID, p.ID, err)
		}
		if failed.Created {
			// Only a branch this call created is removed again.
			c.branch = failed.Branch
			s.undoCut(c)
		}
		return "", problemf("could not create branch %s with its worktree %s in project %s: %v", failed.Branch, dir, p.ID, err)
	}
	s.cuts = append(s.cuts, c)
	if err := e.b.SetBranch(s.ctx, t.ID, p.ID, branch, dir); err != nil {
		return "", fmt.Errorf("record branch %s of #%d: %w", branch, t.ID, err)
	}
	s.cuts[len(s.cuts)-1].recorded = true
	return dir, nil
}

// adopt continues with the branch and worktree an interrupted start of t
// cut in project (omo stopped after git made them and before the board
// recorded them) and records them: the folder dir is a worktree repo
// records, plain, with the .git file git wrote and without changes, and
// its branch is named as a start of t names it, is recorded on no ticket,
// is not protected and holds no commit its base (at baseID) lacks.
// Anything else in that place is not omo's and fails the start.
func (s *starter) adopt(t board.EngineTicket, project, repo, dir, baseID, tmpl string, vars branchname.Vars,
	recorded []board.RecordedBranch, protected []string) (string, error) {
	e := s.e
	foreign := problemf("the folder %s already exists and is not a worktree omo recorded for #%d; omo leaves it alone. Move or remove it, then retry.", dir, t.ID)
	branch, found, err := e.git.BranchAt(repo, dir)
	if err != nil || !found || !ownBranchName(tmpl, vars, branch) || branchname.In(protected, branch) {
		return "", foreign
	}
	for _, r := range recorded {
		if branchname.Same(r.Branch, branch) || branchname.Below(r.Branch, branch) || branchname.Below(branch, r.Branch) {
			return "", foreign
		}
	}
	at, ok, err := e.git.WorktreeOf(repo, branch)
	if err != nil || !ok || !sameWorktree(at, dir) {
		return "", foreign
	}
	if err := e.git.CheckWorktreeGitFile(s.ctx, repo, dir); err != nil {
		return "", foreign
	}
	if dirty, err := e.git.Status(repo, dir); err != nil || len(dirty) > 0 {
		return "", foreign
	}
	head, err := e.git.RevParse(repo, "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", foreign
	}
	if own, err := e.git.IsAncestor(repo, head, baseID); err != nil || !own {
		return "", foreign
	}
	e.log.Printf("engine: #%d goes on with branch %s and worktree %s in project %s, made by a start that was interrupted", t.ID, branch, dir, project)
	s.cuts = append(s.cuts, cutBranch{project: project, repo: repo, dir: dir, branch: branch, base: head})
	if err := e.b.SetBranch(s.ctx, t.ID, project, branch, dir); err != nil {
		return "", fmt.Errorf("record branch %s of #%d: %w", branch, t.ID, err)
	}
	s.cuts[len(s.cuts)-1].recorded = true
	return dir, nil
}

// ensure makes sure the recorded branch of t in project p is checked out in
// its worktree, recreating a worktree folder that is gone. A folder that is
// there but holds something else is never touched.
func (s *starter) ensure(t board.EngineTicket, p board.EngineProject, repo string) (string, error) {
	e := s.e
	dir := p.Worktree
	if dir == "" {
		dir = WorktreeDir(e.o.Layout.Worktrees, p.ID, t.ID)
	}
	at, found, err := e.git.WorktreeOf(repo, p.Branch)
	if err != nil {
		return "", problemf("cannot list the worktrees of project %s: %v", p.ID, err)
	}
	if _, err := os.Lstat(dir); err == nil {
		if found && sameWorktree(at, dir) {
			return dir, nil
		}
		return "", problemf("the folder %s exists but does not have branch %s of #%d checked out; omo leaves it alone. Move or remove it, then retry.", dir, p.Branch, t.ID)
	}
	if found && !samePath(at, dir) {
		return "", problemf("branch %s of #%d is checked out in %s; omo needs it in its worktree %s. Switch that folder to another branch, then retry.", p.Branch, t.ID, at, dir)
	}
	exists, err := e.git.BranchExists(repo, p.Branch)
	if err != nil {
		return "", problemf("cannot read branch %s of #%d in project %s: %v", p.Branch, t.ID, p.ID, err)
	}
	if !exists {
		return "", problemf("branch %s of #%d no longer exists in project %s", p.Branch, t.ID, p.ID)
	}
	// Forget a registration whose folder is gone, then check out again.
	if _, err := e.git.RemoveWorktreeIfClean(repo, dir, p.Branch); err != nil {
		return "", problemf("cannot recreate the worktree %s of #%d: %v", dir, t.ID, err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return "", problemf("cannot create the worktrees folder %s: %v", filepath.Dir(dir), err)
	}
	if err := e.git.AddWorktreeForBranch(repo, dir, p.Branch); err != nil {
		return "", problemf("cannot recreate the worktree %s of branch %s of #%d: %v", dir, p.Branch, t.ID, err)
	}
	if dir != p.Worktree {
		if err := e.b.SetBranch(s.ctx, t.ID, p.ID, p.Branch, dir); err != nil {
			return "", fmt.Errorf("record the worktree of #%d: %w", t.ID, err)
		}
	}
	return dir, nil
}

// sameWorktree reports whether the existing folder dir is the worktree git
// records at at. dir must be a plain folder: an agent can replace its
// worktree by a link to another folder, and an agent started there would
// work in the folder the link points at. Only links in the parents of both
// are resolved.
func sameWorktree(at, dir string) bool {
	real, err := gitops.PlainFolder(dir)
	if err != nil {
		return false
	}
	parent := filepath.Dir(filepath.Clean(at))
	if r, err := filepath.EvalSymlinks(parent); err == nil {
		parent = r
	}
	return filepath.Join(parent, filepath.Base(at)) == real
}

// samePath reports whether a and b name the same folder, whether or not it
// exists.
func samePath(a, b string) bool { return realPath(a) == realPath(b) }

// realPath is p with its links resolved. Of a path that does not exist, the
// part that exists is resolved: git names a worktree whose folder is gone
// by its real path, while omo may know it through a link or, on Windows,
// under a short name (RUNNER~1 for runneradmin).
func realPath(p string) string {
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}

// fail ends a start that cannot go on: a problem a person must solve
// becomes the ticket's failed step (which also ends the claim), anything
// else is logged and returned, and the start is tried again on a later
// pass.
func (s *starter) fail(err error) error {
	var p *startProblem
	if !errors.As(err, &p) {
		s.e.log.Printf("engine: start %s on #%d: %v", s.job.role, s.job.ticket, err)
		return err
	}
	s.undo()
	if serr := s.e.b.SetFailed(s.ctx, s.job.ticket, api.FailedStart, p.reason); serr != nil {
		s.e.log.Printf("engine: flag #%d: %v", s.job.ticket, serr)
		return fmt.Errorf("record the failed start: %w", serr)
	}
	s.claimed = false
	return nil
}

// finish runs when the start is over, however it ended: before the ticket
// moved to In Progress, whatever this start cut is removed again; a claim
// still held is released.
func (s *starter) finish() {
	if !s.begun {
		s.undo()
	}
	if s.claimed {
		if err := s.e.b.ReleaseClaim(context.WithoutCancel(s.ctx), s.job.ticket); err != nil {
			s.e.log.Printf("engine: release the claim on #%d: %v", s.job.ticket, err)
		}
		s.claimed = false
	}
}

// undo removes the branches and worktrees this start cut, newest first,
// and the working folder it made while it is still empty.
func (s *starter) undo() {
	if s.recordedWorkdir {
		if err := s.e.b.SetWorkdir(context.WithoutCancel(s.ctx), s.job.ticket, ""); err != nil {
			s.e.log.Printf("engine: forget the working folder of #%d: %v", s.job.ticket, err)
		}
		s.recordedWorkdir = false
	}
	if dir := s.madeWorkdir; dir != "" {
		if err := os.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.e.log.Printf("engine: kept the working folder %s of #%d: %v", dir, s.job.ticket, err)
		}
		s.madeWorkdir = ""
	}
	for i := len(s.cuts) - 1; i >= 0; i-- {
		c := s.cuts[i]
		if c.recorded {
			if err := s.e.b.SetBranch(context.WithoutCancel(s.ctx), s.job.ticket, c.project, "", ""); err != nil {
				s.e.log.Printf("engine: forget branch %s of #%d: %v", c.branch, s.job.ticket, err)
			}
		}
		s.undoCut(c)
	}
	s.cuts = nil
}

// undoCut removes the worktree and branch of c while they hold nothing but
// what they were cut with.
func (s *starter) undoCut(c cutBranch) {
	g := s.e.git
	if _, err := os.Lstat(c.dir); err == nil {
		dirty, err := g.RemoveWorktreeIfClean(c.repo, c.dir, c.branch)
		if err != nil || len(dirty) > 0 {
			s.e.log.Printf("engine: kept worktree %s of #%d (changes: %s; %v)", c.dir, s.job.ticket, strings.Join(dirty, ", "), err)
			return
		}
	}
	if err := g.DeleteBranch(c.repo, c.branch, c.base); err != nil {
		s.e.log.Printf("engine: kept branch %s of #%d: %v", c.branch, s.job.ticket, err)
	}
}

// spawnRequest describes one agent to start on a session of its own.
type spawnRequest struct {
	role    api.Role
	ticket  int64 // 0 for the CEO
	project string
	purpose api.Purpose
	round   int
	model   string
	workdir string
	// job, when set, learns the session id once the session exists.
	job *startJob
	// sessions are the live sessions, whose names are taken.
	sessions []board.EngineSession
}

// spawn creates the session, renders the start prompt and starts the agent
// process, then watches it. If the process does not start, the session is
// ended and the failure counted (a hanging environment command instead
// flags the ticket for a person, since a fresh start would hang again). It
// must run in a worker (see launch), so stopping the engine waits for it.
func (e *Engine) spawn(ctx context.Context, r spawnRequest) (api.AgentSession, error) {
	profileName, modelName, _ := strings.Cut(r.model, "/")
	prof, ok := e.st.Profile(profileName)
	if !ok {
		return api.AgentSession{}, fmt.Errorf("model %s: profile %s does not exist", r.model, profileName)
	}
	m, ok := prof.Models[modelName]
	if !ok {
		return api.AgentSession{}, fmt.Errorf("model %s does not exist", r.model)
	}
	name, err := e.reserveName(r.role, r.sessions)
	if err != nil {
		return api.AgentSession{}, err
	}
	defer e.releaseName(name)
	logPath, err := e.logPath(r.ticket, name)
	if err != nil {
		return api.AgentSession{}, err
	}
	sess, err := e.b.CreateSession(ctx, board.NewSession{
		Role: r.role, Ticket: r.ticket, Project: r.project, Name: name, Model: r.model,
		Workdir: r.workdir, LogPath: logPath, Purpose: r.purpose, ReviewRound: r.round,
	})
	if err != nil {
		return api.AgentSession{}, fmt.Errorf("create the session of %s: %w", name, err)
	}
	es := board.EngineSession{ID: sess.ID, Role: r.role, Ticket: r.ticket, Project: r.project, Purpose: r.purpose,
		ReviewRound: r.round, State: api.SessionStarting, Name: name, Model: r.model, Workdir: r.workdir}
	en := &entry{sess: es, slot: slotKey(es), delivery: agents.ResolveDelivery(prof.PromptDelivery),
		finished: make(chan struct{})}
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		e.endSession(ctx, sess.ID, "omo stopped", name)
		return sess, nil
	}
	e.entries[sess.ID] = en
	if r.job != nil {
		r.job.session = sess.ID
	}
	e.mu.Unlock()
	if r.role == api.RoleCEO {
		e.bindCEOPending(sess.ID)
	}

	g := e.st.General()
	socket := ""
	if e.o.Socket != nil {
		socket = e.o.Socket()
	}
	spec := agents.Spec{
		SessionID:     sess.ID,
		Name:          name,
		CLI:           prof.CLI,
		Command:       prof.Command,
		Args:          append(append([]string(nil), prof.Args...), m.Args...),
		AgentEnv:      g.AgentEnv,
		ProfileEnv:    prof.Env,
		ModelEnv:      m.Env,
		AccountDir:    expandAccountDir(prof.AccountDir),
		Dir:           r.workdir,
		LogPath:       logPath,
		Socket:        socket,
		PathPrefix:    e.pathPrefix,
		StartPrompt:   e.renderPrompt("start", prompts.Data{Name: name, Role: string(r.role), Ticket: int(r.ticket)}),
		MCPBridge:     e.mcpBridge(prof.CLI),
		Delivery:      en.delivery,
		AwaitingReady: en.awaitingReady,
		OnWarning: func(msg string) {
			e.startWarnings(context.WithoutCancel(ctx), r.ticket, name, []string{msg})
		},
	}
	en.started = e.now()
	ag, err := e.o.Runner.Start(spec)
	if err != nil {
		e.mu.Lock()
		delete(e.entries, sess.ID)
		e.mu.Unlock()
		close(en.finished)
		e.startFailed(ctx, en, err)
		return sess, &countedError{err}
	}
	e.mu.Lock()
	en.agent = ag
	if en.ending == "" && e.stopping {
		en.ending = "omo stopped"
	}
	ending := en.ending
	e.watchers.Add(1)
	e.mu.Unlock()
	if err := e.reg.Add(ag); err != nil {
		e.log.Printf("engine: register %s: %v", name, err)
	}
	go e.watch(en)
	if ending != "" {
		_ = ag.Kill()
		return sess, nil
	}
	e.startWarnings(ctx, r.ticket, name, ag.Warnings())
	return sess, nil
}

// startWarnings tells about problems that did not stop the start of agent
// name on ticket (0 for the CEO): in a comment on the ticket, or in the log.
func (e *Engine) startWarnings(ctx context.Context, ticket int64, name string, w []string) {
	if len(w) == 0 {
		return
	}
	msg := fmt.Sprintf("%s started with warnings:\n- %s", name, strings.Join(w, "\n- "))
	if ticket != 0 {
		if _, err := e.b.Comment(ctx, board.System, ticket, api.CommentRequest{Body: msg}); err != nil {
			e.log.Printf("engine: comment on #%d: %v", ticket, err)
		}
		return
	}
	e.log.Print(msg)
}

// startFailed ends the session of an agent whose process did not start and
// counts the failure. An environment command that timed out fails the start
// for a person instead: every new attempt would hang the same way.
func (e *Engine) startFailed(ctx context.Context, en *entry, err error) {
	e.endSession(ctx, en.sess.ID, "could not start: "+err.Error(), en.sess.Name)
	if en.sess.Ticket == 0 {
		return
	}
	var timeout *session.CommandTimeoutError
	if errors.As(err, &timeout) {
		reason := fmt.Sprintf("%s could not start: %v. Fix that variable in the agent environment (settings, profile or model), then retry.",
			en.sess.Name, timeout)
		if serr := e.b.SetFailed(ctx, en.sess.Ticket, api.FailedStart, reason); serr != nil {
			e.log.Printf("engine: flag #%d: %v", en.sess.Ticket, serr)
		}
		return
	}
	e.failed(ctx, en.sess, fmt.Sprintf("could not start (%v)", err), nil)
}

// endSession ends a session on the board, logging a failure.
func (e *Engine) endSession(ctx context.Context, id, reason, name string) {
	if err := e.b.EndSession(ctx, id, reason); err != nil {
		e.log.Printf("engine: end the session of %s: %s", name, scrub(err, id))
	}
}

// logPath returns a new transcript file for an agent. Its name never holds
// the session id.
func (e *Engine) logPath(ticket int64, name string) (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return filepath.Join(e.o.Layout.Logs, fmt.Sprintf("%d-%s-%s.log", ticket, name, hex.EncodeToString(b[:]))), nil
}

// expandAccountDir expands a leading ~ of a profile's account folder.
func expandAccountDir(dir string) string {
	if dir == "" {
		return ""
	}
	return settings.ExpandPath(dir)
}

// renderPrompt renders the prompt name in effect with d. A customized
// prompt that fails to render, or would not render within the bounds of
// prompts.RenderBounded, is reported as settings.invalid (one line, no text
// of the prompt) and the embedded default is used instead.
func (e *Engine) renderPrompt(name string, d prompts.Data) string {
	text, customized, ok := e.st.Prompt(name)
	if ok {
		out, err := prompts.RenderBounded(name, text, d)
		if err == nil {
			return out
		}
		if customized {
			e.emit(api.KindSettingsInvalid, api.SettingsInvalidData{File: "prompts/" + name + ".md", Message: err.Error()})
		} else {
			e.log.Printf("engine: render prompt %s: %v", name, err)
		}
	}
	out, err := prompts.Render(name, prompts.Defaults()[name], d)
	if err != nil {
		e.log.Printf("engine: render the default prompt %s: %v", name, err)
	}
	return out
}

// mcpBridge is the omo executable whose `mcp stdio` a new agent of cli
// gets as its MCP server for its session, or "" when it gets none: the
// MCP settings switch it per program, and only Claude Code and Codex take
// a server for one session.
func (e *Engine) mcpBridge(cli string) string {
	s := api.MCPSettings{Enabled: true, Inject: api.MCPInject{Claude: true, Codex: true}}
	if e.o.MCP != nil {
		s = e.o.MCP()
	}
	if !s.Enabled {
		return ""
	}
	switch {
	case cli == api.CLIClaude && s.Inject.Claude, cli == api.CLICodex && s.Inject.Codex:
		return e.omoPath
	}
	return ""
}
