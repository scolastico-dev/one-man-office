package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/engine"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/userterm"
)

// shellCloseGrace bounds how long Close waits for the user's shells to be
// reaped once they are killed.
const shellCloseGrace = 2 * time.Second

// newShells makes the manager of the user's shells. A shell in a ticket's
// worktree holds that worktree in the engine while it runs; without an
// engine nothing removes worktrees and no hold is taken. Every change is
// recorded as a terminal.* event on the user's stream.
func (s *Server) newShells() *userterm.Manager {
	o := userterm.Options{
		Env:      func() []string { return userterm.ShellEnv(childenv.Inherited(), s.socketEndpoint()) },
		OnChange: s.shellChanged,
	}
	if s.engine != nil {
		o.Hold = s.engine.HoldWorktree
	}
	return userterm.New(o)
}

// shellChanged records a change of the user's shells as an event. Events
// without a ticket reach only the user's event stream.
func (s *Server) shellChanged(c userterm.Change) {
	var kind string
	switch c.Kind {
	case "opened":
		kind = api.KindTerminalOpened
	case "exited":
		kind = api.KindTerminalExited
	case "closed":
		kind = api.KindTerminalClosed
	default:
		return
	}
	if err := s.appendSystemEvent(kind, api.TerminalData{Terminal: userTerminal(c.Info)}); err != nil {
		s.log.Printf("record %s: %v", kind, err)
	}
	s.events.Wake()
}

func userTerminal(i userterm.Info) api.UserTerminal {
	return api.UserTerminal{
		ID: i.ID, Title: i.Title, Dir: i.Dir, Project: i.Project, Ticket: i.Ticket,
		State: i.State, ExitError: i.ExitError, StartedAt: i.StartedAt, EndedAt: i.EndedAt,
	}
}

// registerTerminals adds the routes of the user's own shells. A shell runs
// with the user's permissions and no agent identity, so every route is the
// user's only, on both listeners: an agent session gets 403 before anything
// is looked up.
func (s *Server) registerTerminals(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/terminals", s.listTerminals)
	mux.HandleFunc("POST /api/terminals", s.openTerminal)
	mux.HandleFunc("GET /api/terminals/{id}/socket", s.terminalSocket)
	mux.HandleFunc("DELETE /api/terminals/{id}", s.closeTerminal)
}

func (s *Server) listTerminals(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	list := s.shells.List()
	out := make([]api.UserTerminal, 0, len(list))
	for _, i := range list {
		out = append(out, userTerminal(i))
	}
	writeJSON(w, http.StatusOK, out)
}

// openTerminal opens a shell in a folder the server picks: the project's
// folder, the worktree omo made for the ticket in that project, or the
// user's home folder. The request names a project, a ticket or home, never
// a path.
func (s *Server) openTerminal(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	var req api.OpenTerminalRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	spec, err := s.shellSpec(r, req)
	if err != nil {
		writeError(w, r, err)
		return
	}
	info, err := s.shells.Open(spec)
	switch {
	case errors.Is(err, userterm.ErrTooMany):
		writeError(w, r, api.Errorf(api.CodeConflict, "16 terminals are open; close one first."))
		return
	case errors.Is(err, userterm.ErrClosed):
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is stopping"))
		return
	case err != nil:
		if _, serr := os.Stat(spec.Dir); serr != nil && spec.Ticket != nil {
			// The worktree went between the check and the start.
			writeError(w, r, api.Errorf(api.CodeConflict, "No worktree yet: work on #%d has not started.", *spec.Ticket))
			return
		}
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, userTerminal(info))
}

