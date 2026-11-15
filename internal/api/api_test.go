package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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

func TestEngineWireNames(t *testing.T) {
	marshal := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	ts := marshal(TicketSummary{})
	for _, want := range []string{`"busy":false`, `"failed_step":""`, `"start_blocked":""`} {
		if !strings.Contains(ts, want) {
			t.Errorf("TicketSummary lacks %s: %s", want, ts)
		}
	}
	if got := marshal(WaitRequest{Timeout: Duration(10 * time.Minute)}); got != `{"timeout":"10m0s"}` && got != `{"timeout":"10m"}` {
		t.Errorf("WaitRequest = %s", got)
	}
	for name, v := range map[string]any{"done": DoneRequest{}, "cancel": CancelRequest{}, "moved": TicketMovedData{}, "approve": ApproveRequest{}} {
		raw := marshal(v)
		for _, key := range []string{"discard_uncommitted", `"url"`, `"error"`, `"merge"`} {
			if strings.Contains(raw, key) {
				t.Errorf("%s marshals %s: %s", name, key, raw)
			}
		}
	}
	if got := marshal(TicketProject{}); !strings.Contains(got, `"export_url":""`) {
		t.Errorf("TicketProject = %s", got)
	}
	if got := marshal(AgentRef{}); !strings.Contains(got, `"purpose":""`) {
		t.Errorf("AgentRef = %s", got)
	}
	if got := marshal(AgentSession{}); !strings.Contains(got, `"review_round":0`) || !strings.Contains(got, `"purpose":""`) {
		t.Errorf("AgentSession = %s", got)
	}
	if got := marshal(SchedulerState{Running: 1, Limit: 2, Paused: true}); got != `{"running":1,"limit":2,"paused":true}` {
		t.Errorf("SchedulerState = %s", got)
	}
	if got := marshal(TicketRetriedData{Ticket: 3, Step: FailedMerge}); got != `{"ticket":3,"step":"merge"}` {
		t.Errorf("TicketRetriedData = %s", got)
	}
	if KindSchedulerChanged != "scheduler.changed" || KindTicketRetried != "ticket.retried" {
		t.Error("event kinds")
	}
}

