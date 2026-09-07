package cli

import (
	"bufio"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/spf13/cobra"
)

func ensureOfficeTrust(cmd *cobra.Command, dir string, explicit bool) (string, error) {
	canonical, err := globalhome.CanonicalOffice(dir)
	if err != nil {
		return "", err
	}
	home, err := globalhome.Open()
	if err != nil {
		return "", err
	}
	if home.IsTrusted(canonical) {
		return canonical, nil
	}
	if !explicit {
		if !inputIsTerminal(cmd.InOrStdin()) {
			return "", fmt.Errorf("office %s is not trusted; launch interactively to approve it or use --trust-office to explicitly trust this location", canonical)
		}
		question := fmt.Sprintf("Trust office %s? Its configuration and plugins can run commands with your user permissions.", canonical)
		if !askYesNo(bufio.NewReader(cmd.InOrStdin()), cmd.OutOrStdout(), question) {
			return "", fmt.Errorf("office %s is not trusted; startup cancelled", canonical)
		}
	}
	if err := home.Trust(canonical); err != nil {
		return "", fmt.Errorf("save office trust: %w", err)
	}
	return canonical, nil
}