// shellSpec resolves where a shell opens. With a ticket it is that ticket's
// worktree in the named project (the project may be left out when the
// ticket has one), refused once the ticket is Done, before work started, or
// when the recorded folder is not the one omo makes for that ticket below
// its worktree folder (see ticketWorktree); for a task without a project
// its working folder (see workdirShell). Without a ticket it is the
// project's folder, resolved through symbolic links (the user's own); it
// must be a directory. With home it is the user's home folder (homeShell).
func (s *Server) shellSpec(r *http.Request, req api.OpenTerminalRequest) (userterm.Spec, error) {
	if req.Home {
		return homeShell(req)
	}
	if req.Ticket == nil {
		if req.Project == "" {
			return userterm.Spec{}, api.Errorf(api.CodeValidation, "project is required")
		}
		p, ok := s.settings.Project(req.Project)
		if !ok {
			return userterm.Spec{}, api.Errorf(api.CodeValidation, "unknown project %q", req.Project)
		}
		dir, ok := existingDir(p.Path)
		if p.Missing || !ok {
			return userterm.Spec{}, api.Errorf(api.CodeProjectUnavailable, "the folder of project %s is missing", p.Name)
		}
		return userterm.Spec{Dir: dir, Title: p.Name, Project: p.ID}, nil
	}

	n := *req.Ticket
	t, err := s.board.Get(r.Context(), board.User, int64(n))
	if err != nil {
		return userterm.Spec{}, err
	}
	if len(t.Projects) == 0 && t.Type == api.TypeTask {
		if req.Project != "" {
			return userterm.Spec{}, api.Errorf(api.CodeValidation, "#%d has no project %q", n, req.Project)
		}
		return s.workdirShell(t)
	}
	var tp *api.TicketProject
	switch {
	case req.Project != "":
		for i := range t.Projects {
			if t.Projects[i].Project == req.Project {
				tp = &t.Projects[i]
			}
		}
		if tp == nil {
			return userterm.Spec{}, api.Errorf(api.CodeValidation, "#%d has no project %q", n, req.Project)
		}
	case len(t.Projects) == 1:
		tp = &t.Projects[0]
	default:
		return userterm.Spec{}, api.Errorf(api.CodeValidation, "#%d has several projects; name the project", n)
	}
	if t.Column == api.ColumnDone {
		return userterm.Spec{}, api.Errorf(api.CodeConflict, "work on #%d is finished; open a terminal in the project instead", n)
	}
	notStarted := api.Errorf(api.CodeConflict, "No worktree yet: work on #%d has not started.", n)
	notOurs := api.Errorf(api.CodeConflict, "the worktree recorded for #%d is not one omo made; open a terminal in the project instead", n)
	if tp.Worktree == "" {
		return userterm.Spec{}, notStarted
	}
	dir, fi, err := s.ticketWorktree(tp.Worktree, tp.Project, int64(n))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return userterm.Spec{}, notStarted
	case err != nil:
		return userterm.Spec{}, notOurs
	}
	p, ok := s.settings.Project(tp.Project)
	if !ok {
		return userterm.Spec{}, notOurs
	}
	title := fmt.Sprintf("#%d %s", n, p.Name)
	// The user's shell setup may run git there (a prompt showing git status)
	// and git would read the repository the .git entry names: the agent's own
	// config, with commands, if it replaced the gitfile git wrote.
	foreignGit := api.Errorf(api.CodeConflict, "the .git in the worktree of #%d is not the one git wrote for it; open a terminal in the project instead", n)
	checkGit := func(ctx context.Context) error {
		err := s.git.CheckWorktreeGitFile(ctx, p.Path, dir)
		if err == nil {
			return nil
		}
		if !errors.Is(err, gitops.ErrForeignGitFile) && !errors.Is(err, gitops.ErrNotRecorded) && !errors.Is(err, gitops.ErrNotAFolder) {
			s.log.Printf("check the .git of the worktree of #%d: %v", n, err)
		}
		return foreignGit
	}
	if err := checkGit(r.Context()); err != nil {
		return userterm.Spec{}, err
	}
	// The agent may swap its folder or its .git after these checks: the same
	// checks run again right before the shell starts, and the folder must
	// still be the one checked here.
	verify := func() error {
		now, err := os.Lstat(dir)
		if err != nil || now.Mode().Type() != fs.ModeDir || !os.SameFile(fi, now) {
			return notOurs
		}
		return checkGit(context.Background())
	}
	return userterm.Spec{Dir: dir, Title: title, Project: tp.Project, Ticket: &n, Hold: true, Verify: verify}, nil
}

// workdirShell is the spec of a shell in the working folder of t, a task
// without a project: also once t is done, since the folder stays for the
// user to look at what the agent left there (a proof of concept, notes).
// The recorded folder must be exactly the one omo makes for t below its
// working folders' folder and a plain folder, not a link; it is held while
// the shell runs, so it is not removed under it. No git check runs: omo
// keeps no repository there, and what the agent put there is its own work.
func (s *Server) workdirShell(t api.Ticket) (userterm.Spec, error) {
	n := t.Number
	notStarted := api.Errorf(api.CodeConflict, "No working folder: work on #%d has not started, or the folder was removed.", n)
	notOurs := api.Errorf(api.CodeConflict, "the working folder recorded for #%d is not one omo made", n)
	root := s.opts.Layout.Workdirs
	if t.Workdir == "" {
		return userterm.Spec{}, notStarted
	}
	if root == "" || filepath.Clean(t.Workdir) != engine.WorkdirDir(root, int64(n)) {
		return userterm.Spec{}, notOurs
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return userterm.Spec{}, notOurs
	}
	dir := engine.WorkdirDir(realRoot, int64(n))
	fi, err := os.Lstat(dir)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return userterm.Spec{}, notStarted
	case err != nil || fi.Mode().Type() != fs.ModeDir:
		return userterm.Spec{}, notOurs
	}
	verify := func() error {
		now, err := os.Lstat(dir)
		if err != nil || now.Mode().Type() != fs.ModeDir || !os.SameFile(fi, now) {
			return notOurs
		}
		return nil
	}
	return userterm.Spec{Dir: dir, Title: fmt.Sprintf("#%d working folder", n), Ticket: &n, Hold: true, Verify: verify}, nil
}

