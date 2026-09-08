package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/office"
)

func addSetupCommand(root *cobra.Command) {
	var update bool
	var sync bool
	var agentCLI string
	var nonInteractive bool
	var withGit bool
	cmd := &cobra.Command{
		Use:   "setup [dir]",
		Short: "Scaffold an office, or replace its embedded assets with --update",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			if update && sync {
				return fmt.Errorf("--sync and --update cannot be combined")
			}
			if sync && !strings.EqualFold(strings.TrimSpace(agentCLI), "auto") {
				return fmt.Errorf("--sync cannot be combined with --agent-cli")
			}
			if update && withGit {
				return fmt.Errorf("--with-git cannot be combined with --update")
			}
			if update {
				replaced, err := office.UpdateTemplatesWithPreview(dir, func(path string) {
					fmt.Fprintln(cmd.OutOrStdout(), "will update", path)
				})
				if err != nil {
					return err
				}
				for _, path := range replaced {
					fmt.Fprintln(cmd.OutOrStdout(), "replaced", path)
				}
				fmt.Fprintln(cmd.OutOrStdout(), "embedded asset generation marker updated; config, database, logs and worktrees were not changed")
				return nil
			}
			if sync {
				updated, err := office.SyncTemplateConfig(dir)
				if err != nil {
					return err
				}
				if len(updated) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "no global partial config override to sync")
				} else {
					fmt.Fprintln(cmd.OutOrStdout(), "synced", office.ConfigPath)
				}
				return nil
			}
			interactive := !nonInteractive && inputIsTerminal(cmd.InOrStdin())
			provider, detected, err := resolveSetupProvider(agentCLI, agentcli.DetectInstalled)
			if err != nil {
				return err
			}
			if interactive && strings.EqualFold(strings.TrimSpace(agentCLI), "auto") {
				provider, err = chooseSetupProvider(cmd.InOrStdin(), cmd.OutOrStdout(), provider)
				if err != nil {
					return err
				}
				detected = false
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
			if withGit {
				changed, err := office.EnableGitIntegration(dir)
				if err != nil {
					return err
				}
				for _, path := range changed {
					fmt.Fprintln(out, "git-enabled", path)
				}
			}
			fmt.Fprintf(out, "\nNext: add your repos and check the profiles in %s, then run 'omo'.\n", office.ConfigPath)
			fmt.Fprintln(out, "Try it first with fake agents and no model calls: omo --mock")
			return nil
		},
	}
	cmd.Flags().BoolVar(&update, "update", false, "replace editable templates and bundled plugins")
	cmd.Flags().BoolVar(&sync, "sync", false, "reapply the global partial .omo/omo.yaml override")
	cmd.Flags().StringVar(&agentCLI, "agent-cli", "auto", "agent CLI: auto, claude, codex, or gemini")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "use defaults without setup prompts (recommended for CI)")
	cmd.Flags().BoolVar(&withGit, "with-git", false, "enable relative paths and commit-ready office handoff files")
	root.AddCommand(cmd)
}

func chooseSetupProvider(input io.Reader, output io.Writer, detected agentcli.Provider) (agentcli.Provider, error) {
	if detected == "" {
		detected = agentcli.Claude
	}
	fmt.Fprintln(output, "\nOMO setup — choose the default agent CLI")
	fmt.Fprintln(output, "  [x] ceo, product_manager, developer, reviewer, freelancer, smokealarm, firefighter")
	fmt.Fprintln(output, "      assignment: detected/default profile (round-robin where multiple profiles exist)")
	fmt.Fprintln(output, "  [x] bundled plugins: nudge, tools")
	fmt.Fprintln(output, "      recommended plugins: review OMO_HOME/known_plugins.json first; plugins have CLI access")
	fmt.Fprintf(output, "  provider [claude/codex/gemini] (default %s): ", detected)
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && len(line) == 0 {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return detected, nil
	}
	return agentcli.Parse(line)
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
