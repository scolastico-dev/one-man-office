//go:build !windows

package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/engine"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
)

// shellServer opens a server without an engine whose user shells are
// /bin/sh, away from the developer's home and shell configuration.
func shellServer(t *testing.T, mutate ...func(*serverconfig.Config)) (*Server, *browser) {
	t.Helper()
	isolateShells(t)
	s := newTestServer(t, mutate...)
	return s, newBrowser(t, s)
}

// isolateShells keeps the user shells a test opens away from the
// developer's home and shell configuration.
func isolateShells(t *testing.T) {
	t.Helper()
	isolateAgents(t)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
}

// openShell opens a user shell through the browser listener and expects 201.
func openShell(t *testing.T, b *browser, s *Server, req api.OpenTerminalRequest) api.UserTerminal {
	t.Helper()
	resp := asUser(b, s, "POST", "/api/terminals", req)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("open terminal: status %d: %s", resp.StatusCode, raw)
	}
	return body[api.UserTerminal](t, resp)
}

// refusedShell opens a user shell and expects the error status, code and,
// when not empty, message.
func refusedShell(t *testing.T, b *browser, s *Server, req any, status int, code, message string) {
	t.Helper()
	resp := asUser(b, s, "POST", "/api/terminals", req)
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("open terminal %+v: status %d, want %d: %s", req, resp.StatusCode, status, raw)
	}
	var env api.ErrorEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != code || (message != "" && env.Error.Message != message) {
		t.Fatalf("open terminal %+v: %s %q, want %s %q", req, env.Error.Code, env.Error.Message, code, message)
	}
}

// dialShell opens the websocket of user shell id through the browser
// listener.
func dialShell(t *testing.T, b *browser, id string, protocols []string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(b.srv.URL, "http")+"/api/terminals/"+id+"/socket", &websocket.DialOptions{
		Host: hostOmoLocalhost + ":" + b.port, Subprotocols: protocols, HTTPHeader: header,
	})
}

