package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func addAICommentCommands(ai *cobra.Command) {
	comment := &cobra.Command{
		Use:   "comment N [--reply-to ID] [--body-file FILE|-] [TEXT...]",
		Short: "Comment on ticket N; @pm, @developer, @reviewer, @ceo or @user notify someone",
		Long: `Post a comment on ticket N. The text is the words after N, or a file, or
standard input with --body-file -. An @role mention notifies the live agent in
that role; if nobody is behind a mentioned role the comment is still stored and
a line says so.`,
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
the user. The question stays open until the addressed role replies to it.`,
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
