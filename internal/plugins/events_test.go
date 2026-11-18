package plugins

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestPayloadsCutLongText(t *testing.T) {
	body := strings.Repeat("é", 5*1024) // 10 KiB of two-byte characters
	c := CommentPayload(api.Comment{ID: 1, Body: body + "x"})
	got := c["body"].(string)
	if len(got) > MaxBodyBytes || len(got) < MaxBodyBytes-1 || !utf8.ValidString(got) || !strings.HasPrefix(body, got) {
		t.Fatalf("body cut to %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	tk := TicketPayload(api.TicketSummary{Title: strings.Repeat("t", 10<<10), Attention: strings.Repeat("a", 10<<10) + "\xff"}, "")
	if len(tk["title"].(string)) != MaxTextBytes || len(tk["attention"].(string)) != MaxTextBytes {
		t.Fatalf("title %d, attention %d bytes", len(tk["title"].(string)), len(tk["attention"].(string)))
	}
	if cutText("a\xffb", 10) != "a\uFFFDb" {
		t.Fatal("invalid UTF-8 kept")
	}
}

func TestPayloadsHoldNoSessionIDOrPath(t *testing.T) {
	const session = "sess-0123456789abcdef"
	const terminal = "term-fedcba9876543210"
	story := 4
	ticket := TicketPayload(api.TicketSummary{
		Number: 9, Type: "task", Title: "T", Column: "review", Story: &story, Supervisor: api.RoleUser,
		Projects:  []api.TicketProject{{Project: "web", Branch: "omo/9", Worktree: "/home/u/.local/omo/worktrees/9", BaseBranch: "main"}},
		CreatedBy: api.Actor{Role: api.RoleDeveloper, Session: session},
	}, "http://user:pw@127.0.0.1:8091/?token=secret-token#x")
	comment := CommentPayload(api.Comment{
		ID: 3, Ticket: 9, Author: api.Actor{Role: api.RoleDeveloper, Session: session, Name: "dev-1"}, Body: "b",
		QuestionTo: api.RolePM, Mentions: []api.Mention{{Role: api.RoleReviewer, Session: session}},
	})
	ticketNumber := 9
	agent := AgentPayload(api.AgentSession{
		ID: session, TerminalID: terminal, Role: api.RoleDeveloper, Ticket: &ticketNumber, Project: "web",
		Name: "dev-1", Model: "claude/opus", Workdir: "/home/u/.local/omo/worktrees/9", Purpose: api.PurposeWork, StartedAt: time.Now(),
	})
	raw, err := json.Marshal(Event{Name: EventAgentStarted, At: time.Now(), Data: map[string]any{"ticket": ticket, "comment": comment, "agent": agent}})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{session, terminal, "worktrees", "secret-token", "pw@", "user:"} {
		if strings.Contains(string(raw), bad) {
			t.Fatalf("payload holds %q: %s", bad, raw)
		}
	}
	if ticket["url"] != "http://127.0.0.1:8091/9" || ticket["story"] != 4 {
		t.Fatalf("ticket = %v", ticket)
	}
	if comment["question"] != true || comment["to"] != "pm" || len(comment["mentions"].([]any)) != 1 {
		t.Fatalf("comment = %v", comment)
	}
	if agent["name"] != "dev-1" || agent["model"] != "claude/opus" || agent["purpose"] != "work" || len(agent) != 4 {
		t.Fatalf("agent = %v", agent)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil || wire["event"] != EventAgentStarted || wire["at"] == "" {
		t.Fatalf("wire = %v, %v", wire, err)
	}
}

func TestExitReason(t *testing.T) {
	for in, want := range map[api.SessionEndKind]string{
		api.SessionEndShutdown: ExitOmo, api.SessionEndUsage: ExitUsage,
		api.SessionEndCrashed: ExitCrashed, api.SessionEndFinished: ExitFinished,
		api.SessionEndStopped: ExitStopped, api.SessionEndUser: ExitStopped, "": ExitStopped,
	} {
		if got := ExitReason(in); got != want {
			t.Errorf("ExitReason(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExitReasonDoesNotInferStateFromWords(t *testing.T) {
	for _, message := range []string{"crash avoided; stopped by request", "usage report requested; stopped by request"} {
		if got := ExitReason(api.SessionEndKind(message)); got != ExitStopped {
			t.Fatalf("message %q misclassified as %q", message, got)
		}
	}
}
