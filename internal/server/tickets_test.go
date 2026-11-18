package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// caller sends requests as the user over TCP (session "") or as an agent over
// the socket.
type caller struct {
	t       *testing.T
	s       *Server
	b       *browser
	session string
}

func userCaller(t *testing.T, s *Server) *caller {
	return &caller{t: t, s: s, b: newBrowser(t, s)}
}

func (s *Server) agent(t *testing.T, session string) *caller {
	return &caller{t: t, s: s, session: session}
}

func (c *caller) do(method, path string, body any) (int, []byte) {
	c.t.Helper()
	if c.session != "" {
		rec := socketDo(c.t, c.s, method, path, body, c.session)
		return rec.Code, rec.Body.Bytes()
	}
	resp := c.b.do(method, path, body, bearer(c.s))
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp.StatusCode, data
}

// ok expects status want and decodes the body into T.
func okJSON[T any](t *testing.T, c *caller, want int, method, path string, body any) T {
	t.Helper()
	status, data := c.do(method, path, body)
	if status != want {
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, status, want, data)
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s %s: %v: %s", method, path, err, data)
	}
	return v
}

// fails expects an error response with status and code.
func (c *caller) fails(want int, code, method, path string, body any) {
	c.t.Helper()
	status, data := c.do(method, path, body)
	var env api.ErrorEnvelope
	_ = json.Unmarshal(data, &env)
	if status != want || env.Error == nil || env.Error.Code != code {
		c.t.Fatalf("%s %s: status %d, body %s; want %d %s", method, path, status, data, want, code)
	}
}

func addProject(t *testing.T, s *Server) string {
	t.Helper()
	p, err := s.settings.AddProject(newRepo(t), "")
	if err != nil {
		t.Fatal(err)
	}
	// Adding a project queues a settings event whose worker runs git in the
	// project's folder. A test goes on only once that is over: its counts of
	// events start from a settled state, and it can remove the folder (which
	// Windows refuses while a program runs in it).
	s.waitSettingsEvents(context.Background())
	return p.ID
}

func newStory(t *testing.T, c *caller, project string) api.Ticket {
	t.Helper()
	return okJSON[api.Ticket](t, c, 201, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeStory, Title: "story", Projects: []string{project}})
}

func newTask(t *testing.T, c *caller, project, title string) api.Ticket {
	t.Helper()
	return okJSON[api.Ticket](t, c, 201, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: title, Projects: []string{project}})
}

func tp(n int) string { return fmt.Sprintf("/api/tickets/%d", n) }

func TestCreateGetListTicketOverHTTP(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	p := addProject(t, s)
	story := newStory(t, u, p)
	if story.Number != 1 || story.Description == "" {
		t.Fatalf("story %+v", story)
	}
	got := okJSON[api.Ticket](t, u, 200, "GET", tp(1), nil)
	if got.Description != board.StoryTemplate || got.Column != api.ColumnPlanning {
		t.Fatalf("got %+v", got)
	}
	list := okJSON[api.TicketList](t, u, 200, "GET", "/api/tickets", nil)
	if list.EventID <= 0 || len(list.Tickets) != 1 {
		t.Fatalf("list %+v", list)
	}
	newTask(t, u, p, "t")
	list = okJSON[api.TicketList](t, u, 200, "GET", "/api/tickets?type=task&needs_me=false&done_limit=5&q=t", nil)
	if len(list.Tickets) != 1 || list.Tickets[0].Number != 2 {
		t.Fatalf("filtered %+v", list)
	}
	// An empty board answers [] and a list never null.
	_, data := u.do("GET", "/api/tickets?column=done", nil)
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	if string(raw["tickets"]) != "[]" {
		t.Fatalf("tickets %s", raw["tickets"])
	}
	u.fails(400, api.CodeBadRequest, "GET", "/api/tickets?story=x", nil)
	u.fails(400, api.CodeBadRequest, "GET", "/api/tickets?needs_me=maybe", nil)
	u.fails(400, api.CodeBadRequest, "GET", "/api/tickets/abc", nil)
	u.fails(422, api.CodeValidation, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "", Projects: []string{p}})
}