func TestProjectBranchFieldsAreOptional(t *testing.T) {
	raw, err := json.Marshal(Project{ID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "branch") {
		t.Errorf("unknown branches are sent: %s", raw)
	}
	raw, _ = json.Marshal(Project{CurrentBranch: "feature", DefaultBranch: "main"})
	if !strings.Contains(string(raw), `"current_branch":"feature"`) || !strings.Contains(string(raw), `"default_branch":"main"`) {
		t.Errorf("branches: %s", raw)
	}
}

func TestDetailTypesRoundTrip(t *testing.T) {
	keys := func(v any) map[string]json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	want := func(name string, v any, names ...string) map[string]json.RawMessage {
		t.Helper()
		m := keys(v)
		if len(m) != len(names) {
			t.Errorf("%s has %d keys, want %d: %v", name, len(m), len(names), m)
		}
		for _, k := range names {
			if _, ok := m[k]; !ok {
				t.Errorf("%s lacks key %q", name, k)
			}
		}
		return m
	}
	f := want("DiffFile", DiffFile{}, "path", "old_path", "status", "binary", "additions", "deletions", "patch", "truncated")
	if string(f["patch"]) != "null" {
		t.Errorf("patch = %s", f["patch"])
	}
	pd := want("ProjectDiff", ProjectDiff{Files: []DiffFile{}, Uncommitted: []DiffFile{}}, "project", "base", "branch",
		"base_commit", "head_commit", "state", "message", "files", "files_truncated", "uncommitted", "uncommitted_truncated")
	if string(pd["files"]) != "[]" || string(pd["uncommitted"]) != "[]" {
		t.Errorf("empty lists: %s %s", pd["files"], pd["uncommitted"])
	}
	want("TicketDiff", TicketDiff{Projects: []ProjectDiff{}}, "ticket", "projects", "computed_at")
	th := want("TicketThread", TicketThread{Events: []Event{}}, "events", "more")
	if string(th["events"]) != "[]" {
		t.Errorf("events = %s", th["events"])
	}
	want("MentionNotice", MentionNotice{}, "comment", "title", "unread")
	n := want("Notifications", Notifications{Mentions: []MentionNotice{}}, "mentions", "seen_through", "unread")
	if string(n["mentions"]) != "[]" {
		t.Errorf("mentions = %s", n["mentions"])
	}
	want("NotificationsSeen", NotificationsSeen{}, "through")
	ut := want("UserTerminal", UserTerminal{}, "id", "title", "dir", "project", "ticket", "state", "exit_error", "started_at", "ended_at")
	if string(ut["ticket"]) != "null" || string(ut["ended_at"]) != "null" {
		t.Errorf("nullable fields: %s %s", ut["ticket"], ut["ended_at"])
	}
	want("OpenTerminalRequest", OpenTerminalRequest{Project: "p"}, "project")
	n3 := 3
	want("OpenTerminalRequest with ticket", OpenTerminalRequest{Project: "p", Ticket: &n3}, "project", "ticket")
	want("TerminalData", TerminalData{}, "terminal")
	if KindTerminalOpened != "terminal.opened" || KindTerminalExited != "terminal.exited" ||
		KindTerminalClosed != "terminal.closed" || KindNotificationsSeen != "notifications.seen" {
		t.Error("event kinds")
	}
}

func strictDecode(src string, v any) error {
	dec := json.NewDecoder(strings.NewReader(src))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func TestOptionalRefusesUnknownNestedKeys(t *testing.T) {
	type nested struct {
		Known int `json:"known"`
	}
	type body struct {
		X Optional[nested] `json:"x"`
	}
	var b body
	if err := strictDecode(`{"x":{"unknown":1}}`, &b); err == nil {
		t.Fatal("unknown nested key accepted")
	}
	b = body{}
	if err := strictDecode(`{"x":{"known":2}}`, &b); err != nil || b.X.Value.Known != 2 {
		t.Fatalf("known nested key: %+v %v", b, err)
	}
	// The refusal does not depend on the caller's decoder.
	b = body{}
	if err := json.Unmarshal([]byte(`{"x":{"unknown":1}}`), &b); err == nil {
		t.Fatal("unknown nested key accepted by json.Unmarshal")
	}
}

func TestStaleSettingsIs409(t *testing.T) {
	if got := StatusFor(CodeStaleSettings); got != 409 {
		t.Fatalf("status %d", got)
	}
	if e := Errorf(CodeStaleSettings, "x"); e.Status != 409 || e.Code != "stale_settings" {
		t.Fatalf("%+v", e)
	}
}

func TestSetupTypesRoundTrip(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		v    any
		keys []string
	}{
		{ScanReport{Roots: []RootScan{}, Added: []string{}, StartedAt: now, FinishedAt: now}, []string{"trigger", "started_at", "finished_at", "roots", "added", "missing"}},
		{RootScan{Added: []string{}, Skipped: []string{}}, []string{"path", "state", "message", "visited", "found", "added", "skipped", "links"}},
		{ScanState{}, []string{"running", "last"}},
		{FolderListing{Folders: []FolderEntry{}}, []string{"path", "parent", "home", "folders", "more"}},
		{FolderEntry{}, []string{"name", "path", "repository", "hidden", "link"}},
		{AgentEnvCheck{Env: map[string]string{}}, []string{"env"}},
		{AgentEnvReport{Variables: []EnvVarCheck{}}, []string{"variables"}},
		{EnvVarCheck{}, []string{"name", "state", "message"}},
		{PromptPreview{}, []string{"text", "error"}},
		{DetectResult{Found: []DetectedCLI{}, Created: []string{}, Assigned: []string{}, Skipped: []string{}}, []string{"found", "created", "assigned", "skipped"}},
		{DetectedCLI{}, []string{"cli", "command", "path"}},
		{FieldDetail{Field: "a.b"}, []string{"field"}},
		{SettingsResponse{Errors: []FileError{}}, []string{"settings", "errors", "revision", "home"}},
		{PromptInfo{}, []string{"name", "customized", "kind", "revision"}},
		{Prompt{}, []string{"name", "text", "customized", "kind", "default", "revision"}},
		{SettingsChangedData{}, []string{"section", "name", "source"}},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if len(m) != len(c.keys) {
			t.Errorf("%T: keys %s, want %v", c.v, b, c.keys)
		}
		for _, k := range c.keys {
			raw, ok := m[k]
			if !ok {
				t.Errorf("%T: missing %q in %s", c.v, k, b)
			} else if string(raw) == "null" && k != "last" {
				t.Errorf("%T: %q is null in %s", c.v, k, b)
			}
		}
	}
	// omitempty revisions of profiles and roles.
	b, _ := json.Marshal(Profile{Revision: "ab"})
	if !strings.Contains(string(b), `"revision":"ab"`) {
		t.Errorf("profile: %s", b)
	}
	b, _ = json.Marshal(RoleSettings{})
	if strings.Contains(string(b), "revision") {
		t.Errorf("role: %s", b)
	}
	if NoRevision != "0" || SourceApp != "app" || SourceDisk != "disk" || KindProjectsScanned != "projects.scanned" ||
		PromptKindRole != "role" || PromptKindInstructions != "instructions" || PromptKindMessage != "message" {
		t.Error("constants")
	}
}
