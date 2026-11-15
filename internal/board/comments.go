package board

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// Question addressees of CommentRequest.To; supervisor is the default.
const (
	ToSupervisor = "supervisor"
	ToPM         = "pm"
	ToCEO        = "ceo"
	ToUser       = "user"
)

// Comment posts a comment on ticket n. Mentions in the body name roles on
// the ticket; each is stored with the live session behind it, which the
// engine notifies. A question (Question set) is addressed to To, by default
// the ticket's supervisor, and stays open until the addressee or the user
// replies to it. The result lists the mentioned agent roles that nobody is
// working in right now, so the writer can be told.
func (b *Board) Comment(ctx context.Context, a Actor, n int64, req api.CommentRequest) (api.CommentResult, error) {
	p := b.env.Policy()
	if err := checkActor(p, a); err != nil {
		return api.CommentResult{}, err
	}
	body := strings.TrimSpace(req.Body)
	if body == "" {
		return api.CommentResult{}, api.Errorf(api.CodeValidation, "a comment needs a body")
	}
	if !req.Question && req.To != "" {
		return api.CommentResult{}, api.Errorf(api.CodeValidation, "to addresses a question; set question to ask one")
	}
	var out api.CommentResult
	err := b.write(ctx, func(tx *sql.Tx) error {
		if err := canComment(tx, a, n); err != nil {
			return err
		}
		t, err := loadTicket(tx, n)
		if err != nil {
			return err
		}
		c := newComment{body: body, replyTo: req.ReplyTo, parse: true}
		if req.Question {
			to, err := questionAddressee(tx, p, a, t, req.To)
			if err != nil {
				return err
			}
			c.questionTo = to
			c.mentions = []api.Role{to}
		}
		out, err = b.postComment(tx, p, a, t, c)
		return err
	})
	return out, err
}

// questionAddressee resolves the addressee of a question asked by a on t.
// The supervisor (the default) is the effective one; when the asker is the
// supervisor itself, the question goes one step up the chain: a product
// manager asks its story's supervisor, the CEO the user.
func questionAddressee(q db.Queryer, p Policy, a Actor, t ticketRow, to string) (api.Role, error) {
	var role api.Role
	switch to {
	case "", ToSupervisor:
		role = effectiveSupervisor(p, t)
		if role == a.Role {
			switch role {
			case api.RolePM:
				story := t
				if t.inStory() {
					s, err := loadTicket(q, t.StoryID)
					if err != nil {
						return "", err
					}
					story = s
				}
				role = effectiveSupervisor(p, story)
			case api.RoleCEO:
				role = api.RoleUser
			}
		}
	case ToPM, ToCEO, ToUser:
		r, err := resolveMention(p, t, to)
		if err != nil {
			return "", err
		}
		role = r
	default:
		return "", api.Errorf(api.CodeValidation, "a question is addressed to supervisor, pm, ceo or user, not %q", to)
	}
	if role == a.Role {
		return "", api.Errorf(api.CodeValidation, "a %s cannot address a question to itself", a.Role)
	}
	return role, nil
}

// newComment is a comment about to be stored.
type newComment struct {
	body       string
	replyTo    *int
	questionTo api.Role
	// mentions are roles mentioned in any case, before those in the body.
	mentions []api.Role
	// parse reads @mentions from the body; an invalid one is an error.
	parse bool
}

