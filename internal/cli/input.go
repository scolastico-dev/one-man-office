package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func addTypeCommand(root *cobra.Command) {
	var keys []string
	cmd := &cobra.Command{
		Use:   "type <agent-name> [text]",
		Short: "Send text or special keys to an active agent terminal",
		Example: "  omo type developer-ada \"1\" --key enter\n" +
			"  omo type developer-ada --key enter\n" +
			"  omo type developer-ada --key down,down,enter\n" +
			"  omo type developer-ada --key ctrl+c",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			text := ""
			if len(args) == 2 {
				text = args[1]
			}
			if text == "" && len(keys) == 0 {
				return fmt.Errorf("text or at least one --key is required")
			}
			endpoint, agentID, err := runningOfficeCaller()
			if err != nil {
				return err
			}
			request := proto.AgentInputArgs{Name: args[0], Text: text, Keys: keys}
			if err := sockc.Call(endpoint, agentID, "agent.input", request, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "input sent to %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&keys, "key", nil,
		"special key(s), repeat or comma-separate: enter, tab, esc, arrows, home/end, page-up/down, backspace, delete, space, ctrl+a..ctrl+z")
	root.AddCommand(cmd)
}
