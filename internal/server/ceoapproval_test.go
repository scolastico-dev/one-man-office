package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// TestCEOTicketWaitsForTheUserOverTheRoutes: with a fresh home's settings
// a task the CEO creates cannot be moved to ready by the CEO, the refusal
// says the user approves it, and the user's approval moves it.
func TestCEOTicketWaitsForTheUserOverTheRoutes(t *testing.T) {
	s := newTestServer(t)
	project := addProject(t, s)
	ceo := s.agent(t, liveSession(t, s, api.RoleCEO).ID)
	user := userCaller(t, s)

	tk := okJSON[api.Ticket](t, ceo, http.StatusCreated, "POST", "/api/tickets",
		api.CreateTicket{Type: api.TypeTask, Title: "task", Projects: []string{project}})
	status, body := ceo.do("POST", tp(tk.Number)+"/move", api.MoveRequest{Column: api.ColumnReady})
	if status != http.StatusForbidden || !strings.Contains(string(body), "the user approves the tickets the CEO creates") {
		t.Fatalf("CEO move: %d %s", status, body)
	}
	ceo.fails(http.StatusForbidden, api.CodeForbidden, "POST", tp(tk.Number)+"/approve", api.ApproveRequest{})

	got := okJSON[api.Ticket](t, user, http.StatusOK, "POST", tp(tk.Number)+"/approve", api.ApproveRequest{})
	if got.Column != api.ColumnReady {
		t.Fatalf("after the user's approval: %s", got.Column)
	}

	// Turned off, the CEO releases its own task as before.
	okJSON[api.General](t, user, http.StatusOK, "PATCH", "/api/settings", map[string]any{"ceo": map[string]any{"tickets_need_user_approval": false}})
	own := okJSON[api.Ticket](t, ceo, http.StatusCreated, "POST", "/api/tickets",
		api.CreateTicket{Type: api.TypeTask, Title: "second", Projects: []string{project}})
	if got := okJSON[api.Ticket](t, ceo, http.StatusOK, "POST", tp(own.Number)+"/move", api.MoveRequest{Column: api.ColumnReady}); got.Column != api.ColumnReady {
		t.Fatalf("CEO release with the setting off: %s", got.Column)
	}
}
