package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func addAICommentCommands(ai *cobra.Command) {
	comments := &cobra.Command{
		Use:   "comments N [--since ID] [--questions]",
		Short: "Read the comment thread of ticket N, oldest first",
		Long: `Print the comments of ticket N in order, one block per comment: id, author,
time, question state, what it replies to, mentioned roles, a blank line, then
the body, indented by four spaces.
System comments (status changes) are marked "system". --since ID prints only
comments with a greater id; --questions prints only questions still open.
Use it to read what you were mentioned in or the answer to your question.`,
		Example: "  omo ai comments 12 --since 40",
		Args:    cobra.ExactArgs(1),
		RunE: aiRun(func(a *aiCall, args []string) error {
			n, err := ticketNumber(args[0])
			if err != nil {
				return err
			}
			flags := a.cmd.Flags()
			since, _ := flags.GetInt("since")
			onlyOpen, _ := flags.GetBool("questions")
			all, err := a.client.Comments(a.ctx, n)
			if err != nil {
				return err
			}
			shown := []api.Comment{}
			for _, c := range all {
				if c.ID <= since || (onlyOpen && (c.QuestionTo == "" || c.AnsweredBy != nil)) {
					continue
				}
				shown = append(shown, c)
			}
			if a.json {
				// The filters apply here too: the server's shape, only the
				// comments that pass.
				raw, err := json.Marshal(shown)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(a.out, string(raw))
				return err
			}
			return a.emit(func(w io.Writer) {
				if len(shown) == 0 {
					fmt.Fprintln(w, "no comments")
				}
				for i, c := range shown {
					if i > 0 {
						fmt.Fprintln(w)
					}
					printComment(w, c)
				}
			})
		}),
	}
	comments.Flags().Int("since", 0, "only comments with an id greater than this")
	comments.Flags().Bool("questions", false, "only questions that are still open")
	ai.AddCommand(comments)

	comment := &cobra.Command{
		Use:   "comment N [--reply-to ID] [--body-file FILE|-] [TEXT...]",
		Short: "Comment on ticket N; @pm, @developer, @reviewer, @supervisor, @ceo or @user notify someone",
		Long: `Post a comment on ticket N. The text is the words after N, or a file, or
standard input with --body-file -. Read the thread, and any reply, with
"omo ai comments N". A mention of @pm, @developer, @reviewer, @supervisor,
@ceo or @user notifies the live agent in that role; if nobody is behind a
mentioned role the comment is still stored and a line says so.`,
		Example: `  omo ai comment 12 "@developer the retry count should be configurable"`,
		Args:    cobra.MinimumNArgs(1),
		RunE: aiRun(func(a *aiCall, args []string) error {
			return postComment(a, args, false)
		}),
	}
	comment.Flags().Int("reply-to", 0, "id of the comment this answers")
	comment.Flags().String("body-file", "", "read the text from FILE, or from standard input with -")
	ai.AddCommand(comment)

	ask := &cobra.Command{
		Use:   "ask N [--to supervisor|pm|ceo|user] [--body-file FILE|-] [TEXT...]",
		Short: "Ask a question on ticket N; the answer arrives as a reply",
		Long: `Post a question on ticket N to your supervisor (default), the pm, the ceo or
the user. The question stays open until the addressed role replies to it;
read the answer with "omo ai comments N".`,
		Example: `  omo ai ask 12 --to user "Should uploads over 2 GB be refused?"`,
		Args:    cobra.MinimumNArgs(1),
		RunE: aiRun(func(a *aiCall, args []string) error {
			return postComment(a, args, true)
		}),
	}
	ask.Flags().String("to", "", "who should answer: supervisor, pm, ceo or user")
	ask.Flags().String("body-file", "", "read the text from FILE, or from standard input with -")
	ai.AddCommand(ask)
}

func postComment(a *aiCall, args []string, question bool) error {
	n, err := ticketNumber(args[0])
	if err != nil {
		return err
	}
	flags := a.cmd.Flags()
	file, _ := flags.GetString("body-file")
	body, err := readBody(a.cmd, "body-file", file, args[1:], false)
	if err != nil {
		return err
	}
	req := api.CommentRequest{Body: body, Question: question}
	if flags.Changed("reply-to") {
		id, _ := flags.GetInt("reply-to")
		req.ReplyTo = &id
	}
	if question {
		req.To, _ = flags.GetString("to")
	}
	res, err := a.client.Comment(a.ctx, n, req)
	if err != nil {
		return err
	}
	return a.emit(func(w io.Writer) {
		kind := "comment"
		if question {
			kind = "question"
		}
		fmt.Fprintf(w, "%s %d posted on #%d\n", kind, res.Comment.ID, n)
		unreachableLines(w, n, res.Unreachable)
	})
}

// printComment prints one comment block.
func printComment(w io.Writer, c api.Comment) {
	author := string(c.Author.Role)
	if c.Author.Role == api.RoleSystem {
		author = "system"
	} else if c.Author.Name != "" {
		author += " " + oneLine(c.Author.Name)
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
