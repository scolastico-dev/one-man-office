package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/selfupdate"
	"github.com/scolastico-dev/one-man-office/internal/service"
)

var (
	latestRelease = func(ctx context.Context) (selfupdate.Release, error) {
		return (selfupdate.Client{}).Latest(ctx)
	}
	releaseByTag = func(ctx context.Context, tag string) (selfupdate.Release, error) {
		return (selfupdate.Client{}).Release(ctx, tag)
	}
	installRelease = func(ctx context.Context, release selfupdate.Release, target string) error {
		return (selfupdate.Client{}).Install(ctx, release, target)
	}
	currentExecutable = os.Executable
)

func addUpdateCommand(root *cobra.Command, currentVersion string) {
	var check bool
	var requestedVersion string
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Install a verified omo release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if check {
				return checkForUpdate(cmd, currentVersion)
			}
			return installUpdate(cmd, currentVersion, requestedVersion)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "report whether a newer release is available without installing it")
	cmd.Flags().StringVar(&requestedVersion, "version", "", "install this exact release tag, including an older release")
	cmd.MarkFlagsMutuallyExclusive("check", "version")
	root.AddCommand(cmd)
}

func checkForUpdate(cmd *cobra.Command, currentVersion string) error {
	release, err := latestRelease(cmd.Context())
	if err != nil {
		return fmt.Errorf("check for omo updates: %w", err)
	}
	if selfupdate.IsNewer(currentVersion, release.Tag) {
		fmt.Fprintf(cmd.OutOrStdout(), "omo %s is available (running %s)\n", release.Tag, currentVersion)
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "omo %s is up to date (latest %s)\n", currentVersion, release.Tag)
	return nil
}

func installUpdate(cmd *cobra.Command, currentVersion, requestedVersion string) error {
	explicit := requestedVersion != ""
	var (
		release selfupdate.Release
		err     error
	)
	if explicit {
		release, err = releaseByTag(cmd.Context(), requestedVersion)
	} else {
		release, err = latestRelease(cmd.Context())
	}
	if err != nil {
		return fmt.Errorf("find omo release: %w", err)
	}
	if !explicit && !selfupdate.IsNewer(currentVersion, release.Tag) {
		fmt.Fprintf(cmd.OutOrStdout(), "omo %s is already up to date (latest %s)\n", currentVersion, release.Tag)
		return nil
	}
	target, err := currentExecutable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	if err := installRelease(cmd.Context(), release, target); err != nil {
		return fmt.Errorf("update: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "updated omo to %s\n", release.Tag)
	// A running server keeps the program it started with, while its agents
	// already call the new one by name.
	if _, running, _ := service.Running(cmd.Context()); running {
		fmt.Fprintln(cmd.OutOrStdout(), "omo is running with the version it started with: restart it (omo stop, then omo) to use the new one.")
	}
	return nil
}
