package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
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
			if sync && withGit {
				return fmt.Errorf("--sync cannot be combined with --with-git")
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
			setupOptions := office.SetupOptions{Provider: provider}
			var choices setupChoices
			var home *globalhome.Home
			configured := officeConfigExists(dir)
			if interactive && !configured {
				home, err = globalhome.Open()
				if err != nil {
					return err
				}
				recommended, err := loadRecommendedPlugins(filepath.Join(home.Dir, "known_plugins.json"))
				if err != nil {
					return err
				}
				choices, err = defaultSetupChoices(provider, detectSetupAgents(), recommended)
				if err != nil {
					return err
				}
				savedModels, savedRoles, hasSetupTemplate, err := home.LoadSetupTemplate()
				if err != nil {
					return err
				}
				if hasSetupTemplate {
					applySavedSetupChoices(&choices, savedModels, savedRoles)
				}
				askGlobal := !hasSetupTemplate && !home.Config.Template.SetupNeverAsk
				choices, err = setupWizard(cmd.InOrStdin(), cmd.OutOrStdout(), choices, askGlobal)
				if err != nil {
					return err
				}
				setupOptions = choices.officeOptions(provider)
			}
			out := cmd.OutOrStdout()
			if interactive && !configured {
				if err := installGlobalSetupPlugins(cmd.Context(), home, choices); err != nil {
					return err
				}
				if choices.SaveTemplate {
					if err := home.SaveSetupTemplate(setupOptions.Models, setupOptions.Roles); err != nil {
						return fmt.Errorf("save global setup template: %w", err)
					}
					fmt.Fprintln(out, "saved model and role choices to the global setup template")
				}
				if choices.NeverAsk {
					if err := home.SetSetupNeverAsk(true); err != nil {
						return fmt.Errorf("save setup prompt preference: %w", err)
					}
				}
			}
			created, err := office.SetupWithOptions(dir, setupOptions)
			if err != nil {
				return err
			}
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
			if interactive && !configured {
				if err := installLocalSetupPlugins(cmd.Context(), dir, choices); err != nil {
					return err
				}
			}
			if len(created) > 0 && provider == agentcli.Gemini {
				fmt.Fprintln(out, "\nWARNING: Gemini is not recommended for omo; Claude or Codex are generally more reliable and cost-effective for this workload.")
			}
			if withGit {
				if interactive {
					if home == nil {
						home, err = globalhome.Open()
						if err != nil {
							return err
						}
					}
					local, err := config.Load(filepath.Join(dir, office.ConfigPath))
					if err != nil {
						return err
					}
					missing := omitRemovedSetupPlugins(globalPluginsMissingLocally(home.Config.Plugins, local.Plugins), choices)
					if len(missing) > 0 {
						selected, err := setupGitPlugins(cmd.InOrStdin(), cmd.OutOrStdout(), missing)
						if err != nil {
							return err
						}
						if err := vendorGlobalPlugins(cmd.Context(), dir, selected); err != nil {
							return err
						}
					}
				}
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

func officeConfigExists(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(abs, office.ConfigPath))
	return err == nil
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
