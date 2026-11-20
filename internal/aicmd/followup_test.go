package aicmd

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

func TestFollowUpCommand(t *testing.T) {
	c, ok := byTool("ticket_follow_up")
	if !ok {
		t.Fatal("missing follow-up command")
	}
	if !c.CommandLineOnly || c.Sees != nil {
		t.Fatal("user-only follow-up command exposed to agents")
	}
	fields, err := Fields(c.Input)
	if err != nil {
		t.Fatal(err)
	}
	var roleFlag, commentFlag bool
	for _, f := range fields {
		roleFlag = roleFlag || f.Flag == "role" && f.Required && f.Type == reflect.TypeFor[string]()
		commentFlag = commentFlag || f.Flag == "comment-file" && f.Text && !f.AllowEmpty
	}
	if !roleFlag || !commentFlag {
		t.Fatalf("missing required recipient or comment input: %+v", fields)
	}
	for _, role := range []string{"developer", "reviewer"} {
		t.Run(role, func(t *testing.T) {
			called := false
			client := apiclient.NewWithTransport(apiclient.HandlerTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.Method != "POST" || r.URL.Path != "/api/tickets/7/follow-up" {
					t.Errorf("wrong request %s %s", r.Method, r.URL)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				want := map[string]string{"role": role, "comment": "Rebase onto main.\nKeep the resolution."}
				if !reflect.DeepEqual(body, want) {
					t.Errorf("body = %#v, want %#v", body, want)
				}
				json.NewEncoder(w).Encode(api.Ticket{TicketSummary: api.TicketSummary{Number: 7, Type: api.TypeTask, Column: api.ColumnInProgress, Title: "Export"}})
			})), "")
			raw, err := json.Marshal(map[string]any{"ticket": 7, "recipient": role, "comment": "Rebase onto main.\nKeep the resolution."})
			if err != nil {
				t.Fatal(err)
			}
			result, err := c.Run(context.Background(), client, proxyTestInput(c, string(raw), t))
			if err != nil {
				t.Fatal(err)
			}
			if !called || result.Text != "#7 task in_progress Export\n" {
				t.Fatalf("called = %t, result = %+v", called, result)
			}
		})
	}
}
