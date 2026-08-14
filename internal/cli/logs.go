package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

func addLogsCommand(root *cobra.Command) {
	var lines int
	cmd := &cobra.Command{
		Use:   "logs <developer-name>",
		Short: "Print the latest lines from an active developer session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			endpoint, agentID, err := logsCaller()
			if err != nil {
				return err
			}
			var response proto.LogTailResponse
			if err := sockc.Call(endpoint, agentID, "agent.logs", proto.LogTailArgs{Name: args[0], Lines: lines}, &response); err != nil {
				return err
			}
			for _, line := range response.Lines {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "number of trailing log lines (1-10000)")
	root.AddCommand(cmd)
}

func logsCaller() (endpoint, agentID string, err error) {
	if endpoint, agentID, err = sockc.Env(); err == nil {
		return endpoint, agentID, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	endpoint, running, err := office.Running(dir)
	if err != nil {
		return "", "", err
	}
	if !running {
		return "", "", fmt.Errorf("no running omo office found at %s", filepath.Join(dir, office.LockPath))
	}
	return endpoint, "user", nil
}
