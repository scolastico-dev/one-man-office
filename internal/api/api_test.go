package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOptionalDistinguishesAbsentNullValue(t *testing.T) {
	var u UpdateTicket
	if err := json.Unmarshal([]byte(`{"model":null}`), &u); err != nil {
		t.Fatal(err)
	}
	if !u.Model.Set || !u.Model.Null || u.Title.Set {
		t.Fatalf("null: %+v", u)
	}
	u = UpdateTicket{}
	if err := json.Unmarshal([]byte(`{}`), &u); err != nil {
		t.Fatal(err)
	}
	if u.Model.Set {
		t.Fatalf("absent: %+v", u.Model)
	}
	u = UpdateTicket{}
	if err := json.Unmarshal([]byte(`{"model":"a/b","max_parallel":3}`), &u); err != nil {
		t.Fatal(err)
	}
	if !u.Model.Set || u.Model.Null || u.Model.Value != "a/b" || u.MaxParallel.Value != 3 {
		t.Fatalf("value: %+v", u)
	}
	b, err := json.Marshal(UpdateTicket{})
	if err != nil || string(b) != "{}" {
		t.Fatalf("marshal empty: %s %v", b, err)
	}
	b, _ = json.Marshal(UpdateTicket{Model: Null[string](), Title: Some("x")})
	if string(b) != `{"title":"x","model":null}` {
		t.Fatalf("marshal: %s", b)
	}
}

func TestErrorfStatus(t *testing.T) {
	cases := map[string]int{
		CodeDependencyCycle: 422, CodeFieldLocked: 409, CodeUnknownSession: 401,
		CodeBadRequest: 400, CodeForbidden: 403, CodeNotFound: 404, CodeTooLarge: 413,
		CodeInternal: 500, CodeNotImplemented: 501, CodeInUse: 409, CodeInvalidSettings: 422,
		CodeUnavailable: 503,
	}
	for code, want := range cases {
		if got := Errorf(code, "x %d", 1).Status; got != want {
			t.Errorf("%s: %d, want %d", code, got, want)
		}
	}
	e := Errorf(CodeValidation, "bad %s", "x")
	if e.Message != "bad x" || e.Error() == "" {
		t.Fatalf("%+v", e)
	}
	b, _ := json.Marshal(ErrorEnvelope{Error: e})
	if string(b) != `{"error":{"code":"validation","message":"bad x"}}` {
		t.Fatalf("%s", b)
	}
}

func TestTicketSummaryJSONNames(t *testing.T) {
	b, err := json.Marshal(Ticket{Description: "d"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"number", "type", "title", "column", "resolution", "rank", "story", "projects",
		"supervisor", "created_by", "model", "review", "effective_review", "when_approved",
		"effective_when_approved", "task_release", "effective_task_release", "max_parallel",
		"review_stage", "review_round", "attention", "export_url", "export_merged", "depends_on",
		"blocks", "waiting_for", "open_questions", "questions_for_user", "progress", "agents",
		"description_version", "created_at", "updated_at", "closed_at", "description"} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	if string(m["story"]) != "null" || string(m["closed_at"]) != "null" {
		t.Errorf("nullable fields: %s %s", m["story"], m["closed_at"])
	}
	lb, _ := json.Marshal(TicketList{})
	if strings.Contains(string(lb), "next_done_before") {
		t.Errorf("next_done_before not omitted: %s", lb)
	}
	lb, _ = json.Marshal(TicketList{NextDoneBefore: 7})
	if !strings.Contains(string(lb), `"next_done_before":7`) {
		t.Errorf("%s", lb)
	}
}

func TestStreamed(t *testing.T) {
	if !Streamed(KindTicketMoved) || Streamed("internal") {
		t.Fatal("Streamed")
	}
}
