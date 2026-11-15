package server

import (
	"bytes"
	"context"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// TestSocketNeverShowsSessionIDsToAgents reads every route that returns
// board data as each agent role and fails if any live session id appears in
// a body. The user, on the same routes, sees them.
func TestSocketNeverShowsSessionIDsToAgents(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	p := addProject(t, s)
	story, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{p}})
	if err != nil {
		t.Fatal(err)
	}
	sn := int64(story.Number)
	start := func(role api.Role, ticket int64) api.AgentSession {
		t.Helper()
		ns := board.NewSession{Role: role, Ticket: ticket, Project: p, Name: string(role)}
		if role == api.RoleCEO {
			ns.Project = ""
		}
		sess, err := s.board.CreateSession(ctx, ns)
		if err != nil {
			t.Fatal(err)
		}
		return sess
	}
	pm := start(api.RolePM, sn)
	pmActor, err := s.board.ResolveSession(ctx, pm.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.board.Create(ctx, pmActor, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{p},
		Story: &story.Number, Description: "work"})
	if err != nil {
		t.Fatal(err)
	}
	tn := int64(task.Number)
	sessions := map[string]api.AgentSession{
		"pm":        pm,
		"developer": start(api.RoleDeveloper, tn),
		"reviewer":  start(api.RoleReviewer, tn),
		"ceo":       start(api.RoleCEO, 0),
	}
	if _, err := s.board.Comment(ctx, board.User, tn, api.CommentRequest{Body: "@developer @reviewer @pm look"}); err != nil {
		t.Fatal(err)
	}

	paths := []string{"/api/me", "/api/agents", "/api/agents?live=true"}
	for _, n := range []int64{sn, tn} {
		base := tp(int(n))
		paths = append(paths, base, base+"/comments", base+"/agents", base+"/events", base+"/descriptions")
	}
	var userBodies []byte
	for _, path := range paths {
		userBodies = append(userBodies, socketDo(t, s, "GET", path, nil, "").Body.Bytes()...)
	}
	for _, sess := range sessions {
		if !bytes.Contains(userBodies, []byte(sess.ID)) {
			t.Errorf("the user does not see session %s (%s)", sess.ID, sess.Role)
		}
	}
	for name, sess := range sessions {
		for _, path := range paths {
			rec := socketDo(t, s, "GET", path, nil, sess.ID)
			if name == "developer" && rec.Code != 200 && path != "/api/agents" && path != "/api/agents?live=true" &&
				path != tp(int(sn))+"/comments" {
				t.Errorf("developer: GET %s: status %d: %s", path, rec.Code, rec.Body.Bytes())
			}
			for _, other := range sessions {
				if bytes.Contains(rec.Body.Bytes(), []byte(other.ID)) {
					t.Errorf("%s: GET %s (status %d) shows session %s: %s", name, path, rec.Code, other.ID, rec.Body.Bytes())
				}
			}
		}
	}
}
