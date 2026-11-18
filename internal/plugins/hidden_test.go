package plugins

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Losing hidden metadata would expose script-owned actions as generic buttons.
func TestHiddenManualHooksMetadataAndInvocation(t *testing.T) {
	e := newEnv(t)
	raw := `{"hooks":[{"event":"manual","name":"private","description":"Private","scope":"ticket","hidden":true,"lua":"a.lua"},{"event":"manual","name":"visible","description":"Visible","lua":"a.lua"},{"event":"manual","name":"explicit","description":"Explicit","hidden":false,"lua":"a.lua"}],"buttons":[{"label":"Declared","place":"ticket","action":"private"}]}`
	e.plugin("p", raw, map[string]string{"a.lua": `return "called " .. event.data.action`})
	e.load(enabled("p", nil))
	info := e.info("p")
	if info.State != StateReady {
		t.Fatalf("hidden hook refused: %+v", info)
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Hooks []map[string]any `json:"hooks"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Hooks) != 3 || got.Hooks[0]["hidden"] != true {
		t.Fatalf("hidden metadata lost: %s", encoded)
	}
	for _, i := range []int{1, 2} {
		if got.Hooks[i]["hidden"] == true {
			t.Fatalf("visible hook hidden: %s", encoded)
		}
	}
	if len(info.Buttons) != 1 || info.Buttons[0].Action != "private" {
		t.Fatalf("explicit button lost: %+v", info.Buttons)
	}
	ticket := TicketPayload(api.TicketSummary{Number: 12, Title: "t"}, "")
	if got, err := e.m.Trigger(t.Context(), "p", "private", ticket); err != nil || got != "called private" {
		t.Fatalf("hidden hook invocation: %q, %v", got, err)
	}
}

// The presence of hidden on an event hook is invalid even if its value is false.
func TestHiddenHookRefusals(t *testing.T) {
	for _, tc := range []struct{ name, event, value, want string }{
		{"null", "manual", "null", "boolean"},
		{"string", "manual", `"true"`, "boolean"},
		{"number", "manual", "1", "boolean"},
		{"object", "manual", "{}", "boolean"},
		{"array", "manual", "[]", "boolean"},
		{"event true", "ticket_moved", "true", "manual hooks only"},
		{"event false", "ticket_moved", "false", "manual hooks only"},
		{"timer false", "timer", "false", "manual hooks only"},
		{"startup false", "omo_started", "false", "manual hooks only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `{"hooks":[{"event":"` + tc.event + `","hidden":` + tc.value + `,"lua":"a.lua"`
			if tc.event == "manual" {
				raw += `,"name":"a","description":"A"`
			}
			if tc.event == "timer" {
				raw += `,"interval":"1m"`
			}
			raw += `}]}`
			if _, err := ParseManifest([]byte(raw)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v; want %s", err, tc.want)
			}
		})
	}
}
