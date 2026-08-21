package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func addReloadCommand(root *cobra.Command) {
	cmd := &cobra.Command{
		Use:   "reload",
		Short: "Reload the running office configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, agentID, err := runningOfficeCaller()
			if err != nil {
				return err
			}
			var response proto.ConfigReloadResponse
			if err := sockc.Call(endpoint, agentID, "office.reload", nil, &response); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "configuration reloaded: %d models, %d repositories\n", response.Models, response.Repos)
			return nil
		},
	}
	root.AddCommand(cmd)
}
