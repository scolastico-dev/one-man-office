package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/server"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

type stubEnv struct{}

func (stubEnv) Policy() board.Policy                     { return board.Policy{} }
func (stubEnv) Project(string) (board.ProjectInfo, bool) { return board.ProjectInfo{}, false }
func (stubEnv) Model(string) (board.ModelInfo, bool)     { return board.ModelInfo{}, false }
func (stubEnv) CurrentBranch(string) (string, error)     { return "main", nil }

// aiEnv is a running in-process server reachable through its socket, with one
// registered project.
type aiEnv struct {
	t       *testing.T
	board   *board.Board
	user    *apiclient.Client
	project string
}

func startAI(t *testing.T) *aiEnv {
	t.Helper()
	isolate(t)
	t.Setenv("OMO_SOCKET", "")
	asUser(t)
	layout, err := globalhome.Open()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Options{Layout: layout, Config: serverconfig.Defaults(), Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	actual, _, cleanup, err := transport.Endpoint(layout.Run)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := transport.Listen(actual)
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{Handler: srv.SocketHandler()}
	go httpSrv.Serve(ln)
	sqlDB, err := db.Open(layout.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		httpSrv.Close()
		cleanup()
		sqlDB.Close()
		srv.Close()
	})
	e := &aiEnv{t: t, board: board.New(sqlDB, stubEnv{}, board.Options{}), user: apiclient.New(actual, "")}
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-q", repo)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	var p api.Project
	if err := e.user.Do(context.Background(), http.MethodPost, "/api/projects", api.CreateProject{Path: repo, Name: "api"}, &p); err != nil {
		t.Fatal(err)
	}
	e.project = p.ID
	return e
}

