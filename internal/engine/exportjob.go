package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/forge"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// An export publishes exactly the reviewed work. Before an export job
// starts, omo itself pushes the commit pinned at hand-in for the current
// review round (never what the branch points at by then: an agent can move
// the branch) from the project's own repository to the remote its config
// names, as the ticket's branch, without force or hooks (gitops.PushCommit).
// The worktree, and with it any .git an agent controls, is not used. Then
// the job starts in a folder omo made below the run folder, holding the
// material for the pull request's text and omo's own title and body (see
// exporttext.go); the job writes the text for this repository, has omo
// check it, opens the pull request with the command omo gives it and
// reports its address.

// exportDir is the folder below the run folder that holds the export jobs'
// folders.
const exportDir = "export"

// Bounds of what goes into a pull request.
const (
	maxExportTitle = 200      // characters
	maxExportBody  = 60 << 10 // bytes of the body
)

// lookPath finds a forge's command line tool; it is never run. Tests
// replace it.
var lookPath = exec.LookPath

// errExportFailed is a prepared export that failed and was recorded for its
// project: no job starts and the start is not tried again.
var errExportFailed = errors.New("the export failed")

// exportFolder is the folder of the export job of ticket n in project.
func (e *Engine) exportFolder(n int64, project string) (string, error) {
	if e.o.Layout.Run == "" {
		return "", errors.New("omo has no run folder for export jobs")
	}
	return filepath.Join(e.o.Layout.Run, exportDir, strconv.FormatInt(n, 10)+"-"+project), nil
}

// exportPlan is what an export of one project of a ticket pushes where.
type exportPlan struct {
	project, repo string
	remote        gitops.Remote
	// forge is the remote as a forge sees it; zero for a local path or an
	// address omo does not know (forgeErr).
	forge    forge.Remote
	forgeErr error
	// commit is pushed as branch; the pull request targets base.
	commit, branch, base string
	// unpinned is set when no commit was recorded at hand-in and the
	// branch's current commit was taken (and recorded) instead.
	unpinned bool
}

// planExport finds what the export of project of t pushes: the commit
// pinned at hand-in for the current round and the push remote of the
// project's base branch. With record set, a missing pin is recorded now from
// the branch's head (as a pass does for a ticket in review); without, the
// head is only read.
func (e *Engine) planExport(ctx context.Context, t board.EngineTicket, project string, record bool) (exportPlan, error) {
	var p board.EngineProject
	found := false
	for _, x := range t.Projects {
		if x.ID == project {
			p, found = x, true
		}
	}
	if !found {
		return exportPlan{}, problemf("project %s is not a project of #%d", project, t.ID)
	}
	if p.Branch == "" {
		return exportPlan{}, problemf("#%d has no branch in project %s to export", t.ID, project)
	}
	proj, ok := e.st.Project(p.ID)
	if !ok || !proj.Enabled || proj.Missing {
		return exportPlan{}, problemf("project %s is missing or disabled", p.ID)
	}
	x := exportPlan{project: p.ID, repo: proj.Path, branch: p.Branch, base: p.Base}
	pins, err := e.pins.reviewed(t.ID, t.ReviewRound)
	if err != nil {
		return exportPlan{}, fmt.Errorf("read the commits handed in for review: %w", err)
	}
	x.commit = pins[p.ID]
	if x.commit == "" {
		head, err := e.git.RevParse(x.repo, "refs/heads/"+p.Branch+"^{commit}")
		if err != nil {
			return exportPlan{}, fmt.Errorf("read the branch %s: %w", p.Branch, err)
		}
		x.commit, x.unpinned = head, true
		if record {
			merged := map[string]string{}
			for k, v := range pins {
				merged[k] = v
			}
			merged[p.ID] = head
			if err := e.pins.setReviewed(t.ID, t.ReviewRound, merged); err != nil {
				return exportPlan{}, fmt.Errorf("record the commit of %s: %w", p.Branch, err)
			}
		}
	}
	x.remote, err = e.git.RemoteFor(ctx, x.repo, p.Base)
	if err != nil {
		return exportPlan{}, err
	}
	x.forge, x.forgeErr = forge.Parse(x.remote.URL)
	return x, nil
}

// withPush is x with what omo pushed (r) in place of what the project's
// remote and branch say now: an export job is told about, and checked
// against, the push omo made, whatever the config says by then.
func (x exportPlan) withPush(r pushRecord) exportPlan {
	x.remote = gitops.Remote{Name: r.Remote, URL: r.Address}
	x.forge, x.forgeErr = forge.Parse(r.Address)
	x.commit, x.branch = r.Commit, r.Branch
	return x
}