// shellConn opens the websocket of user shell id as the browser does.
func shellConn(t *testing.T, b *browser, s *Server, id string) *termOutput {
	t.Helper()
	c, _, err := dialShell(t, b, id, []string{"omo", webSocketTokenPrefix + s.token},
		http.Header{"Origin": {"http://" + hostOmoLocalhost + ":" + b.port}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return &termOutput{t: t, c: c}
}

// typeLine types line and Enter into a shell.
func (o *termOutput) typeLine(line string) {
	o.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := o.c.Write(ctx, websocket.MessageBinary, []byte(line+"\r")); err != nil {
		o.t.Fatal(err)
	}
}

var pidLine = regexp.MustCompile(`PID=(\d+)`)

// shellPID asks the shell for its process id, which is also its process
// group's id: a shell leads its own session.
func (o *termOutput) shellPID() int {
	o.t.Helper()
	o.typeLine("echo PID=$$")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		if m := pidLine.FindStringSubmatch(o.got.String()); m != nil {
			pid, _ := strconv.Atoi(m[1])
			return pid
		}
		kind, data, err := o.c.Read(ctx)
		if err != nil {
			o.t.Fatalf("read the shell: %v (got %q)", err, o.got.String())
		}
		if kind == websocket.MessageBinary {
			o.got.Write(data)
		}
	}
}

// waitGroupGone waits until no process of process group pgid is left.
func waitGroupGone(t *testing.T, pgid int) {
	t.Helper()
	waitGone(t, -pgid)
}

// waitGone waits until kill(pid, 0) finds nothing: no process (or, for a
// negative pid, no process group) is left. It kills what remains on failure.
func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("process (group) %d is still alive (%v)", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// terminalEvents returns the data of the events of kind after id.
func terminalEvents(t *testing.T, s *Server, kind string, after int64) []api.UserTerminal {
	t.Helper()
	var out []api.UserTerminal
	for _, raw := range settingsEvents(t, s, kind, after) {
		var d api.TerminalData
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d.Terminal)
	}
	return out
}

func listShells(t *testing.T, b *browser, s *Server) []api.UserTerminal {
	t.Helper()
	resp := asUser(b, s, "GET", "/api/terminals", nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	return body[[]api.UserTerminal](t, resp)
}

func realPath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// ---- tests ----

// Only the user may list, open, close or connect to a user shell, on both
// listeners; an agent learns nothing about which ids exist.
func TestTerminalRoutesAreUserOnly(t *testing.T) {
	s, b := shellServer(t)
	p := addProject(t, s)
	sh := openShell(t, b, s, api.OpenTerminalRequest{Project: p})

	for _, role := range []api.Role{api.RoleDeveloper, api.RolePM, api.RoleCEO} {
		sess := liveSession(t, s, role)
		for _, id := range []string{sh.ID, "0123456789abcdef0123456789abcdef"} {
			for _, c := range []struct {
				method, path string
				body         any
			}{
				{"GET", "/api/terminals", nil},
				{"POST", "/api/terminals", api.OpenTerminalRequest{Project: p}},
				{"POST", "/api/terminals", api.OpenTerminalRequest{Project: p, Ticket: sess.Ticket}},
				{"DELETE", "/api/terminals/" + id, nil},
				{"GET", "/api/terminals/" + id + "/socket", nil},
			} {
				rec := socketDo(t, s, c.method, c.path, c.body, sess.ID)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("%s as %s: %s %s: status %d: %s", role, sess.Name, c.method, c.path, rec.Code, rec.Body.Bytes())
				}
			}
		}
	}
	// Nothing an agent sent opened or closed a shell.
	if got := listShells(t, b, s); len(got) != 1 || got[0].ID != sh.ID || got[0].State != "running" {
		t.Fatalf("shells after the agents' attempts: %+v", got)
	}

	// Without the login token nothing is answered, whether the id exists
	// or not.
	for _, id := range []string{sh.ID, "nope"} {
		wantStatus(t, b.do("GET", "/api/terminals", nil, nil).StatusCode, http.StatusUnauthorized)
		wantStatus(t, b.do("DELETE", "/api/terminals/"+id, nil, nil).StatusCode, http.StatusUnauthorized)
		if _, resp, err := dialShell(t, b, id, []string{"omo"}, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("socket of %q without the token: %v %v", id, err, resp)
		}
		if _, resp, err := dialShell(t, b, id, []string{"omo", webSocketTokenPrefix + "wrong"}, nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("socket of %q with a wrong token: %v %v", id, err, resp)
		}
	}

	// The user may use them over the socket too (no session header).
	wantStatus(t, socketDo(t, s, "GET", "/api/terminals", nil, "").Code, http.StatusOK)

	// The terminal events are the user's: they belong to no ticket, so no
	// agent reads them through a ticket's events.
	dev := liveSession(t, s, api.RoleDeveloper)
	openShell(t, b, s, api.OpenTerminalRequest{Project: p})
	rec := socketDo(t, s, "GET", tp(*dev.Ticket)+"/events", nil, dev.ID)
	wantStatus(t, rec.Code, http.StatusOK)
	if strings.Contains(rec.Body.String(), "terminal.") {
		t.Fatalf("an agent reads terminal events: %s", rec.Body.Bytes())
	}
	wantStatus(t, socketDo(t, s, "GET", "/api/events", nil, dev.ID).Code, http.StatusForbidden)
}

func TestOpenTerminalInProject(t *testing.T) {
	s, b := shellServer(t)
	repo := committedRepo(t, "main")
	proj, err := s.settings.AddProject(repo, "app")
	if err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())
	after := lastEventID(t, s)

	sh := openShell(t, b, s, api.OpenTerminalRequest{Project: proj.ID})
	if sh.ID == "" || len(sh.ID) != 32 || sh.Dir != realPath(t, repo) || sh.Project != proj.ID || sh.Ticket != nil ||
		sh.State != "running" || sh.Title != "app" || sh.StartedAt.IsZero() || sh.EndedAt != nil {
		t.Fatalf("opened %+v", sh)
	}
	opened := terminalEvents(t, s, api.KindTerminalOpened, after)
	if len(opened) != 1 || opened[0].ID != sh.ID || opened[0].Dir != sh.Dir {
		t.Fatalf("terminal.opened events %+v", opened)
	}
	if got := listShells(t, b, s); len(got) != 1 || got[0].ID != sh.ID {
		t.Fatalf("listed %+v", got)
	}
	out := shellConn(t, b, s, sh.ID)
	out.typeLine("pwd -P; echo DONE$((1+1))")
	out.until("DONE2")
	if !strings.Contains(out.got.String(), sh.Dir) {
		t.Fatalf("the shell does not run in %s: %q", sh.Dir, out.got.String())
	}

	// The folder is never taken from the request.
	refusedShell(t, b, s, map[string]any{"project": proj.ID, "dir": "/"}, http.StatusBadRequest, api.CodeBadRequest, "")
	refusedShell(t, b, s, api.OpenTerminalRequest{}, http.StatusUnprocessableEntity, api.CodeValidation, "")
	for _, id := range []string{"nope", "../" + proj.ID, "/", repo} {
		refusedShell(t, b, s, api.OpenTerminalRequest{Project: id}, http.StatusUnprocessableEntity, api.CodeValidation, "")
	}
	// A project whose folder is gone.
	gone := committedRepo(t, "main")
	gp, err := s.settings.AddProject(gone, "gone")
	if err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(context.Background())
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, api.OpenTerminalRequest{Project: gp.ID}, http.StatusUnprocessableEntity, api.CodeProjectUnavailable, "")
}

