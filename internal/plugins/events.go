package plugins

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// The events a plugin hook can name.
const (
	EventTicketCreated  = "ticket_created"
	EventTicketMoved    = "ticket_moved"
	EventCommentWritten = "comment_written"
	EventQuestionAsked  = "question_asked"
	EventNeedsAttention = "ticket_needs_attention"
	EventAgentStarted   = "agent_started"
	EventAgentExited    = "agent_exited"
	EventPaused         = "omo_paused"
	EventResumed        = "omo_resumed"
	EventTimer          = "timer"
	EventManual         = "manual"
)

// Events lists every event a hook can name.
var Events = []string{
	EventTicketCreated, EventTicketMoved, EventCommentWritten, EventQuestionAsked,
	EventNeedsAttention, EventAgentStarted, EventAgentExited, EventPaused, EventResumed, EventTimer, EventManual,
}

// The reasons of agent_exited.
const (
	ExitFinished = "finished"
	ExitStopped  = "stopped"
	ExitCrashed  = "crashed"
	ExitUsage    = "usage"
	ExitOmo      = "omo stopped"
)

// Bounds of the text a payload carries.
const (
	// MaxBodyBytes bounds a comment body.
	MaxBodyBytes = 4 << 10
	// MaxTextBytes bounds the other texts people or agents write (a
	// title, an attention text).
	MaxTextBytes = 4 << 10
	// maxNameBytes bounds names (agents, models, projects).
	maxNameBytes = 200
)

// Event is one event handed to plugins. Its JSON is
// {"event": <name>, "at": <RFC 3339>, "data": {...}}: the standard input of a
// command hook and the global event of a Lua hook. Data holds only plain
// values (strings, numbers, booleans, lists and objects with fixed keys) as
// the payload builders of this file make them; text written by people or
// agents is a string value in it and nothing else.
type Event struct {
	Name string
	At   time.Time
	Data map[string]any
}

// wire is the event as plugins see it.
func (e Event) wire() map[string]any {
	data := e.Data
	if data == nil {
		data = map[string]any{}
	}
	return map[string]any{"event": e.Name, "at": e.At.UTC().Format(time.RFC3339), "data": data}
}

// MarshalJSON writes the event as plugins see it.
func (e Event) MarshalJSON() ([]byte, error) { return json.Marshal(e.wire()) }

// TicketPayload is the ticket object of a payload. address is the board's
// address; the ticket's url is built from its scheme and host only, so no
// token, user or query of it reaches a plugin.
func TicketPayload(t api.TicketSummary, address string) map[string]any {
	projects := make([]any, 0, len(t.Projects))
	for _, p := range t.Projects {
		projects = append(projects, cutText(p.Project, maxNameBytes))
	}
	var story any
	if t.Story != nil {
		story = *t.Story
	}
	return map[string]any{
		"number":     t.Number,
		"type":       string(t.Type),
		"title":      cutText(t.Title, MaxTextBytes),
		"column":     string(t.Column),
		"story":      story,
		"projects":   projects,
		"supervisor": string(t.Supervisor),
		"attention":  cutText(t.Attention, MaxTextBytes),
		"url":        ticketURL(address, t.Number),
	}
}

// ticketURL is the board's address of ticket n without anything but scheme,
// host and path; "" when address is not an http(s) address.
func ticketURL(address string, n int) string {
	u, err := url.Parse(strings.TrimSpace(address))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	clean := url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/" + strconv.Itoa(n)}
	return clean.String()
}

// CommentPayload is the comment object of comment_written and
// question_asked: id, author (role and name), body cut to 4 KiB at a
// character boundary, question, to (the asked role, "" for a comment) and
// mentions (the mentioned roles). No session id goes into it.
func CommentPayload(c api.Comment) map[string]any {
	mentions := make([]any, 0, len(c.Mentions))
	for _, m := range c.Mentions {
		mentions = append(mentions, string(m.Role))
	}
	return map[string]any{
		"id":       c.ID,
		"author":   map[string]any{"role": string(c.Author.Role), "name": cutText(c.Author.Name, maxNameBytes)},
		"body":     cutText(c.Body, MaxBodyBytes),
		"question": c.QuestionTo != "",
		"to":       string(c.QuestionTo),
		"mentions": mentions,
	}
}

// AgentPayload is the agent object of agent_started and agent_exited:
// name, role, model and purpose. No session id, terminal id or folder goes
// into it.
func AgentPayload(a api.AgentSession) map[string]any {
	return map[string]any{
		"name":    cutText(a.Name, maxNameBytes),
		"role":    string(a.Role),
		"model":   cutText(a.Model, maxNameBytes),
		"purpose": string(a.Purpose),
	}
}

// ExitReason maps the session's structured outcome to the fixed plugin reason.
func ExitReason(kind api.SessionEndKind) string {
	switch kind {
	case api.SessionEndShutdown:
		return ExitOmo
	case api.SessionEndUsage:
		return ExitUsage
	case api.SessionEndCrashed:
		return ExitCrashed
	case api.SessionEndFinished:
		return ExitFinished
	default:
		return ExitStopped
	}
}

// cutText cuts s to at most n bytes at a character boundary. Invalid UTF-8
// is replaced first, so the result is always valid text.
func cutText(s string, n int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\uFFFD")
	}
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