func TestPatchNullClearsOverride(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	task := newTask(t, u, addProject(t, s), "t")
	set := okJSON[api.Ticket](t, u, 200, "PATCH", tp(task.Number), json.RawMessage(`{"review":"human"}`))
	if set.Review == nil || *set.Review != api.ReviewHuman || set.EffectiveReview != api.ReviewHuman {
		t.Fatalf("set %+v", set)
	}
	clear := okJSON[api.Ticket](t, u, 200, "PATCH", tp(task.Number), json.RawMessage(`{"review":null}`))
	if clear.Review != nil || clear.EffectiveReview != settings.Defaults().ReviewDefaults.Task {
		t.Fatalf("clear review=%v effective=%s", clear.Review, clear.EffectiveReview)
	}
	u.fails(422, api.CodeValidation, "PATCH", tp(task.Number), json.RawMessage(`{"review":"bogus"}`))
}

func TestMoveMapsDragToActions(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	task := newTask(t, u, addProject(t, s), "t")
	moved := okJSON[api.Ticket](t, u, 200, "POST", tp(task.Number)+"/move", api.MoveRequest{Column: api.ColumnReady})
	if moved.Column != api.ColumnReady {
		t.Fatalf("moved %+v", moved)
	}
	status, data := u.do("POST", tp(task.Number)+"/move", api.MoveRequest{Column: api.ColumnDone})
	var env api.ErrorEnvelope
	_ = json.Unmarshal(data, &env)
	if status != 409 || env.Error == nil || env.Error.Code != api.CodeInvalidTransition || env.Error.Message == "" {
		t.Fatalf("status %d: %s", status, data)
	}
	// Withdrawing goes back to planning.
	back := okJSON[api.Ticket](t, u, 200, "POST", tp(task.Number)+"/move", api.MoveRequest{Column: api.ColumnPlanning})
	if back.Column != api.ColumnPlanning {
		t.Fatalf("back %+v", back)
	}
}

func TestApproveCancelRejectMarkMerged(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	p := addProject(t, s)
	a := newTask(t, u, p, "a")
	appr := okJSON[api.Ticket](t, u, 200, "POST", tp(a.Number)+"/approve", api.ApproveRequest{})
	if appr.Column != api.ColumnReady {
		t.Fatalf("approve %+v", appr)
	}
	canc := okJSON[api.Ticket](t, u, 200, "POST", tp(a.Number)+"/cancel", api.CancelRequest{DeleteBranch: true})
	if canc.Column != api.ColumnDone || canc.Resolution != api.ResolutionCancelled {
		t.Fatalf("cancel %+v", canc)
	}
	b := newTask(t, u, p, "b")
	u.fails(409, api.CodeInvalidTransition, "POST", tp(b.Number)+"/reject", api.RejectRequest{Comment: "@developer no"})
	u.fails(409, api.CodeInvalidTransition, "POST", tp(b.Number)+"/mark-merged", nil)
	u.fails(404, api.CodeNotFound, "POST", tp(99)+"/cancel", api.CancelRequest{})
}

func TestDescriptionRoutes(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	task := newTask(t, u, addProject(t, s), "t")
	okJSON[api.DescriptionVersion](t, u, 200, "PUT", tp(task.Number)+"/description", api.DescriptionUpdate{Text: "first", BaseVersion: task.DescriptionVersion})
	v := okJSON[api.DescriptionVersion](t, u, 200, "PUT", tp(task.Number)+"/description", api.DescriptionUpdate{Text: "second", BaseVersion: task.DescriptionVersion + 1})
	if v.Text != "second" || !v.Current {
		t.Fatalf("version %+v", v)
	}
	versions := okJSON[[]api.DescriptionVersion](t, u, 200, "GET", tp(task.Number)+"/descriptions", nil)
	if len(versions) < 2 || versions[0].Number != v.Number {
		t.Fatalf("versions %+v", versions)
	}
	oldest := versions[len(versions)-1]
	if oldest.Text != "first" {
		t.Fatalf("oldest %+v", oldest)
	}
	restored := okJSON[api.DescriptionVersion](t, u, 200, "POST", fmt.Sprintf("%s/descriptions/%d/restore", tp(task.Number), oldest.Number), nil)
	if restored.RestoredFrom == nil || *restored.RestoredFrom != oldest.Number {
		t.Fatalf("restored %+v", restored)
	}
	// An edit based on a stale version conflicts.
	u.fails(409, api.CodeStaleDescription, "PUT", tp(task.Number)+"/description", api.DescriptionUpdate{Text: "third", BaseVersion: v.Number})
}

