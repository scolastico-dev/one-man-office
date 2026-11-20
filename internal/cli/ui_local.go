package cli

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/service"
)

// The native shell uses this interface instead of parsing startup logs. Its
// output contains a private local control capability and must not be logged.
func addUILocalCommand(root *cobra.Command) {
	command := &cobra.Command{Use: "ui-local", Hidden: true, Args: cobra.NoArgs}
	command.AddCommand(&cobra.Command{
		Use: "start", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// This is intentionally noninteractive. Old installations must be
			// migrated by the normal CLI before a native launch can use them.
			if _, err := globalhome.Open(); err != nil {
				return err
			}
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			instance, err := service.StartOrAttach(cmd.Context(), executable)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(instance)
		},
	})
	root.AddCommand(command)
}