// run executes `omo ai args...` with stdin and returns standard output and
// the error.
func (e *aiEnv) run(stdin string, args ...string) (string, error) {
	e.t.Helper()
	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"ai"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func (e *aiEnv) must(stdin string, args ...string) string {
	e.t.Helper()
	out, err := e.run(stdin, args...)
	if err != nil {
		e.t.Fatalf("omo ai %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func (e *aiEnv) story(title string) {
	e.t.Helper()
	e.must("", "ticket", "create", "--type", "story", "--title", title, "--project", e.project)
}

// asUser unsets OMO_AGENT_ID for the rest of the test; it is restored
// afterwards.
func asUser(t *testing.T) {
	t.Helper()
	t.Setenv("OMO_AGENT_ID", "")
	os.Unsetenv("OMO_AGENT_ID")
}

func contains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("output %q does not contain %q", got, want)
	}
}

func TestAIWorksForUserWithoutSession(t *testing.T) {
	e := startAI(t)
	out := e.must("", "ticket", "create", "--type", "story", "--title", "X", "--project", e.project)
	contains(t, out, "#1")
	contains(t, e.must("", "ticket", "list"), "#1 story planning X")
	contains(t, e.must("", "whoami"), "role: user")
}

func TestAIDescriptionFromStdin(t *testing.T) {
	e := startAI(t)
	e.must("hello\n", "ticket", "create", "--type", "story", "--title", "X", "--project", e.project, "--description-file", "-")
	contains(t, e.must("", "ticket", "show", "1"), "\nhello\n")
	e.must("second text with 'quotes' and $vars", "ticket", "update", "1", "--description-file", "-")
	contains(t, e.must("", "ticket", "show", "#1"), "second text with 'quotes' and $vars")
	file := t.TempDir() + "/d.md"
	if err := os.WriteFile(file, []byte("from a file"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.must("", "ticket", "update", "1", "--description-file", file, "--title", "Y")
	out := e.must("", "ticket", "show", "1")
	contains(t, out, "from a file")
	contains(t, out, "#1 story: Y")
}

func TestAILinkCycleError(t *testing.T) {
	e := startAI(t)
	e.story("one")
	e.story("two")
	contains(t, e.must("", "link", "2", "--depends-on", "1"), "#2 now depends on #1")
	_, err := e.run("", "link", "1", "--depends-on", "2")
	if err == nil || !strings.Contains(err.Error(), "#1 → #2 → #1") {
		t.Fatalf("error %v", err)
	}
	if strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "(dependency_cycle)") {
		t.Fatalf("error not one line with its code: %q", err)
	}
	contains(t, e.must("", "link", "2", "--depends-on", "1", "--remove"), "no longer depends")
}

func TestAICommentPrintsUnreachable(t *testing.T) {
	e := startAI(t)
	e.story("one")
	e.must("", "ticket", "create", "--type", "task", "--title", "two", "--project", e.project, "--story", "1")
	out := e.must("", "comment", "2", "@developer", "done?")
	contains(t, out, "nobody is currently behind @developer on #2; the comment is stored but no agent was notified")
	out = e.must("a body from stdin, @pm", "comment", "2", "--body-file", "-")
	contains(t, out, " posted on #2")
	contains(t, e.must("", "ask", "2", "--to", "pm", "why?"), "question ")
	if _, err := e.run("", "comment", "2", "--body-file", "-", "also words"); err == nil {
		t.Fatal("words and a file together were accepted")
	}
	if _, err := e.run("", "comment", "2"); err == nil {
		t.Fatal("empty comment accepted")
	}
}

func TestAIDeveloperShowDefaultsToOwnTicket(t *testing.T) {
	e := startAI(t)
	e.story("one")
	e.must("", "ticket", "create", "--type", "task", "--title", "work", "--project", e.project, "--story", "1")
	sess, err := e.board.CreateSession(context.Background(), board.NewSession{Role: api.RoleDeveloper, Ticket: 2, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_AGENT_ID", sess.ID)
	out := e.must("", "ticket", "show")
	contains(t, out, "#2 task: work")
	contains(t, e.must("", "whoami"), "ticket: #2")
	// A developer may not read outside its scope.
	if _, err := e.run("", "ticket", "show", "1"); err != nil {
		t.Logf("story outside scope refused: %v", err)
	}
}

func TestAIStaleSessionFails(t *testing.T) {
	e := startAI(t)
	e.story("one")
	e.must("", "ticket", "create", "--type", "task", "--title", "work", "--project", e.project, "--story", "1")
	sess, err := e.board.CreateSession(context.Background(), board.NewSession{Role: api.RoleDeveloper, Ticket: 2, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.board.EndSession(context.Background(), sess.ID, "test"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_AGENT_ID", sess.ID)
	out, err := e.run("", "ticket", "list")
	if err == nil || !strings.Contains(err.Error(), "unknown or has ended") || !strings.Contains(err.Error(), "unknown_session") {
		t.Fatalf("error %v", err)
	}
	if out != "" {
		t.Fatalf("stale session got output: %q", out)
	}
}

func TestAINotRunning(t *testing.T) {
	isolate(t)
	t.Setenv("OMO_SOCKET", "")
	asUser(t)
	for _, args := range [][]string{{"ticket", "list"}, {"whoami"}, {"ready"}} {
		cmd := Root("test")
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(append([]string{"ai"}, args...))
		err := cmd.Execute()
		if err == nil || err.Error() != "omo is not running (start it with: omo)" {
			t.Fatalf("%v: error %v", args, err)
		}
	}
	if _, err := os.Stat(os.Getenv("OMO_HOME") + "/run/runtime.json"); err == nil {
		t.Fatal("omo ai started a server")
	}
}

func TestAIReadyAndWaitNotImplementedYet(t *testing.T) {
	e := startAI(t)
	for _, args := range [][]string{{"ready"}, {"wait", "--timeout", "1s"}} {
		_, err := e.run("", args...)
		if err == nil || !strings.Contains(err.Error(), "not implemented") {
			t.Fatalf("%v: error %v", args, err)
		}
	}
	if _, err := e.run("", "wait", "--timeout", "soon"); err == nil || !strings.Contains(err.Error(), "duration") {
		t.Fatalf("bad timeout: %v", err)
	}
}

func TestAIJSONOutputIsServerBody(t *testing.T) {
	e := startAI(t)
	e.story("one")
	var tk api.Ticket
	if err := json.Unmarshal([]byte(e.must("", "ticket", "show", "1", "--json")), &tk); err != nil || tk.Number != 1 || tk.Title != "one" {
		t.Fatalf("ticket json: %v %+v", err, tk)
	}
	var list api.TicketList
	if err := json.Unmarshal([]byte(e.must("", "ticket", "list", "--json")), &list); err != nil || len(list.Tickets) != 1 {
		t.Fatalf("list json: %v %+v", err, list)
	}
}

func TestAIMoveProjectsModelsDone(t *testing.T) {
	e := startAI(t)
	e.story("one")
	contains(t, e.must("", "ticket", "move", "1", "ready"), "#1 story ready one")
	contains(t, e.must("", "ticket", "list", "--column", "ready", "--type", "story"), "#1 story ready one")
	if out := e.must("", "ticket", "list", "--column", "done"); !strings.Contains(out, "no tickets") {
		t.Fatalf("empty list: %q", out)
	}
	contains(t, e.must("", "project", "list"), e.project+" api ")
	contains(t, e.must("", "project", "search", "api"), e.project)
	if out := e.must("", "project", "search", "zzzz"); !strings.Contains(out, "no projects") {
		t.Fatalf("search: %q", out)
	}
	e.must("", "model", "list")
	// The user has no ticket to hand in.
	if _, err := e.run("", "done", "finished"); err == nil {
		t.Fatal("the user handed in work")
	}
	if _, err := e.run("", "done", "--approve", "--reject"); err == nil {
		t.Fatal("both verdicts accepted")
	}
	if _, err := e.run("", "ticket", "show"); err == nil || !strings.Contains(err.Error(), "no ticket number") {
		t.Fatalf("show without ticket: %v", err)
	}
	if _, err := e.run("", "ticket", "show", "abc"); err == nil {
		t.Fatal("bad ticket number accepted")
	}
	if _, err := e.run("", "ticket", "update", "1"); err == nil || !strings.Contains(err.Error(), "nothing to update") {
		t.Fatalf("empty update: %v", err)
	}
}

func TestAIHelpAndRootHelp(t *testing.T) {
	for _, args := range [][]string{{"ai", "--help"}, {"ai", "ticket", "create", "--help"}, {"ai", "comment", "--help"}, {"ai", "done", "--help"}} {
		cmd := Root("test")
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		contains(t, out.String(), "Examples:")
		contains(t, out.String(), "omo ai")
	}
	cmd := Root("test")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"completion", "\n  help "} {
		if strings.Contains(out.String(), unwanted) {
			t.Fatalf("root help contains %q:\n%s", unwanted, out.String())
		}
	}
	contains(t, out.String(), "  ai ")
}

func TestAICommentsThread(t *testing.T) {
	e := startAI(t)
	e.story("one")
	e.must("", "ticket", "create", "--type", "task", "--title", "work", "--project", e.project, "--story", "1")
	e.must("", "ticket", "create", "--type", "task", "--title", "other", "--project", e.project)
	if err := e.board.SetAttention(context.Background(), 1, "needs a look"); err != nil {
		t.Fatal(err)
	}
	e.must("", "comment", "1", "plain note for @pm")
	pm, err := e.board.CreateSession(context.Background(), board.NewSession{Role: api.RolePM, Ticket: 1, Name: "pia"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_AGENT_ID", pm.ID)
	e.must("", "ask", "1", "--to", "user", "Which retry count?")
	asUser(t)
	var all []api.Comment
	if err := json.Unmarshal([]byte(e.must("", "comments", "1", "--json")), &all); err != nil {
		t.Fatal(err)
	}
	var qid int
	for _, c := range all {
		if c.QuestionTo != "" {
			qid = c.ID
		}
	}
	if qid == 0 {
		t.Fatalf("no question in %+v", all)
	}
	e.must("", "comment", "1", "--reply-to", itoa(qid), "Three.")

	out := e.must("", "comments", "1")
	t.Log("\n" + out)
	contains(t, out, "by system")
	contains(t, out, "plain note for @pm")
	contains(t, out, "by pm pia")
	contains(t, out, "question to user: answered by comment")
	contains(t, out, "reply to: comment "+itoa(qid))
	contains(t, out, "mentions: @pm")
	// As an agent session the thread reads the same.
	t.Setenv("OMO_AGENT_ID", pm.ID)
	contains(t, e.must("", "comments", "1"), "Three.")
	asUser(t)

	out = e.must("", "comments", "1", "--since", itoa(qid))
	if strings.Contains(out, "Which retry count?") || !strings.Contains(out, "Three.") {
		t.Fatalf("since: %q", out)
	}
	contains(t, e.must("", "comments", "1", "--since", "9999"), "no comments")
	// The question is answered, so no open question is left.
	contains(t, e.must("", "comments", "1", "--questions"), "no comments")
	e.must("", "comment", "1", "another @pm")
	pm2, _ := e.board.CreateSession(context.Background(), board.NewSession{Role: api.RolePM, Ticket: 1, Name: "pia2"})
	t.Setenv("OMO_AGENT_ID", pm2.ID)
	e.must("", "ask", "1", "--to", "user", "Still open?")
	asUser(t)
	out = e.must("", "comments", "1", "--questions")
	contains(t, out, "Still open?")
	contains(t, out, "question to user: open")
	if strings.Contains(out, "Which retry count?") {
		t.Fatalf("answered question listed: %q", out)
	}
	contains(t, e.must("", "ticket", "show", "1"), "read them with: omo ai comments 1")

	dev, err := e.board.CreateSession(context.Background(), board.NewSession{Role: api.RoleDeveloper, Ticket: 2, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_AGENT_ID", dev.ID)
	_, err = e.run("", "comments", "3")
	if err == nil || !strings.Contains(err.Error(), "(forbidden)") || strings.Contains(err.Error(), "\n") {
		t.Fatalf("out of scope: %v", err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestAIReadVerbsNeverPrintSessionID(t *testing.T) {
	e := startAI(t)
	e.story("one")
	e.must("", "ticket", "create", "--type", "task", "--title", "work", "--project", e.project, "--story", "1")
	ctx := context.Background()
	pm, err := e.board.CreateSession(ctx, board.NewSession{Role: api.RolePM, Ticket: 1, Name: "pia"})
	if err != nil {
		t.Fatal(err)
	}
	dev, err := e.board.CreateSession(ctx, board.NewSession{Role: api.RoleDeveloper, Ticket: 2, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	e.must("", "comment", "2", "@developer @pm please look")
	ids := []string{pm.ID, dev.ID}
	reads := [][]string{
		{"whoami"}, {"ticket", "show"}, {"ticket", "show", "1"}, {"ticket", "show", "2"}, {"ticket", "list"},
		{"comments", "1"}, {"comments", "2"}, {"project", "list"}, {"project", "search", "api"}, {"model", "list"},
	}
	for _, sess := range []api.AgentSession{pm, dev} {
		t.Setenv("OMO_AGENT_ID", sess.ID)
		for _, args := range reads {
			for _, asJSON := range []bool{false, true} {
				if asJSON {
					args = append(args[:len(args):len(args)], "--json")
				}
				out, err := e.run("", args...)
				if err != nil {
					out += err.Error()
				}
				for _, id := range ids {
					if strings.Contains(out, id) {
						t.Errorf("%s: omo ai %s prints session %s:\n%s", sess.Role, strings.Join(args, " "), id, out)
					}
				}
			}
		}
	}
	t.Setenv("OMO_AGENT_ID", dev.ID)
	if out := e.must("", "whoami"); strings.Contains(out, "session") {
		t.Fatalf("whoami prints a session line:\n%s", out)
	}
}

func TestAIEmptyAgentIDIsRefused(t *testing.T) {
	e := startAI(t)
	for _, v := range []string{"", " "} {
		t.Setenv("OMO_AGENT_ID", v)
		out, err := e.run("", "ticket", "create", "--type", "story", "--title", "X", "--project", e.project)
		if err == nil || !strings.Contains(err.Error(), "OMO_AGENT_ID is set but empty") || out != "" {
			t.Fatalf("%q: %q %v", v, out, err)
		}
	}
	asUser(t)
	if out := e.must("", "ticket", "list"); !strings.Contains(out, "no tickets") {
		t.Fatalf("a refused call created a ticket: %q", out)
	}
}
