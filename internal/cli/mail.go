package cli

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

// call uses the injected identity inside an agent session and falls back to
// the reserved user identity when invoked from a running office directory.
func call(verb string, args any, out any) error {
	sock, id, err := runningOfficeCaller()
	if err != nil {
		return err
	}
	return sockc.Call(sock, id, verb, args, out)
}

func addMailCommands(root *cobra.Command) {
	var to, subject, prio string
	send := &cobra.Command{
		Use:   "send [body]",
		Short: "Send a message (-t omitted = broadcast); body via argument or stdin",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := ""
			if len(args) == 1 {
				body = args[0]
			} else {
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return err
				}
				body = string(raw)
			}
			var res map[string]any
			if err := call("send", proto.SendArgs{To: to, Subject: subject, Body: body, Priority: prio}, &res); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "delivered to %v recipient(s)\n", res["delivered"])
			return nil
		},
	}
	send.Flags().StringVarP(&to, "to", "t", "", "recipient: agent name, role, or 'user' (empty = broadcast)")
	send.Flags().StringVarP(&subject, "subject", "s", "", "subject (required)")
	send.Flags().StringVarP(&prio, "priority", "p", "normal", "low|normal|high|urgent")
	send.MarkFlagRequired("subject")

	inbox := &cobra.Command{
		Use:   "inbox",
		Short: "List unread mail",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var msgs []bus.Message
			if err := call("inbox", nil, &msgs); err != nil {
				return err
			}
			if len(msgs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no unread mail")
				return nil
			}
			for _, m := range msgs {
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] (%s) from %s: %s\n", m.ID, m.Priority, m.From, m.Subject)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "read one with: omo read <id>")
			return nil
		},
	}

	read := &cobra.Command{
		Use:   "read <id>",
		Short: "Read a message (marks it read)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid message id %q", args[0])
			}
			var m bus.Message
			if err := call("read", proto.ReadArgs{ID: id}, &m); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "From: %s\nPriority: %s\nSubject: %s\n\n%s\n", m.From, m.Priority, m.Subject, m.Body)
			return nil
		},
	}

	root.AddCommand(send, inbox, read)
}
