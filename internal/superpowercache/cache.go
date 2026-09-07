// Package superpowercache owns omo's shared Superpowers checkout. The cache
// lives in omo's global home, independent of the executable and offices.
package superpowercache

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/scolastico-dev/one-man-office/internal/globalhome"
)

const Repository = "https://github.com/obra/superpowers.git"

var (
	RunGit = runGit
)

func InstallDir() (string, error) {
	home, err := globalhome.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "superpowers"), nil
}

// Ensure installs the shared checkout when missing and fast-forwards an
// existing checkout. It returns the usable directory even when an update
// fails, allowing callers to warn and continue with the previous version.
func Ensure(ctx context.Context) (string, error) {
	target, err := InstallDir()
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(filepath.Join(target, ".git")); statErr == nil && info.IsDir() {
		if err := RunGit(ctx, "-C", target, "pull", "--ff-only", "--quiet"); err != nil {
			return target, fmt.Errorf("update Superpowers cache: %w", err)
		}
		return target, nil
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return target, statErr
	}
	if _, statErr := os.Stat(target); statErr == nil {
		return target, fmt.Errorf("Superpowers cache path exists but is not a Git checkout: %s", target)
	} else if !os.IsNotExist(statErr) {
		return target, statErr
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return target, err
	}
	stage, err := os.MkdirTemp(parent, ".omo-superpowers-")
	if err != nil {
		return target, fmt.Errorf("stage Superpowers cache: %w", err)
	}
	defer os.RemoveAll(stage)
	checkout := filepath.Join(stage, "checkout")
	if err := RunGit(ctx, "clone", "--depth", "1", "--quiet", Repository, checkout); err != nil {
		return target, fmt.Errorf("download Superpowers cache: %w", err)
	}
	if err := os.Rename(checkout, target); err != nil {
		if info, statErr := os.Stat(filepath.Join(target, ".git")); statErr == nil && info.IsDir() {
			return target, nil // another omo process completed the same install
		}
		return target, fmt.Errorf("install Superpowers cache: %w", err)
	}
	return target, nil
}

func runGit(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, output)
	}
	return nil
}
