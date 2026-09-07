package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/selfupdate"
)

var (
	releaseByTag = func(ctx context.Context, tag string) (selfupdate.Release, error) {
		return (selfupdate.Client{}).Release(ctx, tag)
	}
	findOfficeConfigForSelfUpdate   = findOfficeConfigForSelfUpdateFromWorkingDirectory
	setCheckSelfUpdateForSelfUpdate = config.SetCheckSelfUpdate
)

func addSelfUpdateCommand(root *cobra.Command, currentVersion string) {
	var check bool
	var requestedVersion string
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Install a verified omo release without starting an office",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if check {
				return checkForSelfUpdate(cmd, currentVersion)
			}
			return installSelfUpdate(cmd, currentVersion, requestedVersion)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "report whether a newer release is available without installing it")
	cmd.Flags().StringVar(&requestedVersion, "version", "", "install this exact release tag, including an older release")
	cmd.MarkFlagsMutuallyExclusive("check", "version")
	root.AddCommand(cmd)
}

func checkForSelfUpdate(cmd *cobra.Command, currentVersion string) error {
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

func installSelfUpdate(cmd *cobra.Command, currentVersion, requestedVersion string) error {
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
	configPath := ""
	if explicit {
		var found bool
		configPath, found, err = findOfficeConfigForSelfUpdate()
		if err != nil {
			return fmt.Errorf("find office configuration before update: %w", err)
		}
		if found {
			if err := setCheckSelfUpdateForSelfUpdate(configPath, false); err != nil {
				return fmt.Errorf("disable automatic self-update checks in %s: %w", configPath, err)
			}
		}
	}
	if err := installRelease(cmd.Context(), release, target); err != nil {
		if configPath != "" {
			return fmt.Errorf("self-update: %w; automatic self-update checks are disabled in %s", err, configPath)
		}
		return fmt.Errorf("self-update: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "updated omo to %s\n", release.Tag)
	if !explicit {
		return nil
	}
	if configPath == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "no office setting was changed (no .omo/omo.yaml found)")
		return nil
	}
	fmt.Fprintf(cmd.OutOrStdout(), "disabled automatic self-update checks in %s\n", configPath)
	return nil
}

func findOfficeConfigForSelfUpdateFromWorkingDirectory() (string, bool, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false, err
	}
	for {
		path := filepath.Join(dir, office.ConfigPath)
		info, err := os.Stat(path)
		if err == nil {
			if info.IsDir() {
				return "", false, fmt.Errorf("%s is a directory", path)
			}
			return path, true, nil
		}
		if !os.IsNotExist(err) {
			return "", false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}