func TestOpenTerminalInTicketWorktree(t *testing.T) {
	s, b := shellServer(t)
	ctx := context.Background()
	repo := committedRepo(t, "main")
	proj, err := s.settings.AddProject(repo, "app")
	if err != nil {
		t.Fatal(err)
	}
	other := addProject(t, s)
	s.waitSettingsEvents(ctx)
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{proj.ID}})
	if err != nil {
		t.Fatal(err)
	}
	n := tk.Number
	notStarted := fmt.Sprintf("No worktree yet: work on #%d has not started.", n)
	ticket := func(project string) api.OpenTerminalRequest {
		return api.OpenTerminalRequest{Project: project, Ticket: &n}
	}

	// Nothing recorded yet.
	refusedShell(t, b, s, ticket(""), http.StatusConflict, api.CodeConflict, notStarted)
	// Recorded, but the folder does not exist.
	wt := engine.WorktreeDir(s.opts.Layout.Worktrees, proj.ID, int64(n))
	if err := s.board.SetBranch(ctx, int64(n), proj.ID, "omo/1-work", wt); err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, ticket(proj.ID), http.StatusConflict, api.CodeConflict, notStarted)

	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	after := lastEventID(t, s)
	for _, project := range []string{"", proj.ID} {
		sh := openShell(t, b, s, ticket(project))
		if sh.Dir != realPath(t, wt) || sh.Ticket == nil || *sh.Ticket != n || sh.Project != proj.ID ||
			sh.Title != fmt.Sprintf("#%d app", n) || sh.State != "running" {
			t.Fatalf("opened %+v", sh)
		}
	}
	if got := terminalEvents(t, s, api.KindTerminalOpened, after); len(got) != 2 || got[0].Ticket == nil || *got[0].Ticket != n {
		t.Fatalf("terminal.opened events %+v", got)
	}
	// A project the ticket does not have, and an unknown ticket.
	refusedShell(t, b, s, ticket(other), http.StatusUnprocessableEntity, api.CodeValidation, "")
	missing := 999
	refusedShell(t, b, s, api.OpenTerminalRequest{Ticket: &missing}, http.StatusNotFound, api.CodeNotFound, "")

	// A folder recorded outside omo's worktrees, or one that leads there
	// through a symbolic link, is not opened.
	outside := t.TempDir()
	if err := s.board.SetBranch(ctx, int64(n), proj.ID, "omo/1-work", outside); err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, ticket(proj.ID), http.StatusConflict, api.CodeConflict, "")
	if err := os.Remove(wt); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, wt); err != nil {
		t.Fatal(err)
	}
	if err := s.board.SetBranch(ctx, int64(n), proj.ID, "omo/1-work", wt); err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, ticket(proj.ID), http.StatusConflict, api.CodeConflict, "")
	// Another ticket's worktree is not this ticket's.
	if err := os.Remove(wt); err != nil {
		t.Fatal(err)
	}
	stranger := engine.WorktreeDir(s.opts.Layout.Worktrees, proj.ID, int64(n)+1)
	if err := os.MkdirAll(stranger, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.board.SetBranch(ctx, int64(n), proj.ID, "omo/1-work", stranger); err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, ticket(proj.ID), http.StatusConflict, api.CodeConflict, "")

	// Several projects: the project must be named.
	two, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeStory, Title: "both", Projects: []string{proj.ID, other}})
	if err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, api.OpenTerminalRequest{Ticket: &two.Number}, http.StatusUnprocessableEntity, api.CodeValidation, "")

	// Done: work there is over.
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.board.SetBranch(ctx, int64(n), proj.ID, "omo/1-work", wt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Cancel(ctx, board.User, int64(n), api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, ticket(proj.ID), http.StatusConflict, api.CodeConflict,
		fmt.Sprintf("work on #%d is finished; open a terminal in the project instead", n))
}

