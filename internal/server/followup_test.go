package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// followUpExport records an actual board export without starting an engine or
// contacting a remote. The HTTP requests below use the real board transitions.
func followUpExport(t *testing.T, s *Server, project string) api.Ticket {
	t.Helper()
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "Export follow-up", Projects: []string{project}, Review: api.ReviewHuman, WhenApproved: api.WhenApprovedExport})
	if err != nil {
		t.Fatal(err)
	}
	n := int64(tk.Number)
	if err := s.board.SetBranch(ctx, n, project, "feature", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tickets SET col='review',review_stage='exporting',review_round=1 WHERE id=?`, n); err != nil {
		t.Fatal(err)
	}
	base, commit := strings.Repeat("a", 40), strings.Repeat("b", 40)
	if err := s.board.RecordCommits(ctx, n, project, base, commit); err != nil {
		t.Fatal(err)
	}
	if err := s.board.RecordExportWatch(ctx, n, project, "https://example.test/org/repo", "feature", "main", commit, base); err != nil {
		t.Fatal(err)
	}
	sess, err := s.board.CreateSession(ctx, board.NewSession{Role: api.RoleDeveloper, Ticket: n, Project: project, Name: "export", Purpose: api.PurposeExport})
	if err != nil {
		t.Fatal(err)
	}
	actor, err := s.board.ResolveSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Done(ctx, actor, api.DoneRequest{URL: "https://example.test/org/repo/pull/1"}); err != nil {
		t.Fatal(err)
	}
	tk, err = s.board.Get(ctx, board.User, n)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestFollowUpRouteTransitionsAndPreservesExport(t *testing.T) {
	for _, role := range []string{"developer", "reviewer"} {
		for _, stage := range []api.ReviewStage{api.ReviewStageAwaitingMerge, api.ReviewStageHuman} {
			t.Run(role+"/"+string(stage), func(t *testing.T) {
				s := newTestServer(t)
				project := addProject(t, s)
				before := followUpExport(t, s, project)
				if _, err := s.db.Exec(`UPDATE tickets SET review_stage=? WHERE id=?`, stage, before.Number); err != nil {
					t.Fatal(err)
				}
				path := tp(before.Number) + "/follow-up"
				rec := socketDo(t, s, http.MethodPost, path, api.FollowUpRequest{Role: role, Comment: "Rebase onto main.\nKeep the reviewed resolution."}, "")
				if rec.Code != http.StatusOK {
					t.Fatalf("status %d: %s", rec.Code, rec.Body)
				}
				got := decodeBody[api.Ticket](t, rec.Body)
				wantCol, wantStage, wantRound := api.ColumnInProgress, api.ReviewStage(""), before.ReviewRound
				if role == "reviewer" {
					wantCol, wantStage, wantRound = api.ColumnReview, api.ReviewStageAgent, before.ReviewRound+1
				}
				if got.Column != wantCol || got.ReviewStage != wantStage || got.ReviewRound != wantRound {
					t.Fatalf("follow-up returned %s/%s round %d, want %s/%s round %d", got.Column, got.ReviewStage, got.ReviewRound, wantCol, wantStage, wantRound)
				}
				p, old := got.Projects[0], before.Projects[0]
				if p.LastExportCommit != strings.Repeat("b", 40) || p.LastExportURL != "https://example.test/org/repo/pull/1" || p.Branch != old.Branch || p.BaseBranch != old.BaseBranch {
					t.Fatalf("last export identity or working branch changed: before %+v, after %+v", old, p)
				}
				if p.ExportURL != "" || p.ExportCommit != "" || p.ExportRemote != "" || p.ExportBranch != "" || p.ExportBase != "" || p.ExportTargetCommit != "" || got.ExportMerged {
					t.Fatalf("stale export watch remains: %+v", p)
				}
				stored, err := s.board.Get(t.Context(), board.User, int64(got.Number))
				if err != nil || !reflect.DeepEqual(stored, got) {
					t.Fatalf("response differs from persisted transition: %+v, %v", stored, err)
				}
				comments, err := s.board.Comments(t.Context(), board.User, int64(got.Number))
				if err != nil {
					t.Fatal(err)
				}
				last := comments[len(comments)-1]
				if last.Body != "Rebase onto main.\nKeep the reviewed resolution." || last.Author.Role != api.RoleUser || len(last.Mentions) != 1 || last.Mentions[0].Role != api.Role(role) {
					t.Fatalf("follow-up task was not addressed to %s: %+v", role, last)
				}
			})
		}
	}
}

func TestFollowUpRouteRejectsAgentsAndUnknownSessions(t *testing.T) {
	s := newTestServer(t)
	before := followUpExport(t, s, addProject(t, s))
	path := tp(before.Number) + "/follow-up"
	body := api.FollowUpRequest{Role: "developer", Comment: "Rebase onto main"}
	for _, role := range agentRoles {
		session := liveSession(t, s, role)
		rec := socketDo(t, s, http.MethodPost, path, body, session.ID)
		if rec.Code != http.StatusForbidden || errorCode(t, rec.Body) != api.CodeForbidden {
			t.Fatalf("%s: status %d, body %s", role, rec.Code, rec.Body)
		}
	}
	if rec := socketDo(t, s, http.MethodPost, path, body, "missing-session"); rec.Code != http.StatusUnauthorized || errorCode(t, rec.Body) != api.CodeUnknownSession {
		t.Fatalf("unknown session: status %d, body %s", rec.Code, rec.Body)
	}
	after, err := s.board.Get(t.Context(), board.User, int64(before.Number))
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("refused session changed ticket: %+v, %v", after, err)
	}
}

func TestFollowUpRouteRejectsInvalidRequests(t *testing.T) {
	for _, tc := range []struct {
		name, update string
		body         any
		status       int
		code         string
	}{
		{name: "missing comment", body: map[string]any{"role": "developer"}, status: http.StatusUnprocessableEntity, code: api.CodeValidation},
		{name: "blank comment", body: map[string]any{"role": "developer", "comment": " \n "}, status: http.StatusUnprocessableEntity, code: api.CodeValidation},
		{name: "invalid recipient", body: map[string]any{"role": "ceo", "comment": "Rebase"}, status: http.StatusUnprocessableEntity, code: api.CodeValidation},
		{name: "unknown field", body: map[string]any{"role": "developer", "comment": "Rebase", "force": true}, status: http.StatusBadRequest, code: api.CodeBadRequest},
		{name: "wrong comment type", body: map[string]any{"role": "developer", "comment": []string{"Rebase"}}, status: http.StatusBadRequest, code: api.CodeBadRequest},
		{name: "no previous export", update: `UPDATE ticket_projects SET last_export_commit='',last_export_url='' WHERE ticket_id=?`, body: api.FollowUpRequest{Role: "developer", Comment: "Rebase"}, status: http.StatusConflict, code: api.CodeInvalidTransition},
		{name: "busy", update: `UPDATE tickets SET busy=1 WHERE id=?`, body: api.FollowUpRequest{Role: "reviewer", Comment: "Review"}, status: http.StatusConflict, code: api.CodeConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestServer(t)
			ticket := followUpExport(t, s, addProject(t, s))
			if tc.update != "" {
				if _, err := s.db.Exec(tc.update, ticket.Number); err != nil {
					t.Fatal(err)
				}
			}
			before, err := s.board.Get(t.Context(), board.User, int64(ticket.Number))
			if err != nil {
				t.Fatal(err)
			}
			start := lastEventID(t, s)
			rec := socketDo(t, s, http.MethodPost, tp(ticket.Number)+"/follow-up", tc.body, "")
			if rec.Code != tc.status || errorCode(t, rec.Body) != tc.code {
				t.Fatalf("status %d, body %s; want %d %s", rec.Code, rec.Body, tc.status, tc.code)
			}
			after, err := s.board.Get(t.Context(), board.User, int64(ticket.Number))
			if err != nil || !reflect.DeepEqual(after, before) || lastEventID(t, s) != start {
				t.Fatalf("refused request changed ticket or emitted an event: %+v, %v", after, err)
			}
		})
	}
}

func TestFollowUpRouteRejectsMalformedJSON(t *testing.T) {
	s := newTestServer(t)
	before := followUpExport(t, s, addProject(t, s))
	req := httptest.NewRequest(http.MethodPost, "http://omo/api/tickets/"+strconv.Itoa(before.Number)+"/follow-up", strings.NewReader(`{"role":"developer","comment":`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.SocketHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec.Body) != api.CodeBadRequest {
		t.Fatalf("malformed JSON: status %d, body %s", rec.Code, rec.Body)
	}
	after, err := s.board.Get(t.Context(), board.User, int64(before.Number))
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("malformed request changed ticket: %+v, %v", after, err)
	}
}
