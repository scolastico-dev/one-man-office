package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/office"
)

func addSetupCommand(root *cobra.Command) {
	var update bool
	var agentCLI string
	cmd := &cobra.Command{
		Use:   "setup [dir]",
		Short: "Scaffold an office, or replace its editable templates with --update",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			if update {
				replaced, err := office.UpdateTemplates(dir)
				if err != nil {
					return err
				}
				for _, path := range replaced {
					fmt.Fprintln(cmd.OutOrStdout(), "replaced", path)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "template generation marker updated; config, database, logs and worktrees were not changed")
				return nil
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
			provider, detected, err := resolveSetupProvider(agentCLI, agentcli.DetectInstalled)
			if err != nil {
				return err
			}
			created, err := office.SetupWithAgentCLI(dir, provider)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(created) == 0 {
				fmt.Fprintln(out, "office already set up — nothing to do")
			} else if detected {
				fmt.Fprintf(out, "detected agent CLI: %s\n", provider)
			} else if strings.EqualFold(strings.TrimSpace(agentCLI), "auto") {
				fmt.Fprintln(out, "no supported agent CLI detected on PATH; defaulted to claude")
			}
			for _, c := range created {
				fmt.Fprintln(out, "created", c)
			}
			if len(created) > 0 && provider == agentcli.Gemini {
				fmt.Fprintln(out, "\nWARNING: Gemini is not recommended for omo; Claude or Codex are generally more reliable and cost-effective for this workload.")
			}
			fmt.Fprintf(out, "\nNext: add your repos and check the profiles in %s, then run 'omo'.\n", office.ConfigPath)
			fmt.Fprintln(out, "Try it first with fake agents and no model calls: omo --mock")
			return nil
		},
	}
	cmd.Flags().BoolVar(&update, "update", false, "replace .omo/messages and .omo/prompts with this version's defaults")
	cmd.Flags().StringVar(&agentCLI, "agent-cli", "auto", "agent CLI: auto, claude, codex, or gemini")
	root.AddCommand(cmd)
}

func resolveSetupProvider(value string, detect func() (agentcli.Provider, bool)) (agentcli.Provider, bool, error) {
	if !strings.EqualFold(strings.TrimSpace(value), "auto") {
		provider, err := agentcli.Parse(value)
		return provider, false, err
	}
	if provider, ok := detect(); ok {
		return provider, true, nil
	}
	// Preserve the historical default when setup runs before a CLI is
	// installed. --agent-cli remains available to override this choice.
	return agentcli.Claude, false, nil
}
