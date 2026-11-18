package cli

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

func TestAIProxyCommands(t *testing.T) {
	e := startAI(t)
	ctx := context.Background()
	out := e.must("", "proxy", "add", "--port", "5173", "--name", "global dev")
	for _, text := range []string{"base_path:", "/proxy/", "not started by omo"} {
		contains(t, out, text)
	}
	var globals api.ProxyForwardList
	if err := json.Unmarshal([]byte(e.must("", "proxy", "list", "--json")), &globals); err != nil || len(globals.Forwards) != 1 || globals.Forwards[0].Ticket != nil {
		t.Fatalf("global list %+v %v", globals, err)
	}
	tk, err := e.user.CreateTicket(ctx, api.CreateTicket{Type: api.TypeTask, Title: "preview", Projects: []string{e.project}})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := e.board.CreateSession(ctx, board.NewSession{Role: api.RoleDeveloper, Ticket: int64(tk.Number), Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_AGENT_ID", sess.ID)
	var own api.ProxyForward
	out = e.must("", "proxy", "add", "--port", "3000", "--name", "ticket UI", "--host", "::1", "--scheme", "https", "--start-path", "/demo?q=a%2Fb", "--strip-prefix", "--verify-tls", "--json")
	if err := json.Unmarshal([]byte(out), &own); err != nil || own.Ticket == nil || *own.Ticket != tk.Number || own.Host != "::1" || own.Scheme != "https" || !own.StripPrefix || !own.VerifyTLS || own.StartPath != "/demo?q=a%2Fb" {
		t.Fatalf("own %+v %v", own, err)
	}
	out = e.must("", "proxy", "list")
	contains(t, out, own.ID)
	if strings.Contains(out, globals.Forwards[0].ID) {
		t.Fatal("agent list leaked global")
	}
	if _, err := e.run("", "proxy", "remove", globals.Forwards[0].ID); err == nil {
		t.Fatal("agent removed global")
	}
	e.must("", "proxy", "remove", own.ID)
	contains(t, e.must("", "proxy", "list", "--json"), `"forwards":[]`)
	asUser(t)
	var explicit api.ProxyForward
	if err := json.Unmarshal([]byte(e.must("", "proxy", "add", "--ticket", strconv.Itoa(tk.Number), "--port", "8080", "--name", "explicit", "--json")), &explicit); err != nil || explicit.Ticket == nil || *explicit.Ticket != tk.Number {
		t.Fatalf("explicit %+v %v", explicit, err)
	}
	e.must("", "proxy", "remove", globals.Forwards[0].ID)
	for _, args := range [][]string{{"proxy", "add", "--name", "missing port"}, {"proxy", "add", "--port", "1"}, {"proxy", "list", "--ticket", "0"}} {
		if _, err := e.run("", args...); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
