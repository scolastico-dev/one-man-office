package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

// boardEvent builds a streamed event of kind with data.
func boardEvent(t *testing.T, id int64, kind string, data any) api.Event {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return api.Event{ID: id, Kind: kind, At: "2026-11-15T10:00:00.123Z", Data: raw}
}

func TestPluginEventMapping(t *testing.T) {
	seven := 7
	tk := api.TicketSummary{Number: 7, Type: api.TypeTask, Title: "Fix it", Column: api.ColumnReady}
	flagged := tk
	flagged.Attention = "merge conflict"
	other := tk
	other.Attention = "review failed"
	done := flagged
	done.Column = api.ColumnDone
	ticket := func(n int) map[string]any {
		if n != 7 {
			return nil
		}
		return plugins.TicketPayload(tk, "")
	}
	comment := api.Comment{ID: 3, Ticket: 7, Author: api.Actor{Role: api.RoleDeveloper, Name: "dev"}, Body: "done"}
	question := comment
	question.QuestionTo = api.RoleUser
	agent := api.AgentSession{ID: "s1", Role: api.RoleDeveloper, Ticket: &seven, Name: "dev", Model: "fake/a", Purpose: api.PurposeWork}
	ended := agent
	ended.EndReason = "Different words that mention crash but describe a usage stop"
	ended.EndKind = api.SessionEndUsage
	ceo := api.AgentSession{ID: "s2", Role: api.RoleCEO, Name: "ceo"}

	att := map[int]string{}
	cases := []struct {
		name string
		ev   api.Event
		want []string
	}{
		{"created", boardEvent(t, 1, api.KindTicketCreated, api.TicketCreatedData{Ticket: tk}), []string{plugins.EventTicketCreated}},
		{"moved", boardEvent(t, 2, api.KindTicketMoved, api.TicketMovedData{Ticket: tk, From: api.ColumnPlanning, To: api.ColumnReady, Action: "move"}), []string{plugins.EventTicketMoved}},
		{"moved within its column", boardEvent(t, 3, api.KindTicketMoved, api.TicketMovedData{Ticket: tk, From: api.ColumnReady, To: api.ColumnReady}), nil},
		{"updated without attention", boardEvent(t, 4, api.KindTicketUpdated, api.TicketUpdatedData{Ticket: tk, Fields: []string{"title"}}), nil},
		{"attention set", boardEvent(t, 5, api.KindTicketUpdated, api.TicketUpdatedData{Ticket: flagged, Fields: []string{"attention"}}), []string{plugins.EventNeedsAttention}},
		{"same attention again", boardEvent(t, 6, api.KindTicketUpdated, api.TicketUpdatedData{Ticket: flagged, Fields: []string{"derived"}}), nil},
		{"moved with the same attention", boardEvent(t, 7, api.KindTicketMoved, api.TicketMovedData{Ticket: flagged, From: api.ColumnReady, To: api.ColumnReview}), []string{plugins.EventTicketMoved}},
		{"attention changed", boardEvent(t, 8, api.KindTicketUpdated, api.TicketUpdatedData{Ticket: other, Fields: []string{"attention"}}), []string{plugins.EventNeedsAttention}},
		{"attention cleared", boardEvent(t, 9, api.KindTicketUpdated, api.TicketUpdatedData{Ticket: tk, Fields: []string{"attention"}}), nil},
		{"attention set again", boardEvent(t, 10, api.KindTicketMoved, api.TicketMovedData{Ticket: other, From: api.ColumnReview, To: api.ColumnReady}), []string{plugins.EventTicketMoved, plugins.EventNeedsAttention}},
		{"done forgets", boardEvent(t, 11, api.KindTicketMoved, api.TicketMovedData{Ticket: done, From: api.ColumnReady, To: api.ColumnDone}), []string{plugins.EventTicketMoved}},
		{"comment", boardEvent(t, 12, api.KindCommentCreated, api.CommentCreatedData{Comment: comment}), []string{plugins.EventCommentWritten}},
		{"question", boardEvent(t, 13, api.KindCommentCreated, api.CommentCreatedData{Comment: question}), []string{plugins.EventCommentWritten, plugins.EventQuestionAsked}},
		{"agent started", boardEvent(t, 14, api.KindAgentCreated, api.AgentData{Agent: agent}), []string{plugins.EventAgentStarted}},
		{"agent updated", boardEvent(t, 15, api.KindAgentUpdated, api.AgentData{Agent: agent}), nil},
		{"agent exited", boardEvent(t, 16, api.KindAgentEnded, api.AgentData{Agent: ended}), []string{plugins.EventAgentExited}},
		{"ceo started", boardEvent(t, 17, api.KindAgentCreated, api.AgentData{Agent: ceo}), []string{plugins.EventAgentStarted}},
		{"settings", boardEvent(t, 18, api.KindSettingsChanged, api.SettingsChangedData{Section: "plugins"}), nil},
		{"plugin changed", boardEvent(t, 19, api.KindPluginChanged, api.PluginInfo{Name: "x"}), nil},
		{"description", boardEvent(t, 20, api.KindDescriptionChanged, api.DescriptionChangedData{Ticket: 7}), nil},
		{"paused", boardEvent(t, 21, api.KindServerPaused, api.ServerPausedData{Paused: true}), []string{plugins.EventPaused}},
		{"resumed", boardEvent(t, 22, api.KindServerPaused, api.ServerPausedData{Paused: false}), []string{plugins.EventResumed}},
		{"broken pause", api.Event{ID: 23, Kind: api.KindServerPaused, At: "2026-11-15T10:00:00.123Z", Data: []byte(`{"paused":"yes"}`)}, nil},
	}
	for _, c := range cases {
		got := pluginEvents(c.ev, att, ticket, "http://omo.localhost:1/#token")
		var names []string
		for _, e := range got {
			names = append(names, e.Name)
			if e.At.IsZero() || !e.At.Equal(time.Date(2026, 11, 15, 10, 0, 0, 123e6, time.UTC)) {
				t.Errorf("%s: at %v", c.name, e.At)
			}
		}
		if !slices.Equal(names, c.want) {
			t.Errorf("%s: %v, want %v", c.name, names, c.want)
		}
		if (c.name == "paused" || c.name == "resumed") && len(got[0].Data) != 0 {
			t.Errorf("%s: data %v, want the empty object", c.name, got[0].Data)
		}
		if c.name == "agent exited" && got[0].Data["reason"] != plugins.ExitUsage {
			t.Errorf("exit reason %v", got[0].Data["reason"])
		}
		if c.name == "ceo started" && got[0].Data["ticket"] != nil {
			t.Errorf("the CEO's ticket is %v, want null", got[0].Data["ticket"])
		}
		if c.name == "attention set" {
			if got[0].Data["reason"] != "merge conflict" {
				t.Errorf("reason %v", got[0].Data["reason"])
			}
			raw, _ := json.Marshal(got[0])
			if strings.Contains(string(raw), "token") {
				t.Errorf("the board's token reached a payload: %s", raw)
			}
		}
	}
	if len(att) != 0 {
		t.Errorf("a Done ticket's attention is kept: %v", att)
	}
}