// The ticket's worktree folder replaced by a link to another ticket's
// worktree, at exactly the path omo expects: refused.
func TestOpenTerminalRefusesLinkToSiblingWorktree(t *testing.T) {
	s, b := shellServer(t)
	ctx := context.Background()
	repo := committedRepo(t, "main")
	proj, err := s.settings.AddProject(repo, "app")
	if err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(ctx)
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{proj.ID}})
	if err != nil {
		t.Fatal(err)
	}
	n := tk.Number
	wt := engine.WorktreeDir(s.opts.Layout.Worktrees, proj.ID, int64(n))
	sibling := engine.WorktreeDir(s.opts.Layout.Worktrees, proj.ID, int64(n)+1)
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sibling, wt); err != nil {
		t.Fatal(err)
	}
	if err := s.board.SetBranch(ctx, int64(n), proj.ID, "omo/1-work", wt); err != nil {
		t.Fatal(err)
	}
	refusedShell(t, b, s, api.OpenTerminalRequest{Project: proj.ID, Ticket: &n}, http.StatusConflict, api.CodeConflict,
		fmt.Sprintf("the worktree recorded for #%d is not one omo made; open a terminal in the project instead", n))
	if len(listShells(t, b, s)) != 0 {
		t.Fatal("a shell was opened")
	}
}

func TestTerminalLimit(t *testing.T) {
	s, b := shellServer(t)
	p := addProject(t, s)
	var first api.UserTerminal
	for i := range 16 {
		sh := openShell(t, b, s, api.OpenTerminalRequest{Project: p})
		if i == 0 {
			first = sh
		}
	}
	refusedShell(t, b, s, api.OpenTerminalRequest{Project: p}, http.StatusConflict, api.CodeConflict, "16 terminals are open; close one first.")
	wantStatus(t, asUser(b, s, "DELETE", "/api/terminals/"+first.ID, nil).StatusCode, http.StatusNoContent)
	openShell(t, b, s, api.OpenTerminalRequest{Project: p})

	// Connections are bounded as well, together with the agent terminals.
	var conns []*websocket.Conn
	defer func() {
		for _, c := range conns {
			c.CloseNow()
		}
	}()
	shells := listShells(t, b, s)
	own := http.Header{"Origin": {"http://" + hostOmoLocalhost + ":" + b.port}}
	for i := range terminalConnections {
		c, _, err := dialShell(t, b, shells[i%len(shells)].ID, []string{"omo", webSocketTokenPrefix + s.token}, own)
		if err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	if c, resp, err := dialShell(t, b, shells[0].ID, []string{"omo", webSocketTokenPrefix + s.token}, own); err == nil {
		c.CloseNow()
		t.Fatal("a connection beyond the limit was accepted")
	} else if resp == nil || resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("beyond the limit: %v %v", err, resp)
	}
}