// promptData is the export data of the job's prompt.
func (x exportPlan) promptData(folder string) prompts.ExportData {
	d := prompts.ExportData{
		Folder: folder, Remote: x.remote.Name, Address: x.remote.Address(),
		RemoteWeb: x.forge.Web, Repo: x.forge.Repo, Forge: x.forge.Kind, CLI: forge.CLIFor(x.forge.Kind),
		Commit: x.commit, Branch: x.branch, Base: x.base,
	}
	c, ok := forge.For(x.forge, x.branch, x.base, lookPath)
	switch {
	case ok:
		d.Command, d.EditCommand = c.Line, c.Edit
	case x.forgeErr != nil:
		d.Problem = "omo does not know the forge of this remote"
	case x.forge.Kind == "":
		d.Problem = "the remote is a folder on this machine, not a forge"
	default:
		if _, err := lookPath(d.CLI); err != nil {
			// The host matches forge's host name pattern: no quote in it.
			d.Problem = "no command line tool for " + x.forge.Host + ": " + d.CLI + " is not on omo's PATH"
		} else {
			d.Problem = "the branch names cannot be passed to " + d.CLI + " safely"
		}
	}
	return d
}

// prepareExport pushes the reviewed commit of project of t and makes the
// export job's folder; it returns the folder. A push that fails, or a
// remote omo does not push to, fails the export of this project (see
// projectExportFailed; the other projects go on) and returns
// errExportFailed: no job starts.
func (e *Engine) prepareExport(ctx context.Context, t board.EngineTicket, project string) (string, error) {
	x, err := e.planExport(ctx, t, project, true)
	var problem *startProblem
	if errors.As(err, &problem) {
		return "", err
	}
	rec := pushRecord{Commit: x.commit, Branch: x.branch, Remote: x.remote.Name, Address: x.remote.Address()}
	var prev pushRecord
	if err == nil {
		prev, _, err = e.pins.pushed(t.ID, project)
	}
	if err == nil {
		// Only the branch recorded for this ticket alone, never a base or
		// default branch, whatever the branch template made of its name.
		err = e.checkOwnBranch(ctx, t, projectOf(t, project), x.repo, x.branch)
	}
	if err == nil {
		// A push again after a failed job, or in a later review round,
		// may move the branch from the commit omo pushed before; any other
		// commit there is not omo's.
		before := ""
		// exact: both names are omo's own records of this ticket.
		if prev.Branch == x.branch && prev.Address == x.remote.Address() {
			before = prev.Commit
		}
		e.at("export-push")
		err = e.git.PushCommit(ctx, x.repo, x.remote, x.commit, x.branch, before)
	}
	if err == nil {
		err = e.pins.setPushed(t.ID, project, rec)
	}
	if err == nil {
		// What the pull request shows: the pushed commit since it left
		// the base branch.
		err = e.recordCommits(ctx, t.ID, project, x.repo, gitops.BranchRef(x.base), x.commit)
	}
	if err != nil {
		if hint := e.remoteHint(t.ID, project, err); hint != "" {
			e.comment(ctx, t.ID, hint)
		}
		e.projectExportFailed(ctx, t.ID, project, err.Error())
		return "", errExportFailed
	}
	// The push is announced once per ticket, project, commit, remote and
	// branch, also across a restart of omo: the record is kept with the
	// pins until the ticket is done.
	if prev != rec {
		msg := fmt.Sprintf("Pushed %s to %s (%s) as %s.", codeSpan(short(x.commit)), codeSpan(x.remote.Name),
			codeSpan(x.remote.Address()), codeSpan(x.branch))
		if x.unpinned {
			msg += " omo had no record of the commit handed in for review, so it pushed the commit the branch was at."
		}
		e.comment(ctx, t.ID, msg)
	}
	folder, err := e.exportFolder(t.ID, project)
	if err != nil {
		return "", err
	}
	tx := e.exportTextsFor(ctx, t, x)
	if err := writeExportFolder(folder, tx, notChecked); err != nil {
		return "", fmt.Errorf("write the export folder of #%d: %w", t.ID, err)
	}
	return folder, nil
}

// remoteHint says what the user can do when err, the failed export of
// project of ticket n, is that the project has no remote omo pushes to:
// give the repository one, or take the project off the ticket. It is ""
// for any other error. It goes into a comment of its own, so the export
// state and the attention flag keep the short reason.
func (e *Engine) remoteHint(n int64, project string, err error) string {
	if !errors.Is(err, gitops.ErrBadRemote) {
		return ""
	}
	repo := "<folder>"
	if p, ok := e.st.Project(project); ok {
		repo = p.Path
	}
	return fmt.Sprintf("omo cannot export project %s of #%d: %v.\n\nGive the repository a remote to push to (`git -C %s remote add origin <address>`) and export again, or take the project off the ticket (`omo ai ticket remove-project %d %s`, or Remove from ticket under Branches in the browser) and export the others.",
		code(project), n, err, repo, n, project)
}

