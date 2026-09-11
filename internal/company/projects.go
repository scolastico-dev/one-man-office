// Package company owns the local browser dashboard and its child terminals.
package company

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/office"
)

type Project struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Available bool   `json:"available"`
}

func projectAt(path string) Project {
	info, err := os.Stat(filepath.Join(path, office.ConfigPath))
	canonical, canonicalErr := globalhome.CanonicalOffice(path)
	return Project{Path: path, Name: filepath.Base(path), Available: err == nil && info.Mode().IsRegular() && canonicalErr == nil && canonical == path}
}

func Projects() ([]Project, error) {
	home, err := globalhome.Open()
	if err != nil {
		return nil, err
	}
	projects := make([]Project, 0, len(home.Config.TrustedOffices))
	for _, path := range home.Config.TrustedOffices {
		projects = append(projects, projectAt(path))
	}
	return projects, nil
}

func TrustedProject(path string) (string, error) {
	canonical, err := globalhome.CanonicalOffice(path)
	if err != nil {
		return "", err
	}
	home, err := globalhome.Open()
	if err != nil {
		return "", err
	}
	if !home.IsTrusted(canonical) {
		return "", fmt.Errorf("office is not trusted: %s", canonical)
	}
	if !projectAt(canonical).Available {
		return "", fmt.Errorf("office config is missing at %s", canonical)
	}
	return canonical, nil
}

// TrustProject is an explicit dashboard approval of an existing office.
func TrustProject(path string) (Project, error) {
	canonical, err := globalhome.CanonicalOffice(path)
	if err != nil {
		return Project{}, err
	}
	project := projectAt(canonical)
	if !project.Available {
		return Project{}, fmt.Errorf("directory contains no .omo/omo.yaml")
	}
	home, err := globalhome.Open()
	if err != nil {
		return Project{}, err
	}
	if err := home.Trust(canonical); err != nil {
		return Project{}, err
	}
	return project, nil
}

// UntrustProject removes a dashboard office approval without requiring the office to exist.
func UntrustProject(path string) error {
	home, err := globalhome.Open()
	if err != nil {
		return err
	}
	return home.Untrust(path)
}

// CreateProject reserves a new directory exclusively, then scaffolds it. A clone
// source is passed as one Git argument; the executable transport is prohibited.
// Failed setup leaves its new directory for inspection, never deletes user data.
func CreateProject(ctx context.Context, destination, source string) (Project, error) {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return Project{}, fmt.Errorf("destination must be a clean absolute path")
	}
	parent, err := globalhome.CanonicalOffice(filepath.Dir(destination))
	if err != nil {
		return Project{}, fmt.Errorf("destination parent: %w", err)
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	if source != "" {
		if filepath.IsAbs(source) {
			source, err = globalhome.CanonicalOffice(source)
			if err != nil {
				return Project{}, err
			}
		} else {
			u, parseErr := url.Parse(source)
			if parseErr != nil || (u.Scheme != "https" && u.Scheme != "ssh") || u.Hostname() == "" || strings.HasPrefix(u.Hostname(), "-") || strings.ContainsAny(source, "\r\n\x00") {
				return Project{}, fmt.Errorf("clone source must be an HTTPS URL, ssh:// URL, or absolute local repository path")
			}
		}
	}
	if err := os.Mkdir(destination, 0700); err != nil {
		return Project{}, err
	}
	if source != "" {
		cmd := exec.CommandContext(ctx, "git", "-c", "protocol.ext.allow=never", "clone", "--", source, destination)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if err := cmd.Run(); err != nil {
			return Project{}, fmt.Errorf("clone failed (%v); inspect %s before retrying", err, destination)
		}
	}
	if err := validateSetupTree(destination); err != nil {
		return Project{}, err
	}
	provider, ok := agentcli.DetectInstalled()
	if !ok {
		provider = agentcli.Claude
	}
	if _, err := office.SetupWithAgentCLI(destination, provider); err != nil {
		return Project{}, fmt.Errorf("setup failed: %w; inspect %s before retrying", err, destination)
	}
	return TrustProject(destination)
}

// A cloned office must not redirect the scaffolder's writes outside its newly
// reserved destination. Project symlinks outside .omo are unrelated to setup.
func validateSetupTree(officeDir string) error {
	root := filepath.Join(officeDir, ".omo")
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root && os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("office setup rejects symbolic links and special files under .omo: %s; inspect the clone before retrying", path)
		}
		return nil
	})
}