func TestTerminalSocketNeverEchoesToken(t *testing.T) {
	s, b := shellServer(t)
	p := addProject(t, s)
	sh := openShell(t, b, s, api.OpenTerminalRequest{Project: p})
	protocols := []string{"omo", webSocketTokenPrefix + s.token}

	evil := http.Header{"Origin": {"http://evil.example"}}
	if _, resp, err := dialShell(t, b, sh.ID, protocols, evil); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("from another origin: %v %v", err, resp)
	}
	if _, resp, err := dialShell(t, b, "0123456789abcdef0123456789abcdef", protocols, nil); err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown terminal: %v %v", err, resp)
	} else if raw, _ := io.ReadAll(resp.Body); strings.Contains(string(raw), s.token) {
		t.Fatal("the answer for an unknown terminal contains the token")
	}

	c, resp, err := dialShell(t, b, sh.ID, protocols, http.Header{"Origin": {"http://" + hostOmoLocalhost + ":" + b.port}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if c.Subprotocol() != "omo" {
		t.Fatalf("subprotocol %q", c.Subprotocol())
	}
	for k, vs := range resp.Header {
		for _, v := range vs {
			if strings.Contains(v, s.token) {
				t.Fatalf("response header %s echoes the login token", k)
			}
		}
	}
	out := &termOutput{t: t, c: c}
	out.typeLine("echo hello$((40+2))")
	out.until("hello42")

	// With origin_check off, the handshake still refuses another site.
	s2, b2 := shellServer(t, func(c *serverconfig.Config) { c.OriginCheck = false })
	sh2 := openShell(t, b2, s2, api.OpenTerminalRequest{Project: addProject(t, s2)})
	if c, resp, err := dialShell(t, b2, sh2.ID, []string{"omo", webSocketTokenPrefix + s2.token}, evil); err == nil {
		c.CloseNow()
		t.Fatal("a foreign Origin opened the shell with origin_check off")
	} else if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign Origin with origin_check off: %v %v", err, resp)
	}
}

// The server itself may run with an agent's identity in its environment
// (started from an agent's shell); the user's shell never inherits it.
func TestShellEnvironmentHasNoAgentID(t *testing.T) {
	isolateShells(t)
	t.Setenv("OMO_AGENT_ID", "agent-secret-id")
	t.Setenv("OMO_FAKE_SCRIPT_DEVELOPER", "fake-script")
	s := newTestServer(t)
	b := newBrowser(t, s)
	sock, err := s.ListenSocket()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sock.Close() })
	sh := openShell(t, b, s, api.OpenTerminalRequest{Project: addProject(t, s)})
	out := shellConn(t, b, s, sh.ID)
	out.typeLine("env; echo END$((1+1))")
	out.until("END2")
	got := out.got.String()
	if strings.Contains(got, "OMO_AGENT_ID") || strings.Contains(got, "agent-secret-id") || strings.Contains(got, "OMO_FAKE") {
		t.Fatalf("the shell has an agent identity:\n%s", got)
	}
	if !strings.Contains(got, "OMO_SOCKET="+s.socketEndpoint()) || !strings.Contains(got, "TERM=xterm-256color") {
		t.Fatalf("the shell lacks OMO_SOCKET or TERM:\n%s", got)
	}
}

