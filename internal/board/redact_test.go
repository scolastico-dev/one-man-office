package board

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// sessionBoard is a board with a story, a task in it and a standalone task,
// live sessions in every agent role, and comments, mentions, description
// versions and events that all name sessions.
type sessionBoard struct {
	b      *Board
	agents map[string]Actor
	ids    []string
}

func newSessionBoard(t *testing.T) sessionBoard {
	t.Helper()
	ctx := context.Background()
	b, _ := newTestBoard(t)
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	pm := agent(t, b, api.RolePM, 1)
	mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(1), Description: "first"})
	if _, err := b.SetDescription(ctx, pm, 2, api.DescriptionUpdate{Text: "second"}); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "U", Projects: []string{"api"}, Story: intPtr(1)})
	if _, err := b.AddDependency(ctx, User, 2, 3); err != nil {
		t.Fatal(err)
	}
	sb := sessionBoard{b: b, agents: map[string]Actor{
		"ceo":         agent(t, b, api.RoleCEO, 0),
		"pm":          pm,
		"developer":   agent(t, b, api.RoleDeveloper, 2),
		"reviewer":    agent(t, b, api.RoleReviewer, 2),
		"reviewer-s":  agent(t, b, api.RoleReviewer, 1),
		"developer-u": agent(t, b, api.RoleDeveloper, 3),
	}}
	for _, a := range sb.agents {
		sb.ids = append(sb.ids, a.Session)
	}
	comment := func(a Actor, n int64, body string, question bool) {
		t.Helper()
		if _, err := b.Comment(ctx, a, n, api.CommentRequest{Body: body, Question: question}); err != nil {
			t.Fatal(err)
		}
	}
	comment(User, 2, "@developer @reviewer @pm please look", false)
	comment(sb.agents["developer"], 2, "@reviewer ready for you", false)
	comment(pm, 1, "@ceo which order?", true)
	comment(sb.agents["ceo"], 1, "@pm first the api", false)
	return sb
}

// reads calls every board method that returns data as a and returns the
// JSON encoding of each result, or the text of its error.
func (sb sessionBoard) reads(t *testing.T, a Actor) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	out := map[string][]byte{}
	add := func(name string, v any, err error) {
		if err != nil {
			out[name] = []byte(err.Error())
			return
		}
		raw, jerr := json.Marshal(v)
		if jerr != nil {
			t.Fatal(jerr)
		}
		out[name] = raw
	}
	for n := int64(1); n <= 3; n++ {
		v1, err := sb.b.Get(ctx, a, n)
		add(fmt.Sprintf("Get(%d)", n), v1, err)
		v2, err := sb.b.Comments(ctx, a, n)
		add(fmt.Sprintf("Comments(%d)", n), v2, err)
		v3, err := sb.b.Descriptions(ctx, a, n)
		add(fmt.Sprintf("Descriptions(%d)", n), v3, err)
		v4, err := sb.b.TicketEvents(ctx, a, n)
		add(fmt.Sprintf("TicketEvents(%d)", n), v4, err)
		v5, err := sb.b.TicketSessions(ctx, a, n)
		add(fmt.Sprintf("TicketSessions(%d)", n), v5, err)
	}
	v6, err := sb.b.List(ctx, a, api.TicketFilter{})
	add("List", v6, err)
	for _, live := range []bool{false, true} {
		v7, err := sb.b.AgentSessions(ctx, a, live)
		add(fmt.Sprintf("AgentSessions(%t)", live), v7, err)
	}
	add("Me", Me(a), nil)
	// Results of writes carry the same data.
	own := a.Ticket
	if own == 0 {
		own = 1
	}
	v8, err := sb.b.Comment(ctx, a, own, api.CommentRequest{Body: "@" + string(api.RoleUser) + " noted"})
	add("Comment", v8, err)
	v9, err := sb.b.AddDependency(ctx, a, own, 3)
	add("AddDependency", v9, err)
	return out
}

func TestAgentsNeverSeeSessionIDs(t *testing.T) {
	sb := newSessionBoard(t)
	for name, a := range sb.agents {
		got := sb.reads(t, a)
		succeeded := 0
		for method, raw := range got {
			if raw[0] == '{' || raw[0] == '[' {
				succeeded++
			}
			for _, id := range sb.ids {
				if bytes.Contains(raw, []byte(id)) {
					t.Errorf("%s: %s shows session id %s: %s", name, method, id, raw)
				}
			}
		}
		if succeeded < 8 {
			t.Errorf("%s: only %d of %d reads succeeded: %s", name, succeeded, len(got), got)
		}
	}
}

func TestUserAndSystemSeeSessionIDs(t *testing.T) {
	sb := newSessionBoard(t)
	ctx := context.Background()
	for _, a := range []Actor{User, System} {
		var all []byte
		for _, raw := range sb.reads(t, a) {
			all = append(all, raw...)
		}
		for _, id := range sb.ids {
			if !bytes.Contains(all, []byte(id)) {
				t.Errorf("%s does not see session %s", a.Role, id)
			}
		}
		tk, err := sb.b.Get(ctx, a, 2)
		if err != nil {
			t.Fatal(err)
		}
		if tk.CreatedBy.Session != sb.agents["pm"].Session || len(tk.Agents) != 2 || tk.Agents[0].Session == "" {
			t.Errorf("%s: ticket %+v", a.Role, tk.TicketSummary)
		}
	}
	// The stored events keep the real ids.
	evs, err := sb.b.TicketEvents(ctx, User, 2)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(evs)
	if !bytes.Contains(raw, []byte(sb.agents["developer"].Session)) {
		t.Fatal("stored events lost the session ids")
	}
}

func TestAgentMeHasNoSession(t *testing.T) {
	sb := newSessionBoard(t)
	dev := sb.agents["developer"]
	me := Me(dev)
	if me.Actor.Session != "" || me.Actor.Role != api.RoleDeveloper || me.Actor.Name != dev.Name || me.Ticket == nil || *me.Ticket != 2 {
		t.Fatalf("me %+v", me)
	}
	if me := Me(User); me.Actor.Role != api.RoleUser || me.Ticket != nil {
		t.Fatalf("user me %+v", me)
	}
}

func TestAgentStillSeesRolesAndNames(t *testing.T) {
	sb := newSessionBoard(t)
	tk, err := sb.b.Get(context.Background(), sb.agents["developer"], 2)
	if err != nil {
		t.Fatal(err)
	}
	if tk.CreatedBy.Role != api.RolePM || tk.CreatedBy.Name != "pm-agent" || len(tk.Agents) != 2 {
		t.Fatalf("ticket %+v", tk.TicketSummary)
	}
	for _, ref := range tk.Agents {
		if ref.Session != "" || ref.Role == "" || ref.Name == "" {
			t.Fatalf("agent ref %+v", ref)
		}
	}
}

func TestUnknownSessionErrorOmitsID(t *testing.T) {
	b, _ := newTestBoard(t)
	id := "0123456789abcdef0123456789abcdef"
	_, err := b.ResolveSession(context.Background(), id)
	wantCode(t, err, api.CodeUnknownSession)
	if bytes.Contains([]byte(err.Error()), []byte(id)) {
		t.Fatalf("error echoes the id: %v", err)
	}
}
