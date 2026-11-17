package aicmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

type commentsIn struct {
	Ticket    int  `json:"ticket" arg:"0" jsonschema:"the ticket number"`
	Since     int  `json:"since,omitempty" flag:"since" help:"only comments with an id greater than this" jsonschema:"only comments with an id greater than this"`
	Questions bool `json:"questions,omitempty" flag:"questions" help:"only questions that are still open" jsonschema:"only questions that are still open"`
}

type commentIn struct {
	Ticket  int    `json:"ticket" arg:"0" jsonschema:"the ticket number"`
	ReplyTo *int   `json:"reply_to,omitempty" flag:"reply-to" help:"id of the comment this answers" jsonschema:"id of the comment this answers"`
	Body    string `json:"body" arg:"1..." text:"body-file" help:"read the text from FILE, or from standard input with -" jsonschema:"the comment (Markdown); @pm, @developer, @reviewer, @supervisor, @ceo or @user notify someone"`
}

type askIn struct {
	Ticket int    `json:"ticket" arg:"0" jsonschema:"the ticket number"`
	To     string `json:"to,omitempty" flag:"to" help:"who should answer: supervisor, pm, ceo or user" jsonschema:"who should answer: supervisor (the default), pm, ceo or user"`
	Body   string `json:"body" arg:"1..." text:"body-file" help:"read the text from FILE, or from standard input with -" jsonschema:"the question (Markdown)"`
}

func commentCommands() []Command {
	return []Command{
		Def("comments", "Read the comment thread of ticket N, oldest first", everyone, comments).help("comments N [--since ID] [--questions]",
			`Print the comments of ticket N in order, one block per comment: id, author,
time, question state, what it replies to, mentioned roles, a blank line, then
the body, indented by four spaces.
System comments (status changes) are marked "system". --since ID prints only
comments with a greater id; --questions prints only questions still open.
Use it to read what you were mentioned in or the answer to your question.`,
			"  omo ai comments 12 --since 40"),
		Def("comment", "Comment on ticket N; @pm, @developer, @reviewer, @supervisor, @ceo or @user notify someone", everyone, comment).help("comment N [--reply-to ID] [--body-file FILE|-] [TEXT...|-]",
			`Post a comment on ticket N. The text is the words after N, or a file, or
standard input with a lone - or --body-file -. Read the thread, and any
reply, with "omo ai comments N". A mention of @pm, @developer, @reviewer,
@supervisor, @ceo or @user notifies the live agent in that role; if nobody is
behind a mentioned role the comment is still stored and a line says so.`,
			`  omo ai comment 12 "@developer the retry count should be configurable"`),
		Def("ask", "Ask a question on ticket N; the answer arrives as a reply", everyone, ask).help("ask N [--to supervisor|pm|ceo|user] [--body-file FILE|-] [TEXT...|-]",
			`Post a question on ticket N to your supervisor (default), the pm, the ceo or
the user. The text is the words after N, or a file, or standard input with a
lone - or --body-file -. The question stays open until the addressed role
replies to it; read the answer with "omo ai comments N".`,
			`  omo ai ask 12 --to user "Should uploads over 2 GB be refused?"`),
	}
}

func comments(ctx context.Context, c *apiclient.Client, in commentsIn) (Result, error) {
	all, err := c.Comments(ctx, in.Ticket)
	if err != nil {
		return Result{}, err
	}
	shown := []api.Comment{}
	for _, cm := range all {
		if cm.ID <= in.Since || (in.Questions && (cm.QuestionTo == "" || cm.AnsweredBy != nil)) {
			continue
		}
		shown = append(shown, cm)
	}
	// The filters apply to the value too: the server's shape, only the
	// comments that pass.
	value, err := json.Marshal(shown)
	if err != nil {
		return Result{}, err
	}
	notes := newImageNotes(ctx, c)
	for i := range shown {
		shown[i].Body = notes.annotate(shown[i].Body)
	}
	return Result{Value: value, Text: textOf(func(w *strings.Builder) {
		if len(shown) == 0 {
			fmt.Fprintln(w, "no comments")
		}
		for i, cm := range shown {
			if i > 0 {
				fmt.Fprintln(w)
			}
			printComment(w, cm)
		}
	})}, nil
}

func comment(ctx context.Context, c *apiclient.Client, in commentIn) (Result, error) {
	return postComment(ctx, c, in.Ticket, api.CommentRequest{Body: in.Body, ReplyTo: in.ReplyTo})
}

func ask(ctx context.Context, c *apiclient.Client, in askIn) (Result, error) {
	return postComment(ctx, c, in.Ticket, api.CommentRequest{Body: in.Body, Question: true, To: in.To})
}

func postComment(ctx context.Context, c *apiclient.Client, n int, req api.CommentRequest) (Result, error) {
	res, err := c.Comment(ctx, n, req)
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) {
		kind := "comment"
		if req.Question {
			kind = "question"
		}
		fmt.Fprintf(w, "%s %d posted on #%d\n", kind, res.Comment.ID, n)
		unreachableLines(w, n, res.Unreachable)
	})), nil
}

// printComment prints one comment block.
func printComment(w io.Writer, c api.Comment) {
	author := string(c.Author.Role)
	if c.Author.Role == api.RoleSystem {
		author = "system"
	} else if c.Author.Name != "" {
		author += " " + OneLine(c.Author.Name)
	}
	fmt.Fprintf(w, "comment %d by %s at %s\n", c.ID, author, c.CreatedAt.UTC().Format("2006-01-02 15:04:05Z"))
	if c.QuestionTo != "" {
		state := "open"
		if c.AnsweredBy != nil {
			state = fmt.Sprintf("answered by comment %d", *c.AnsweredBy)
		}
		fmt.Fprintf(w, "question to %s: %s\n", c.QuestionTo, state)
	}
	if c.ReplyTo != nil {
		fmt.Fprintf(w, "reply to: comment %d\n", *c.ReplyTo)
	}
	if len(c.Mentions) > 0 {
		roles := make([]string, len(c.Mentions))
		for i, m := range c.Mentions {
			roles[i] = "@" + string(m.Role)
		}
		fmt.Fprintf(w, "mentions: %s\n", strings.Join(roles, ", "))
	}
	fmt.Fprintln(w)
	printBody(w, c.Body)
}