// exportPromptData is the export data of the prompt of the export job of
// ticket n in project: the push omo recorded, else found again without
// pushing. What cannot be found stays empty, and the prompt then says
// there is no command.
func (e *Engine) exportPromptData(ctx context.Context, n int64, project string) prompts.ExportData {
	folder, _ := e.exportFolder(n, project)
	t, _, ok, err := e.read(ctx, n)
	if err != nil || !ok {
		return prompts.ExportData{Folder: folder, Problem: "omo could not read the ticket"}
	}
	x, err := e.planExport(ctx, t, project, false)
	if err != nil {
		e.log.Printf("engine: find the export of #%d in project %s: %v", n, project, err)
		return prompts.ExportData{Folder: folder, Problem: "omo could not find the remote of the project"}
	}
	if r, ok, err := e.pins.pushed(n, project); err != nil {
		e.log.Printf("engine: read the push of #%d in project %s: %v", n, project, err)
	} else if ok {
		x = x.withPush(r)
	}
	return x.promptData(folder)
}

// checkExportAddress checks the pull request address an export job of
// ticket a.Ticket reports against the remote omo pushed to for this export
// (recorded at the push; see forge.CheckAddress), not against the remote
// as it is configured now, which the job could have changed: a job cannot
// leave a link to another host, repository or page on the ticket. A remote
// that is a folder on this machine accepts no address; the job's prompt
// tells it to report a failure instead.
func (e *Engine) checkExportAddress(a board.Actor, address string) error {
	r, ok, err := e.pins.pushed(a.Ticket, a.Project)
	if err != nil {
		return api.Errorf(api.CodeConflict, "omo could not read what it pushed for #%d to check the address; try again", a.Ticket)
	}
	if !ok {
		return api.Errorf(api.CodeConflict, "omo has no record of a push of project %s to check the address against; report the failure with omo ai done --failed", a.Project)
	}
	f, err := forge.Parse(r.Address)
	if err != nil {
		return api.Errorf(api.CodeValidation, "omo cannot check an address for the remote of project %s; report the failure with omo ai done --failed", a.Project)
	}
	if err := forge.CheckAddress(f, address); err != nil {
		if errors.Is(err, forge.ErrLocalRemote) {
			return api.Errorf(api.CodeValidation, "%v; report the failure with omo ai done --failed \"the remote is a folder on this machine, not a forge\"", err)
		}
		return api.Errorf(api.CodeValidation, "%v", err)
	}
	return nil
}

// exportTitle is omo's own title of the pull request: the ticket's title
// without hidden characters, at most maxExportTitle characters.
func exportTitle(title string) string {
	title = strings.TrimSpace(strings.Map(func(r rune) rune {
		if textsafe.IsHidden(r) {
			return -1
		}
		return r
	}, title))
	if utf8.RuneCountInString(title) > maxExportTitle {
		title = strings.TrimSpace(string([]rune(title)[:maxExportTitle]))
	}
	return title
}

// writeExportFolder makes folder afresh (mode 0700, below a folder of mode
// 0700) and writes the material and omo's own title and body into it, with
// src as the record of where they come from (files of mode 0600). Whatever
// was there before, links included, is removed first and never followed.
func writeExportFolder(folder string, tx exportTexts, src textSource) error {
	parent := filepath.Dir(folder)
	if fi, err := os.Lstat(parent); err == nil && !fi.IsDir() {
		if err := os.Remove(parent); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return err
	}
	if err := os.RemoveAll(folder); err != nil {
		return err
	}
	if err := os.Mkdir(folder, 0o700); err != nil {
		return err
	}
	if err := replaceExportFile(folder, materialFile, tx.material); err != nil {
		return err
	}
	return writeTextFiles(folder, tx.title, tx.body, src)
}

// removeExportFolders removes the folders of export jobs whose ticket is
// no longer exporting. A link (or anything else that is not a
// folder) at <run>/export is removed, never followed.
func (e *Engine) removeExportFolders(snap board.EngineSnapshot) {
	exporting := map[string]bool{}
	for _, t := range snap.Tickets {
		if t.Column != api.ColumnReview || t.ReviewStage != api.ReviewStageExporting {
			continue
		}
		for _, p := range t.Projects {
			exporting[strconv.FormatInt(t.ID, 10)+"-"+p.ID] = true
		}
	}
	if e.o.Layout.Run == "" {
		return
	}
	dir := filepath.Join(e.o.Layout.Run, exportDir)
	// Like writeExportFolder, never follow what is not a real folder: it
	// is removed itself.
	if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
		if err := os.Remove(dir); err != nil {
			e.log.Printf("engine: remove the export folder: %v", err)
		}
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			e.log.Printf("engine: read the export folders: %v", err)
		}
		return
	}
	for _, en := range entries {
		if exporting[en.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, en.Name())); err != nil {
			e.log.Printf("engine: remove the export folder %s: %v", en.Name(), err)
		}
	}
}