func TestDependencyRoutes(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	p := addProject(t, s)
	a, b := newTask(t, u, p, "a"), newTask(t, u, p, "b")
	linked := okJSON[api.Ticket](t, u, 201, "POST", tp(b.Number)+"/dependencies", api.DependencyRequest{DependsOn: a.Number})
	if len(linked.DependsOn) != 1 || linked.DependsOn[0] != a.Number {
		t.Fatalf("linked %+v", linked)
	}
	u.fails(422, api.CodeDependencyCycle, "POST", tp(a.Number)+"/dependencies", api.DependencyRequest{DependsOn: b.Number})
	cleared := okJSON[api.Ticket](t, u, 200, "DELETE", fmt.Sprintf("%s/dependencies/%d", tp(b.Number), a.Number), nil)
	if len(cleared.DependsOn) != 0 {
		t.Fatalf("cleared %+v", cleared)
	}
	_, data := u.do("GET", tp(b.Number), nil)
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	if string(raw["depends_on"]) != "[]" {
		t.Fatalf("depends_on %s", raw["depends_on"])
	}
}

func TestCommentRouteReturnsUnreachable(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	task := newTask(t, u, addProject(t, s), "t")
	res := okJSON[api.CommentResult](t, u, 201, "POST", tp(task.Number)+"/comments", api.CommentRequest{Body: "@developer please look"})
	if len(res.Unreachable) != 1 || res.Unreachable[0] != api.RoleDeveloper || res.Comment.Author.Role != api.RoleUser {
		t.Fatalf("result %+v", res)
	}
	list := okJSON[[]api.Comment](t, u, 200, "GET", tp(task.Number)+"/comments", nil)
	if len(list) == 0 || list[len(list)-1].ID != res.Comment.ID {
		t.Fatalf("comments %+v", list)
	}
	u.fails(422, api.CodeValidation, "POST", tp(task.Number)+"/comments", api.CommentRequest{Body: ""})
}

func TestTicketEventsAndAgentsRoutes(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	dev := liveSession(t, s, api.RoleDeveloper)
	n := *dev.Ticket
	events := okJSON[[]api.Event](t, u, 200, "GET", tp(n)+"/events", nil)
	if len(events) == 0 || events[0].Kind != "ticket.created" {
		t.Fatalf("events %+v", events)
	}
	agents := okJSON[[]api.AgentSession](t, u, 200, "GET", tp(n)+"/agents", nil)
	if len(agents) != 1 || agents[0].ID != dev.ID {
		t.Fatalf("agents %+v", agents)
	}
	if err := s.board.EndSession(t.Context(), dev.ID, "done"); err != nil {
		t.Fatal(err)
	}
	if got := okJSON[[]api.AgentSession](t, u, 200, "GET", "/api/agents?live=true", nil); len(got) != 0 {
		t.Fatalf("live %+v", got)
	}
	if got := okJSON[[]api.AgentSession](t, u, 200, "GET", "/api/agents", nil); len(got) != 1 {
		t.Fatalf("all %+v", got)
	}
	u.fails(400, api.CodeBadRequest, "GET", "/api/agents?live=x", nil)
	u.fails(404, api.CodeNotFound, "GET", tp(999)+"/agents", nil)
}