// homeShell is the spec of a shell in the user's home folder: the operating
// system's home of the user omo runs as (not omo's home), resolved through
// symbolic links like a project folder (the user's own).
func homeShell(req api.OpenTerminalRequest) (userterm.Spec, error) {
	if req.Project != "" || req.Ticket != nil {
		return userterm.Spec{}, api.Errorf(api.CodeValidation, "home cannot be combined with project or ticket")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return userterm.Spec{}, api.Errorf(api.CodeValidation, "omo knows no home folder of the user: %v", err)
	}
	dir, ok := existingDir(home)
	if !ok {
		return userterm.Spec{}, api.Errorf(api.CodeValidation, "the home folder %s is missing", home)
	}
	return userterm.Spec{Dir: dir, Title: "Home"}, nil
}

// errNotOmos marks a recorded worktree that is not the folder omo makes.
var errNotOmos = errors.New("not the worktree omo makes")

// ticketWorktree checks the worktree recorded for ticket n in project and
// returns the folder a shell may open in, with what Lstat found there. The
// recorded path must be exactly the one omo makes below its worktrees
// folder (engine.WorktreeDir), compared as written: it is never resolved
// through links. The returned folder is that path below the worktrees
// folder with links resolved in the worktrees folder only (omo's own), and
// each folder below it must be a plain folder, not a link: an agent can
// replace its worktree by a link to another ticket's. A folder that does not
// exist gives an error wrapping os.ErrNotExist.
func (s *Server) ticketWorktree(recorded, project string, n int64) (string, os.FileInfo, error) {
	root := s.opts.Layout.Worktrees
	if root == "" || !filepath.IsAbs(recorded) {
		return "", nil, errNotOmos
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", nil, errNotOmos
	}
	clean := filepath.Clean(recorded)
	if clean != engine.WorktreeDir(root, project, n) && clean != engine.WorktreeDir(realRoot, project, n) {
		return "", nil, errNotOmos
	}
	rel, err := filepath.Rel(realRoot, engine.WorktreeDir(realRoot, project, n))
	if err != nil || !inside(realRoot, filepath.Join(realRoot, rel)) {
		return "", nil, errNotOmos
	}
	dir := realRoot
	var fi os.FileInfo
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		dir = filepath.Join(dir, part)
		fi, err = os.Lstat(dir)
		if err != nil {
			return "", nil, err
		}
		if fi.Mode().Type() != fs.ModeDir {
			return "", nil, errNotOmos
		}
	}
	return dir, fi, nil
}

// existingDir resolves p through symbolic links and reports whether it is a
// directory.
func existingDir(p string) (string, bool) {
	if p == "" || !filepath.IsAbs(p) {
		return "", false
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", false
	}
	st, err := os.Stat(real)
	if err != nil || !st.IsDir() {
		return "", false
	}
	return real, true
}

// inside reports whether dir lies below root (both resolved).
func inside(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// terminalSocket connects the user's websocket to a shell, running or
// exited (its output is replayed). It goes through the browser's checks
// (Host, Origin, login token) like the agent terminals, shares their
// connection limit, and selects the subprotocol "omo" only, so a token
// offered as omo-token.<token> is never echoed.
func (s *Server) terminalSocket(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	term, ok := s.shells.Terminal(r.PathValue("id"))
	if !ok {
		writeError(w, r, api.Errorf(api.CodeNotFound, "no such terminal"))
		return
	}
	term.ServeWebSocket(r.Context(), w, r, s.terminals)
}

// closeTerminal ends a shell if it runs, releases what it held and forgets
// it.
func (s *Server) closeTerminal(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	if err := s.shells.Close(r.PathValue("id")); err != nil {
		if errors.Is(err, userterm.ErrUnknown) {
			writeError(w, r, api.Errorf(api.CodeNotFound, "no such terminal"))
			return
		}
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
