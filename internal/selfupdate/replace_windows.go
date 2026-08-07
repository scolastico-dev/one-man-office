//go:build windows

package selfupdate

import (
	"fmt"
	"os"
)

func replaceExecutable(staged, target string) error {
	backup := target + ".previous"
	_ = os.Remove(backup)
	if err := os.Rename(target, backup); err != nil {
		return fmt.Errorf("move running executable aside: %w", err)
	}
	if err := os.Rename(staged, target); err != nil {
		_ = os.Rename(backup, target)
		return fmt.Errorf("install new executable: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}