// postComment is the one path every comment takes: it stores the comment
// and its mentions (each role once, with its live session), answers the
// question it replies to when the author may answer it, and emits
// comment.created, question.answered and ticket.updated as needed.
func (b *Board) postComment(tx *sql.Tx, p Policy, a Actor, t ticketRow, c newComment) (api.CommentResult, error) {
	roles := []api.Role{}
	for _, r := range c.mentions {
		if !slices.Contains(roles, r) {
			roles = append(roles, r)
		}
	}
	if c.parse {
		for _, name := range ParseMentions(c.body) {
			r, err := resolveMention(p, t, name)
			if err != nil {
				return api.CommentResult{}, err
			}
			if !slices.Contains(roles, r) {
				roles = append(roles, r)
			}
		}
	}

	var replyTo any
	var question *api.Comment
	if c.replyTo != nil {
		target, err := loadComment(tx, int64(*c.replyTo))
		if err != nil && errCode(err) != api.CodeNotFound {
			return api.CommentResult{}, err
		}
		if err != nil || int64(target.Ticket) != t.ID {
			return api.CommentResult{}, api.Errorf(api.CodeValidation, "comment %d is not a comment of #%d", *c.replyTo, t.ID)
		}
		replyTo = target.ID
		question = &target
	}

	res, err := tx.Exec(`INSERT INTO comments (ticket_id, author_role, author_session, body, reply_to, question_to, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, t.ID, a.Role, a.sessionArg(), c.body, replyTo, c.questionTo, b.stamp())
	if err != nil {
		return api.CommentResult{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return api.CommentResult{}, err
	}
	out := api.CommentResult{Unreachable: []api.Role{}}
	for _, r := range roles {
		session, err := liveSessionID(tx, t, r)
		if err != nil {
			return out, err
		}
		var sessionArg any
		if session != "" {
			sessionArg = session
		}
		if _, err := tx.Exec(`INSERT INTO comment_mentions (comment_id, role, session_id) VALUES (?, ?, ?)`,
			id, r, sessionArg); err != nil {
			return out, err
		}
		if session == "" && r != api.RoleUser && r != api.RoleCEO {
			out.Unreachable = append(out.Unreachable, r)
		}
	}

	answers := question != nil && question.QuestionTo != "" && question.AnsweredBy == nil &&
		(a.Role == question.QuestionTo || a.Role == api.RoleUser)
	if answers {
		if _, err := tx.Exec(`UPDATE comments SET answered_by = ? WHERE id = ?`, id, question.ID); err != nil {
			return out, err
		}
	}

	if out.Comment, err = loadComment(tx, id); err != nil {
		return out, err
	}
	if err := emit(tx, api.KindCommentCreated, t.ID, a, api.CommentCreatedData{Comment: out.Comment}); err != nil {
		return out, err
	}
	if answers {
		if err := emit(tx, api.KindQuestionAnswered, t.ID, a, api.QuestionAnsweredData{
			Ticket: int(t.ID), Question: question.ID, Answer: int(id),
		}); err != nil {
			return out, err
		}
	}
	if answers || c.questionTo != "" {
		// The ticket's open question counts changed.
		if err := b.touch(tx, a, t.ID); err != nil {
			return out, err
		}
	}
	return out, nil
}

// systemComment posts a comment by the system on ticket n that mentions the
// given roles, recording each role's live session (if any), and emits
// comment.created.
func (b *Board) systemComment(tx *sql.Tx, n int64, body string, mentions ...api.Role) error {
	_, err := b.addComment(tx, System, n, body, mentions...)
	return err
}

// addComment posts a comment by a on ticket n through the general comment
// path and returns its id. The given mentions are added to those in the
// body; a mention of the CEO while it is off goes to the user. The body of
// a system comment is not searched for mentions.
func (b *Board) addComment(tx *sql.Tx, a Actor, n int64, body string, mentions ...api.Role) (int64, error) {
	p := b.env.Policy()
	t, err := loadTicket(tx, n)
	if err != nil {
		return 0, err
	}
	c := newComment{body: body, parse: a.Role != api.RoleSystem}
	for _, m := range mentions {
		if m == "" {
			continue
		}
		if m == api.RoleCEO && !p.CEOEnabled {
			m = api.RoleUser
		}
		c.mentions = append(c.mentions, m)
	}
	res, err := b.postComment(tx, p, a, t, c)
	return int64(res.Comment.ID), err
}

// tellPM posts a system comment on t that mentions its story's product
// manager when the user changed a task inside a story by hand; the pm's own
// actions and those of other agents are not reported.
func (b *Board) tellPM(tx *sql.Tx, a Actor, t ticketRow, body string) error {
	if a.Role != api.RoleUser || !t.inStory() {
		return nil
	}
	return b.systemComment(tx, t.ID, body, api.RolePM)
}

// Comments returns the comments of ticket n, oldest first.
func (b *Board) Comments(ctx context.Context, a Actor, n int64) ([]api.Comment, error) {
	out := []api.Comment{}
	err := b.read(ctx, func(tx *sql.Tx) error {
		if err := checkRead(tx, a, n); err != nil {
			return err
		}
		if _, err := loadTicket(tx, n); err != nil {
			return err
		}
		byID := map[int]int{}
		if err := each(tx, commentSelect+` WHERE c.ticket_id = ? ORDER BY c.id`, n, func(rows *sql.Rows) error {
			c, err := scanComment(rows)
			if err != nil {
				return err
			}
			byID[c.ID] = len(out)
			out = append(out, c)
			return nil
		}); err != nil {
			return err
		}
		return each(tx, `SELECT m.comment_id, m.role, COALESCE(m.session_id, '') FROM comment_mentions m
			JOIN comments c ON c.id = m.comment_id WHERE c.ticket_id = ? ORDER BY m.comment_id, m.rowid`, n,
			func(rows *sql.Rows) error {
				var id int
				var m api.Mention
				if err := rows.Scan(&id, &m.Role, &m.Session); err != nil {
					return err
				}
				if i, ok := byID[id]; ok {
					out[i].Mentions = append(out[i].Mentions, m)
				}
				return nil
			})
	})
	return out, err
}

const commentSelect = `SELECT c.id, c.ticket_id, c.author_role, COALESCE(c.author_session, ''), COALESCE(s.name, ''),
	c.body, c.reply_to, c.question_to, c.answered_by, c.created_at
	FROM comments c LEFT JOIN agent_sessions s ON s.id = c.author_session`

// scanComment reads one comment row without its mentions; Mentions is
// empty, never nil.
func scanComment(row interface{ Scan(...any) error }) (api.Comment, error) {
	var c api.Comment
	var replyTo, answeredBy sql.NullInt64
	var created string
	if err := row.Scan(&c.ID, &c.Ticket, &c.Author.Role, &c.Author.Session, &c.Author.Name,
		&c.Body, &replyTo, &c.QuestionTo, &answeredBy, &created); err != nil {
		return c, err
	}
	if replyTo.Valid {
		v := int(replyTo.Int64)
		c.ReplyTo = &v
	}
	if answeredBy.Valid {
		v := int(answeredBy.Int64)
		c.AnsweredBy = &v
	}
	c.Mentions = []api.Mention{}
	var err error
	c.CreatedAt, err = parseTime(created)
	return c, err
}

// loadComment reads one comment with its mentions.
func loadComment(q db.Queryer, id int64) (api.Comment, error) {
	c, err := scanComment(q.QueryRow(commentSelect+` WHERE c.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, api.Errorf(api.CodeNotFound, "comment %d does not exist", id)
	}
	if err != nil {
		return c, err
	}
	err = each(q, `SELECT role, COALESCE(session_id, '') FROM comment_mentions WHERE comment_id = ? ORDER BY rowid`,
		id, func(rows *sql.Rows) error {
			var m api.Mention
			if err := rows.Scan(&m.Role, &m.Session); err != nil {
				return err
			}
			c.Mentions = append(c.Mentions, m)
			return nil
		})
	return c, err
}
