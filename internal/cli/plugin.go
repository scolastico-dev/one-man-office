package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
	"github.com/spf13/cobra"
)

func addPluginCommands(root *cobra.Command) {
	pluginCmd := &cobra.Command{Use: "plugin", Short: "Install and manage office plugins"}
	pluginCmd.AddCommand(&cobra.Command{
		Use:     "trigger <name> [-- <args>...]",
		Short:   "Run a plugin's manual hooks in the running office (user only)",
		Long:    "Run the loaded plugin's manual hooks and wait for completion. Arguments require manual_args: true in plugin.json. Run from the office directory; use the manifest name shown in the Plugins tab.",
		Example: "  omo plugin trigger report\n  omo plugin trigger report -- weekly \"two words\" --verbose",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint, caller, err := runningOfficeCaller()
			if err != nil {
				return err
			}
			if err := sockc.Call(endpoint, caller, "plugin.trigger", proto.PluginTriggerArgs{Name: args[0], Args: args[1:]}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "plugin %s completed\n", args[0])
			return nil
		},
	})
	pluginCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List configured plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, cfg, err := loadOfficeConfig()
			if err != nil {
				return err
			}
			if len(cfg.Plugins.Installed) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no managed plugins configured")
				return nil
			}
			names := make([]string, 0, len(cfg.Plugins.Installed))
			for name := range cfg.Plugins.Installed {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				entry := cfg.Plugins.Installed[name]
				state := "disabled"
				if entry.Enabled {
					state = "enabled"
				}
				extra := ""
				if entry.Subpath != "" {
					extra = " subpath=" + entry.Subpath
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s%s\n", name, state, entry.Source, extra)
			}
			return nil
		},
	})

	var installName, installSubpath string
	install := &cobra.Command{
		Use:   "install <repository-url>",
		Short: "Clone a Git-backed plugin and add it to omo.yaml",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, cfg, err := loadOfficeConfig()
			if err != nil {
				return err
			}
			source, err := pluginmanager.NormalizeSource(args[0])
			if err != nil {
				return err
			}
			subpath, err := pluginmanager.NormalizeSubpath(installSubpath)
			if err != nil {
				return err
			}
			name := installName
			if name == "" {
				name = pluginmanager.SuggestedName(source, subpath)
			}
			if err := pluginmanager.ValidateName(name); err != nil {
				return err
			}
			if _, exists := cfg.Plugins.Installed[name]; exists {
				return fmt.Errorf("plugin %q is already configured; use 'omo plugin update %s'", name, name)
			}
			entry := config.Plugin{Source: source, Subpath: subpath, Enabled: true}
			result, err := pluginmanager.Sync(cmd.Context(), cfgOfficeDir(configPath), name, entry)
			if err != nil {
				return err
			}
			if err := pluginmanager.UpsertConfig(configPath, name, entry); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "installed plugin %s at %s (restart the office to load it)\n", name, shortRevision(result.Revision))
			return nil
		},
	}
	install.Flags().StringVar(&installName, "name", "", "installed plugin name (defaults to repository or subpath name)")
	install.Flags().StringVar(&installSubpath, "subpath", "", "plugin directory inside the repository")
	pluginCmd.AddCommand(install)

	pluginCmd.AddCommand(&cobra.Command{
		Use:   "update [name]",
		Short: "Update one plugin or every configured plugin",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, cfg, err := loadOfficeConfig()
			if err != nil {
				return err
			}
			officeDir := cfgOfficeDir(configPath)
			if len(args) == 1 {
				entry, ok := cfg.Plugins.Installed[args[0]]
				if !ok {
					return fmt.Errorf("plugin %q is not configured", args[0])
				}
				result, err := pluginmanager.Sync(cmd.Context(), officeDir, args[0], entry)
				if err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "updated plugin %s at %s\n", args[0], shortRevision(result.Revision))
				return nil
			}
			results, errs := pluginmanager.SyncAll(cmd.Context(), officeDir, cfg.Plugins)
			for _, result := range results {
				fmt.Fprintf(cmd.OutOrStdout(), "updated plugin %s at %s\n", result.Name, shortRevision(result.Revision))
			}
			if len(errs) > 0 {
				return fmt.Errorf("update plugins: %w", errors.Join(errs...))
			}
			return nil
		},
	})

	pluginCmd.AddCommand(pluginToggleCommand("enable", true), pluginToggleCommand("disable", false))
	root.AddCommand(pluginCmd)
}

func pluginToggleCommand(verb string, enabled bool) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <name>",
		Short: verb + " a configured plugin without deleting it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			configPath, cfg, err := loadOfficeConfig()
			if err != nil {
				return err
			}
			if _, exists := cfg.Plugins.Installed[args[0]]; !exists {
				return fmt.Errorf("plugin %q is not configured", args[0])
			}
			if err := pluginmanager.SetEnabled(configPath, args[0], enabled); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%sd plugin %s (restart the office to apply)\n", verb, args[0])
			return nil
		},
	}
}

func cfgOfficeDir(configPath string) string {
	return filepath.Dir(filepath.Dir(configPath))
}

func shortRevision(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}
