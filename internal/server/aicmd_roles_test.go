package server

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// TestRoleFilterMatchesRoutes calls every omo tool an agent session does
// not see, as that session, through the in-process client: the route must
// refuse it too. So the coarse filter of the tools never hides a tool its
// route would allow; what a session may do is decided by the routes.
func TestRoleFilterMatchesRoutes(t *testing.T) {
	s, project := engineServer(t, &testRunner{}, fakeProfile(), false)
	ctx := context.Background()
	sessions := map[aicmd.Audience]string{}
	for _, role := range []api.Role{api.RoleCEO, api.RolePM, api.RoleDeveloper, api.RoleReviewer} {
		sessions[aicmd.Audience{Role: role, Purpose: api.PurposeWork}] = liveSession(t, s, role).ID
	}
	sessions[aicmd.Audience{Role: api.RoleDeveloper, Purpose: api.PurposeExport}] = exportSession(t, s, project)

	checked := 0
	for _, c := range aicmd.Commands() {
		if c.CommandLineOnly {
			continue
		}
		for a, session := range sessions {
			if c.Sees(a) {
				continue
			}
			// An empty input: the refusal comes before the input matters.
			in := reflect.New(c.Input)
			client := apiclient.NewWithTransport(apiclient.HandlerTransport(s.SocketHandler()), session)
			_, err := c.Run(ctx, client, in.Interface())
			var apiErr *api.Error
			if !errors.As(err, &apiErr) || apiErr.Code != api.CodeForbidden {
				t.Errorf("%s as %s/%s is hidden, but its route answers %v", c.Tool, a.Role, a.Purpose, err)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no hidden tool was called")
	}
}

// exportSession starts an export job for project on a task that waits for
// its export.
func exportSession(t *testing.T, s *Server, project string) string {
	t.Helper()
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "export", Projects: []string{project}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tickets SET col = ?, review_stage = ?, review_round = 1 WHERE id = ?`,
		api.ColumnReview, api.ReviewStageExporting, tk.Number); err != nil {
		t.Fatal(err)
	}
	sess, err := s.board.CreateSession(ctx, board.NewSession{Role: api.RoleDeveloper, Ticket: int64(tk.Number), Project: project,
		Name: "export", Purpose: api.PurposeExport})
	if err != nil {
		t.Fatal(err)
	}
	return sess.ID
}
