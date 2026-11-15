package server

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/engine"
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
		Env:      func() []string { return userterm.ShellEnv(os.Environ(), s.socketEndpoint()) },
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
// folder, or the worktree omo made for the ticket in that project. The
// request names a project and a ticket, never a path.
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
// its worktree folder. Without a ticket it is the project's folder. The
// folder is resolved through symbolic links and must be a directory.
func (s *Server) shellSpec(r *http.Request, req api.OpenTerminalRequest) (userterm.Spec, error) {
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
	if tp.Worktree == "" {
		return userterm.Spec{}, notStarted
	}
	dir, ok := existingDir(tp.Worktree)
	if !ok {
		return userterm.Spec{}, notStarted
	}
	want, ok := existingDir(engine.WorktreeDir(s.opts.Layout.Worktrees, tp.Project, int64(n)))
	root, rootOK := existingDir(s.opts.Layout.Worktrees)
	if !ok || !rootOK || dir != want || !inside(root, dir) {
		return userterm.Spec{}, api.Errorf(api.CodeConflict, "the worktree recorded for #%d is not one omo made; open a terminal in the project instead", n)
	}
	title := fmt.Sprintf("#%d %s", n, tp.Project)
	if p, ok := s.settings.Project(tp.Project); ok {
		title = fmt.Sprintf("#%d %s", n, p.Name)
	}
	return userterm.Spec{Dir: dir, Title: title, Project: tp.Project, Ticket: &n, Hold: true}, nil
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
