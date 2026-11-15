package board

import (
	"encoding/json"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// A session id is the only credential of an agent: whoever presents it acts
// as that agent. The user and the system see session ids; every agent actor
// gets them blanked (set to "") in everything the board returns to it, its
// own id included. Fields stay in place, only their value is emptied.
// Every exported method that returns data passes its result through
// forActor, the one place this rule lives.

// seesSessions reports whether a may see session ids.
func (a Actor) seesSessions() bool { return a.Role == api.RoleUser || a.Role == api.RoleSystem }

// forActor returns v as a may see it. err is passed through unchanged so a
// method can end with `return forActor(a, out, err)`. A type without a rule
// below is a programming error and panics, so a new result type cannot leak
// session ids silently.
func forActor[T any](a Actor, v T, err error) (T, error) {
	if a.seesSessions() {
		return v, err
	}
	switch x := any(&v).(type) {
	case *api.Ticket:
		hideInSummary(&x.TicketSummary)
	case *api.TicketList:
		for i := range x.Tickets {
			hideInSummary(&x.Tickets[i])
		}
	case *api.Comment:
		hideInComment(x)
	case *[]api.Comment:
		for i := range *x {
			hideInComment(&(*x)[i])
		}
	case *api.CommentResult:
		hideInComment(&x.Comment)
	case *api.DescriptionVersion:
		x.Author.Session = ""
	case *[]api.DescriptionVersion:
		for i := range *x {
			(*x)[i].Author.Session = ""
		}
	case *api.AgentSession:
		x.ID = ""
	case *[]api.AgentSession:
		for i := range *x {
			(*x)[i].ID = ""
		}
	case *api.Me:
		x.Actor.Session = ""
	case *[]api.Event:
		for i := range *x {
			if e := hideInEvent(&(*x)[i]); e != nil && err == nil {
				var zero T
				return zero, e
			}
		}
	default:
		panic(fmt.Sprintf("board: no session id rule for %T", v))
	}
	return v, err
}

// Me is what GET /api/me answers to a.
func Me(a Actor) api.Me {
	me := api.Me{Actor: a.API()}
	if a.Ticket != 0 {
		n := int(a.Ticket)
		me.Ticket = &n
	}
	me, _ = forActor(a, me, nil)
	return me
}

func hideInSummary(s *api.TicketSummary) {
	s.CreatedBy.Session = ""
	for i := range s.Agents {
		s.Agents[i].Session = ""
	}
}

func hideInComment(c *api.Comment) {
	c.Author.Session = ""
	for i := range c.Mentions {
		c.Mentions[i].Session = ""
	}
}

// hideInEvent blanks the session ids in a stored event's actor and data. The
// data is decoded as the payload of its kind and encoded again; kinds whose
// payload carries no actor or session are left as they are.
func hideInEvent(e *api.Event) error {
	e.Actor.Session = ""
	var data any
	switch e.Kind {
	case api.KindTicketCreated:
		var d api.TicketCreatedData
		data = &d
	case api.KindTicketUpdated:
		var d api.TicketUpdatedData
		data = &d
	case api.KindTicketMoved:
		var d api.TicketMovedData
		data = &d
	case api.KindDescriptionChanged:
		var d api.DescriptionChangedData
		data = &d
	case api.KindCommentCreated:
		var d api.CommentCreatedData
		data = &d
	case api.KindAgentCreated, api.KindAgentUpdated, api.KindAgentEnded:
		var d api.AgentData
		data = &d
	default:
		return nil
	}
	if err := json.Unmarshal(e.Data, data); err != nil {
		return fmt.Errorf("decode %s event %d: %w", e.Kind, e.ID, err)
	}
	switch d := data.(type) {
	case *api.TicketCreatedData:
		hideInSummary(&d.Ticket)
	case *api.TicketUpdatedData:
		hideInSummary(&d.Ticket)
	case *api.TicketMovedData:
		hideInSummary(&d.Ticket)
	case *api.DescriptionChangedData:
		d.Version.Author.Session = ""
	case *api.CommentCreatedData:
		hideInComment(&d.Comment)
	case *api.AgentData:
		d.Agent.ID = ""
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	e.Data = raw
	return nil
}