func TestDeveloperCannotListEveryAgent(t *testing.T) {
	s := newTestServer(t)
	dev := liveSession(t, s, api.RoleDeveloper)
	pm := liveSession(t, s, api.RolePM)
	a := s.agent(t, dev.ID)
	a.fails(403, api.CodeForbidden, "GET", "/api/agents", nil)
	a.fails(403, api.CodeForbidden, "GET", "/api/agents?live=true", nil)
	// Its own ticket's sessions stay readable.
	if got := okJSON[[]api.AgentSession](t, a, 200, "GET", tp(*dev.Ticket)+"/agents", nil); len(got) != 1 || got[0].ID != "" || got[0].Role != api.RoleDeveloper {
		t.Fatalf("own agents %+v", got)
	}
	if got := okJSON[[]api.AgentSession](t, s.agent(t, pm.ID), 200, "GET", "/api/agents", nil); len(got) != 2 {
		t.Fatalf("pm lists %+v", got)
	}
}

func TestAgentDoneOverSocket(t *testing.T) {
	s := newTestServer(t)
	dev := liveSession(t, s, api.RoleDeveloper)
	n := *dev.Ticket
	if _, err := s.board.Move(t.Context(), board.User, int64(n), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		t.Fatal(err)
	}
	if err := s.board.BeginWork(t.Context(), int64(n)); err != nil {
		t.Fatal(err)
	}
	a := s.agent(t, dev.ID)
	done := okJSON[api.Ticket](t, a, 200, "POST", "/api/ai/done", api.DoneRequest{Comment: "finished"})
	if done.Column != api.ColumnReview {
		t.Fatalf("done %+v", done)
	}
	// The user has no use for done.
	userCaller(t, s).fails(403, api.CodeForbidden, "POST", "/api/ai/done", api.DoneRequest{})
}

func TestAgentRefusedWhatUserMay(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	dev := liveSession(t, s, api.RoleDeveloper)
	n := *dev.Ticket
	a := s.agent(t, dev.ID)
	// Agents cannot change the supervisor, nor reject, nor mark merged.
	a.fails(403, api.CodeForbidden, "PATCH", tp(n), json.RawMessage(`{"supervisor":"user"}`))
	a.fails(403, api.CodeForbidden, "POST", tp(n)+"/reject", api.RejectRequest{Comment: "@developer no"})
	a.fails(403, api.CodeForbidden, "POST", tp(n)+"/mark-merged", nil)
	okJSON[api.Ticket](t, u, 200, "PATCH", tp(n), json.RawMessage(`{"supervisor":"user"}`))
	// Agents may read what is in scope.
	okJSON[api.Ticket](t, a, 200, "GET", tp(n), nil)
}

func TestDeveloperCannotSeeTicketOutsideScope(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	dev := liveSession(t, s, api.RoleDeveloper)
	other := newTask(t, u, addProject(t, s), "other")
	a := s.agent(t, dev.ID)
	a.fails(403, api.CodeForbidden, "GET", tp(other.Number), nil)
	a.fails(403, api.CodeForbidden, "GET", tp(other.Number)+"/comments", nil)
	a.fails(403, api.CodeForbidden, "GET", tp(other.Number)+"/events", nil)
	a.fails(403, api.CodeForbidden, "GET", tp(other.Number)+"/agents", nil)
	a.fails(403, api.CodeForbidden, "GET", tp(other.Number)+"/descriptions", nil)
	list := okJSON[api.TicketList](t, a, 200, "GET", "/api/tickets", nil)
	for _, tk := range list.Tickets {
		if tk.Number == other.Number {
			t.Fatalf("list leaks #%d", other.Number)
		}
	}
}

func TestUnknownFieldIs400(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	task := newTask(t, u, addProject(t, s), "t")
	u.fails(400, api.CodeBadRequest, "PATCH", tp(task.Number), json.RawMessage(`{"titel":"x"}`))
	u.fails(400, api.CodeBadRequest, "POST", "/api/tickets", json.RawMessage(`{"type":"task","title":"x","bogus":1}`))
}

func TestTicketNotFound(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	u.fails(404, api.CodeNotFound, "GET", "/api/tickets/999", nil)
	u.fails(404, api.CodeNotFound, "PATCH", "/api/tickets/999", json.RawMessage(`{"title":"x"}`))
}

func TestTicketRoutesNeedJSONContentType(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	task := newTask(t, u, addProject(t, s), "t")
	hdr := bearer(s)
	hdr["Content-Type"] = "text/plain"
	resp := u.b.do("POST", tp(task.Number)+"/mark-merged", nil, hdr)
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
}