func TestPluginPayloadsHoldNoSessionID(t *testing.T) {
	seven := 7
	tk := api.TicketSummary{Number: 7, Type: api.TypeTask, Title: "t", Column: api.ColumnReady, Attention: "look",
		Agents: []api.AgentRef{{Session: "sess-secret-1", Role: api.RoleDeveloper, Name: "dev"}}}
	ticket := func(int) map[string]any { return plugins.TicketPayload(tk, "") }
	comment := api.Comment{ID: 1, Ticket: 7, QuestionTo: api.RoleUser,
		Author:   api.Actor{Role: api.RoleDeveloper, Name: "dev", Session: "sess-secret-2"},
		Mentions: []api.Mention{{Role: api.RolePM, Session: "sess-secret-3"}}}
	agent := api.AgentSession{ID: "sess-secret-4", TerminalID: "term-secret-5", Workdir: "/work/dir-secret-6", Role: api.RoleDeveloper, Ticket: &seven}
	att := map[int]string{}
	var all []plugins.Event
	for i, ev := range []api.Event{
		boardEvent(t, 1, api.KindTicketCreated, api.TicketCreatedData{Ticket: tk}),
		boardEvent(t, 2, api.KindTicketMoved, api.TicketMovedData{Ticket: tk, From: api.ColumnPlanning, To: api.ColumnReady}),
		boardEvent(t, 3, api.KindCommentCreated, api.CommentCreatedData{Comment: comment}),
		boardEvent(t, 4, api.KindAgentCreated, api.AgentData{Agent: agent}),
		boardEvent(t, 5, api.KindAgentEnded, api.AgentData{Agent: agent}),
	} {
		ev.Actor = api.Actor{Role: api.RoleDeveloper, Session: "sess-secret-actor"}
		got := pluginEvents(ev, att, ticket, "")
		if len(got) == 0 {
			t.Fatalf("event %d mapped to nothing", i)
		}
		all = append(all, got...)
	}
	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret") {
		t.Fatalf("a session id, terminal id or folder reached a payload: %s", raw)
	}
}

