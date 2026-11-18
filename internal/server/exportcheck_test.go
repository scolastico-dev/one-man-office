package server

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

func TestCheckExportRoute(t *testing.T) {
	s, _ := engineServer(t, &testRunner{}, fakeProfile(), false)
	ctx := context.Background()
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: "Export", Projects: []string{"app"}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/tickets/" + strconv.Itoa(tk.Number) + "/check-export"
	rec := socketDo(t, s, http.MethodPost, path, struct{}{}, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("not waiting status %d: %s", rec.Code, rec.Body)
	}
	// Use the board-only export helper: no real repository or fetch is needed
	// to verify access, stage checks and accepting a coalesced check request.
	if _, err := s.db.Exec(`UPDATE tickets SET col = 'review',review_stage = 'exporting' WHERE id = ?`, tk.Number); err != nil {
		t.Fatal(err)
	}
	if err := s.board.CompleteExport(ctx, int64(tk.Number), "https://example.test/pr/1"); err != nil {
		t.Fatal(err)
	}
	ceo, err := s.board.CreateSession(ctx, board.NewSession{Role: api.RoleCEO, Name: "ceo-ada"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := socketDo(t, s, http.MethodPost, path, struct{}{}, ceo.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("agent status %d: %s", rec.Code, rec.Body)
	}
	rec = socketDo(t, s, http.MethodPost, path, struct{}{}, "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("check status %d: %s", rec.Code, rec.Body)
	}
	var result struct {
		Checking bool `json:"checking"`
	}
	result = decodeBody[struct {
		Checking bool `json:"checking"`
	}](t, rec.Body)
	if !result.Checking {
		t.Fatal("request not acknowledged")
	}
}
