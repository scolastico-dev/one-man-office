package server

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// TestRemoveProjectRouteAccess: POST /api/tickets/{n}/remove-project lets
// agents through to the board, which allows the user and the CEO (on
// tickets it created or supervises) and refuses every other role.
func TestRemoveProjectRouteAccess(t *testing.T) {
	s, first := engineServer(t, &testRunner{}, fakeProfile(), false)
	ctx := context.Background()
	second, err := s.settings.AddProject(committedRepo(t, "main"), "second")
	if err != nil {
		t.Fatal(err)
	}
	s.waitSettingsEvents(ctx)
	projects := []string{first, second.ID}
	path := func(n int) string { return "/api/tickets/" + strconv.Itoa(n) + "/remove-project" }
	req := api.RemoveProjectRequest{Project: second.ID}

	ceo, err := s.board.CreateSession(ctx, board.NewSession{Role: api.RoleCEO, Name: "ceo-ada"})
	if err != nil {
		t.Fatal(err)
	}
	ceoActor, err := s.board.ResolveSession(ctx, ceo.ID)
	if err != nil {
		t.Fatal(err)
	}
	story := func(a board.Actor) int {
		tk, err := s.board.Create(ctx, a, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: projects})
		if err != nil {
			t.Fatal(err)
		}
		return tk.Number
	}
	session := func(role api.Role, n int, project string) string {
		sess, err := s.board.CreateSession(ctx, board.NewSession{Role: role, Ticket: int64(n), Project: project, Name: string(role) + "-x"})
		if err != nil {
			t.Fatal(err)
		}
		return sess.ID
	}

	// Every agent but the CEO is refused by the board, also on its own
	// ticket.
	byUser := story(board.User)
	for role, sess := range map[api.Role]string{
		api.RolePM:       session(api.RolePM, byUser, ""),
		api.RoleReviewer: session(api.RoleReviewer, byUser, second.ID),
	} {
		rec := socketDo(t, s, http.MethodPost, path(byUser), req, sess)
		if rec.Code != http.StatusForbidden || errorCode(t, rec.Body) != api.CodeForbidden {
			t.Errorf("%s: status %d, want 403", role, rec.Code)
		}
	}
	task, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{first}})
	if err != nil {
		t.Fatal(err)
	}
	if rec := socketDo(t, s, http.MethodPost, path(task.Number), api.RemoveProjectRequest{Project: first}, session(api.RoleDeveloper, task.Number, first)); rec.Code != http.StatusForbidden {
		t.Errorf("developer: status %d, want 403", rec.Code)
	}
	// The CEO: not on the user's ticket, but on its own.
	if rec := socketDo(t, s, http.MethodPost, path(byUser), req, ceo.ID); rec.Code != http.StatusForbidden {
		t.Errorf("CEO on the user's ticket: status %d, want 403", rec.Code)
	}
	byCEO := story(ceoActor)
	rec := socketDo(t, s, http.MethodPost, path(byCEO), req, ceo.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("CEO on its ticket: status %d: %s", rec.Code, rec.Body)
	}
	if got := decodeBody[api.Ticket](t, rec.Body); len(got.Projects) != 1 || got.Projects[0].Project != first {
		t.Fatalf("projects %+v", got.Projects)
	}
	// The user, on the socket without a session and in the browser.
	if rec := socketDo(t, s, http.MethodPost, path(story(board.User)), req, ""); rec.Code != http.StatusOK {
		t.Fatalf("user: status %d: %s", rec.Code, rec.Body)
	}
	other := story(board.User)
	b := newBrowser(t, s)
	if resp := asUser(b, s, http.MethodPost, path(other), req); resp.StatusCode != http.StatusOK {
		t.Fatalf("user in the browser: status %d", resp.StatusCode)
	}
	// Unknown fields are refused like on every route.
	if rec := socketDo(t, s, http.MethodPost, path(other), map[string]any{"project": first, "force": true}, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: status %d", rec.Code)
	}
}