// TestPluginEventsEndToEnd runs a command hook that appends its standard
// input to a file: creating and moving a ticket, a comment and a question
// of an agent, and the agent's start reach it in order, as data, and
// without the agent's session id.
func TestPluginEventsEndToEnd(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	events := dataFile(s, "rec", "events.jsonl")
	ctx := context.Background()

	m := newTicketMaker(t, s)
	tk, err := s.board.Create(ctx, board.User, api.CreateTicket{Type: api.TypeTask, Title: `$(touch pwned) "quoted"`, Projects: []string{m.project}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Move(ctx, board.User, int64(tk.Number), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		t.Fatal(err)
	}
	got := recordedEvents(t, waitLines(t, events, 2))
	if names := eventNames(got); !slices.Equal(names[:2], []string{plugins.EventTicketCreated, plugins.EventTicketMoved}) {
		t.Fatalf("events %v", names)
	}
	data := got[0]["data"].(map[string]any)
	if title := data["ticket"].(map[string]any)["title"]; title != `$(touch pwned) "quoted"` {
		t.Fatalf("title %v", title)
	}
	moved := got[1]["data"].(map[string]any)
	if moved["from"] != string(api.ColumnPlanning) || moved["to"] != string(api.ColumnReady) {
		t.Fatalf("moved %v", moved)
	}

	sess := liveSession(t, s, api.RoleDeveloper)
	actor, err := s.board.ResolveSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.board.Comment(ctx, actor, int64(*sess.Ticket), api.CommentRequest{Body: "What now?", Question: true, To: "user"}); err != nil {
		t.Fatal(err)
	}
	lines := waitLines(t, events, 6)
	all := recordedEvents(t, lines)
	names := eventNames(all)
	for _, want := range []string{plugins.EventAgentStarted, plugins.EventCommentWritten, plugins.EventQuestionAsked} {
		if !slices.Contains(names, want) {
			t.Fatalf("no %s in %v", want, names)
		}
	}
	if i, j := slices.Index(names, plugins.EventCommentWritten), slices.Index(names, plugins.EventQuestionAsked); i > j {
		t.Fatalf("question_asked before comment_written: %v", names)
	}
	for _, l := range lines {
		if strings.Contains(l, sess.ID) {
			t.Fatalf("the session id reached a plugin: %s", l)
		}
	}
	if _, err := os.Lstat(filepath.Join(s.opts.Layout.Plugins, "rec", "pwned")); err == nil {
		t.Fatal("a title ran as a command")
	}
}

// TestPluginEventsNotReplayedAfterRestart: events of an earlier run of omo
// are not handed to plugins again; events after the start are.
func TestPluginEventsNotReplayedAfterRestart(t *testing.T) {
	isolateGit(t)
	layout := testHome(t)
	s := openServer(t, layout, testConfig())
	b := newBrowser(t, s)
	installRecorder(t, s, b)
	events := dataFile(s, "rec", "events.jsonl")
	m := newTicketMaker(t, s)
	m.create()
	waitLines(t, events, 1)
	// An event the plugin does not get before omo stops: the runtime is
	// closed first, the board goes on.
	s.plugins.m.Close()
	m.create()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2 := openServer(t, layout, testConfig())
	time.Sleep(300 * time.Millisecond)
	if n := len(readLines(t, events)); n != 1 {
		t.Fatalf("%d lines after the restart, want 1: events were replayed", n)
	}
	m2 := &ticketMaker{t: t, s: s2, project: m.project}
	m2.create()
	got := eventNames(recordedEvents(t, waitLines(t, events, 2)))
	if len(got) != 2 {
		t.Fatalf("events %v", got)
	}
}

// TestPluginAttentionNotRepeatedAfterRestart: a ticket that needed
// attention before omo stopped does not count as new after the start.
func TestPluginAttentionNotRepeatedAfterRestart(t *testing.T) {
	isolateGit(t)
	layout := testHome(t)
	s := openServer(t, layout, testConfig())
	m := newTicketMaker(t, s)
	n := m.create()
	if err := s.board.SetAttention(context.Background(), int64(n), "look at me"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2 := openServer(t, layout, testConfig())
	installRecorder(t, s2, newBrowser(t, s2))
	events := dataFile(s2, "rec", "events.jsonl")
	ctx := context.Background()
	if _, err := s2.board.Move(ctx, board.User, int64(n), api.MoveRequest{Column: api.ColumnReady}); err != nil {
		t.Fatal(err)
	}
	if err := s2.board.SetAttention(ctx, int64(n), "now this"); err != nil {
		t.Fatal(err)
	}
	waitLines(t, events, 2)
	time.Sleep(200 * time.Millisecond)
	// Setting attention also writes a system comment; only the ticket
	// events matter here.
	got := slices.DeleteFunc(eventNames(recordedEvents(t, readLines(t, events))), func(n string) bool {
		return n == plugins.EventCommentWritten
	})
	if !slices.Equal(got, []string{plugins.EventTicketMoved, plugins.EventNeedsAttention}) {
		t.Fatalf("events %v, want one ticket_moved and one ticket_needs_attention for the new text", got)
	}
}

// TestPluginDispatchNeverSlowsTheBoard: a plugin whose hook hangs and a
// burst of more events than its queue holds do not slow the board's
// writes; the plugin gets what fits, the rest is dropped for it.
func TestPluginDispatchNeverSlowsTheBoard(t *testing.T) {
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	man := recordManifest("slow")
	man["hooks"] = []any{map[string]any{"event": plugins.EventTicketCreated, "command": []string{"sh", "-c", "sleep 30"}, "timeout": "30s"}}
	resp := asUser(b, s, "POST", "/api/plugins", community(api.PluginSettings{Name: "slow", Source: pluginRepo(t, man)}))
	if resp.StatusCode != 201 {
		t.Fatalf("install: %d %s", resp.StatusCode, respText(t, resp))
	}
	m := newTicketMaker(t, s)
	started := time.Now()
	for range plugins.QueueSize + 50 {
		m.create()
	}
	// The same burst without any plugin takes a fraction of this; a
	// dispatch that waited for the hook would take 30 s per event.
	if d := time.Since(started); d > 20*time.Second {
		t.Fatalf("creating %d tickets took %s with a hanging plugin", plugins.QueueSize+50, d)
	}
	deadline := time.Now().Add(10 * time.Second)
	for s.pluginInfo("slow").State != plugins.StateRunning {
		if time.Now().After(deadline) {
			t.Fatalf("the hook never ran: %+v", s.pluginInfo("slow"))
		}
		time.Sleep(20 * time.Millisecond)
	}
}
