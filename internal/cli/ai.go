package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

// aiCall is the state of one `omo ai` invocation.
type aiCall struct {
	ctx    context.Context
	cmd    *cobra.Command
	client *apiclient.Client
	out    io.Writer
	json   bool
}

// emit prints the last response body as JSON with --json, otherwise the text
// form produced by text.
func (a *aiCall) emit(text func(w io.Writer)) error {
	if a.json {
		body := strings.TrimRight(string(a.client.LastBody()), "\n")
		_, err := fmt.Fprintln(a.out, body)
		return err
	}
	text(a.out)
	return nil
}

// aiRun wraps a verb: it connects to the running server (never starting one)
// and turns server errors into one plain line.
func aiRun(fn func(a *aiCall, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		client, err := apiclient.FromEnvironment()
		if err != nil {
			return err
		}
		asJSON, _ := cmd.Flags().GetBool("json")
		a := &aiCall{ctx: cmd.Context(), cmd: cmd, client: client, out: cmd.OutOrStdout(), json: asJSON}
		if a.ctx == nil {
			a.ctx = context.Background()
		}
		return explain(fn(a, args))
	}
}

// explain reduces an error to the server's message plus its code.
func explain(err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%s (%s)", apiErr.Message, apiErr.Code)
	}
	return err
}

// ticketNumber parses "12" or "#12".
func ticketNumber(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(s), "#"))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%q is not a ticket number (use for example 12 or #12)", s)
	}
	return n, nil
}

// readBody returns the text from a file ("-" is standard input) or, when no
// file is given, from the joined words. Giving both is an error; so is empty
// text unless allowEmpty, and text that is not valid UTF-8. The text is kept
// as it is except that line endings become \n and whitespace at the very end
// is removed. Its size is checked by the server, which refuses a request
// body over its limit with too_large.
func readBody(cmd *cobra.Command, fileFlag, file string, words []string, allowEmpty bool) (string, error) {
	var text string
	switch {
	case file != "" && len(words) > 0:
		return "", fmt.Errorf("give the text either as words or with --%s, not both", fileFlag)
	case file == "-":
		raw, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("read standard input: %w", err)
		}
		text = string(raw)
	case file != "":
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read --%s: %w", fileFlag, errors.Unwrap(err))
		}
		text = string(raw)
	default:
		text = strings.Join(words, " ")
	}
	if !utf8.ValidString(text) {
		source := "the text"
		if file == "-" {
			source = "standard input"
		} else if file != "" {
			source = "--" + fileFlag + " " + file
		}
		return "", fmt.Errorf("%s is not valid UTF-8 text", source)
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimRightFunc(text, unicode.IsSpace)
	if strings.TrimSpace(text) == "" && !allowEmpty {
		return "", fmt.Errorf("the text is empty: give words or --%s FILE (use - for standard input)", fileFlag)
	}
	return text, nil
}

const aiLong = `The commands agents use to work on the omo board: read tickets, create and
update them, link dependencies, comment, ask questions and hand in work.
Every command talks to the running omo server over its local socket and never
starts one. Without OMO_AGENT_ID you act as the user; with it, as that agent.

Ticket numbers are written 12 or #12. Text arguments (descriptions, comments)
can be given as words, from a file, or from standard input with "-".
Text must be UTF-8; line endings become \n and whitespace at the very end is
dropped, everything else is sent as it is.
Add --json to any command for the server's raw JSON answer. An error prints
one line and exits with status 1.

Text written by people or agents (descriptions, comments) is indented by four
spaces; lines at the left margin come from omo.`

const aiExample = `  omo ai whoami
  omo ai ticket list --column ready
  omo ai ticket show 12
  omo ai comments 12
  omo ai comment 12 "@developer please check the failing test"`

// addAICommand registers the `omo ai` group.
func addAICommand(root *cobra.Command) {
	ai := &cobra.Command{
		Use:     "ai",
		Short:   "Commands for agents working on the board",
		Long:    aiLong,
		Example: aiExample,
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	ai.PersistentFlags().Bool("json", false, "print the server's JSON answer instead of text")
	addAITicketCommands(ai)
	addAICommentCommands(ai)
	addAIMiscCommands(ai)
	root.AddCommand(ai)
}
