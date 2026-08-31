package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/proto"
)

func addAgentVerbCommands(root *cobra.Command) {
	ready := &cobra.Command{
		Use:   "ready",
		Short: "Report alive and receive your role prompt and goal",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var r proto.ReadyResponse
			if err := call("ready", nil, &r); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), r.Prompt)
			return nil
		},
	}
	done := &cobra.Command{
		Use:   "done [result]",
		Short: "Report your goal reached; the session becomes terminatable",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			result := strings.Join(args, " ")
			if err := call("done", proto.DoneArgs{Result: result}, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "acknowledged — follow your role prompt (usually: omo wait, or simply stop)")
			return nil
		},
	}
	var waitTimeout time.Duration
	wait := &cobra.Command{
		Use:   "wait",
		Short: "Park until woken, optionally for a limited time",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if waitTimeout < 0 {
				return fmt.Errorf("--timeout must not be negative")
			}
			var w proto.WaitResponse
			args := proto.WaitArgs{TimeoutMillis: waitTimeout.Milliseconds()}
			if err := call("wait", args, &w); err != nil {
				return err
			}
			if w.Reason == "timeout" {
				fmt.Fprintln(cmd.OutOrStdout(), "wait timed out")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "woken: %s — check `omo inbox`\n", w.Reason)
			}
			return nil
		},
	}
	wait.Flags().DurationVar(&waitTimeout, "timeout", 0, "return after this duration (for example 30s or 5m; zero waits indefinitely)")
	step := &cobra.Command{
		Use:   "step <current-status>",
		Short: "Publish your current work step to agents and the TUI",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			description := strings.TrimSpace(args[0])
			if description == "" {
				return fmt.Errorf("current status must not be empty")
			}
			if err := call("step", proto.StepArgs{Description: description}, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "current step updated")
			return nil
		},
	}
	branchName := &cobra.Command{
		Use:    "branch-name <name>",
		Short:  "Return a generated branch name to the supervisor",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := call("branch.name", proto.BranchNameArgs{Name: args[0]}, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "branch name accepted")
			return nil
		},
	}
	root.AddCommand(ready, done, wait, step, branchName)
}