func TestDeleteTerminal(t *testing.T) {
	s, b := shellServer(t)
	p := addProject(t, s)
	after := lastEventID(t, s)

	// A running shell: DELETE ends its processes and forgets it.
	sh := openShell(t, b, s, api.OpenTerminalRequest{Project: p})
	pid := shellConn(t, b, s, sh.ID).shellPID()
	wantStatus(t, asUser(b, s, "DELETE", "/api/terminals/"+sh.ID, nil).StatusCode, http.StatusNoContent)
	waitGroupGone(t, pid)
	if got := terminalEvents(t, s, api.KindTerminalClosed, after); len(got) != 1 || got[0].ID != sh.ID {
		t.Fatalf("terminal.closed events %+v", got)
	}
	if got := listShells(t, b, s); len(got) != 0 {
		t.Fatalf("listed after delete: %+v", got)
	}
	browserError(t, asUser(b, s, "DELETE", "/api/terminals/"+sh.ID, nil), http.StatusNotFound, api.CodeNotFound)
	if _, resp, err := dialShell(t, b, sh.ID, []string{"omo", webSocketTokenPrefix + s.token}, nil); err == nil || resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("socket of a deleted shell: %v %v", err, resp)
	}

	// A shell that exits stays listed as exited until it is deleted.
	sh = openShell(t, b, s, api.OpenTerminalRequest{Project: p})
	out := shellConn(t, b, s, sh.ID)
	pid = out.shellPID()
	out.typeLine("exit 3")
	waitFor(t, "the shell to exit", func() bool {
		got := listShells(t, b, s)
		return len(got) == 1 && got[0].State == "exited"
	})
	waitGroupGone(t, pid)
	got := listShells(t, b, s)[0]
	if got.EndedAt == nil || got.ExitError == "" {
		t.Fatalf("exited shell %+v", got)
	}
	if ev := terminalEvents(t, s, api.KindTerminalExited, after); len(ev) != 1 || ev[0].ID != sh.ID || ev[0].State != "exited" {
		t.Fatalf("terminal.exited events %+v", ev)
	}
	wantStatus(t, asUser(b, s, "DELETE", "/api/terminals/"+sh.ID, nil).StatusCode, http.StatusNoContent)
	if got := terminalEvents(t, s, api.KindTerminalClosed, after); len(got) != 2 {
		t.Fatalf("terminal.closed events %+v", got)
	}
}

// Run's stop ends every user shell with all its processes, after the
// listeners stopped, without waiting for them beyond its bound.
func TestRunStopEndsShells(t *testing.T) {
	isolateShells(t)
	l := testHome(t)
	_, stop := startRun(t, Options{Layout: l, Config: testConfig(), Version: "test"}, io.Discard)
	client := socketClient(l)
	p, err := json.Marshal(api.CreateProject{Path: committedRepo(t, "main")})
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string, raw []byte, out any) {
		t.Helper()
		req, _ := http.NewRequest("POST", "http://omo"+path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			got, _ := io.ReadAll(resp.Body)
			t.Fatalf("POST %s: %d %s", path, resp.StatusCode, got)
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	var proj api.Project
	post("/api/projects", p, &proj)
	raw, _ := json.Marshal(api.OpenTerminalRequest{Project: proj.ID})
	var sh api.UserTerminal
	post("/api/terminals", raw, &sh)
	// The shell starts a child in its own process group; both must go.
	marker := filepath.Join(t.TempDir(), "child")
	ctxDial, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctxDial, "ws://omo/api/terminals/"+sh.ID+"/socket", &websocket.DialOptions{HTTPClient: client, Subprotocols: []string{"omo"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	out := &termOutput{t: t, c: c}
	pid := out.shellPID()
	out.typeLine("sleep 600 & echo $! > " + marker + "; echo STARTED$((1+1))")
	out.until("STARTED2")
	childRaw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	child, _ := strconv.Atoi(strings.TrimSpace(string(childRaw)))
	begin := time.Now()
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(begin); d > shutdownTimeout {
		t.Fatalf("stopping took %s with a shell open", d)
	}
	waitGroupGone(t, pid)
	waitGone(t, child)
}
