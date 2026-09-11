// Package company owns the local browser dashboard and its child terminals.
package company

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"

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

// ValidateProjectRequest checks a dashboard create or clone before any child is
// launched. It returns the canonical destination and its canonical parent.
func ValidateProjectRequest(destination, source string, clone bool) (canonical, parent, validatedSource string, err error) {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return "", "", "", fmt.Errorf("destination must be a clean absolute path")
	}
	parent, err = globalhome.CanonicalOffice(filepath.Dir(destination))
	if err != nil {
		return "", "", "", fmt.Errorf("destination parent: %w", err)
	}
	destination = filepath.Join(parent, filepath.Base(destination))
	canonical = destination
	destinationExists := false
	if entry, statErr := os.Lstat(destination); statErr == nil {
		destinationExists = true
		if entry.Mode()&os.ModeSymlink != 0 {
			return "", "", "", fmt.Errorf("destination must be a directory")
		}
		info, statErr := os.Stat(destination)
		if statErr != nil {
			return "", "", "", statErr
		}
		if !info.IsDir() {
			return "", "", "", fmt.Errorf("destination must be a directory")
		}
		canonical, err = globalhome.CanonicalOffice(destination)
		if err != nil {
			return "", "", "", err
		}
	} else if !os.IsNotExist(statErr) {
		return "", "", "", statErr
	}
	if _, statErr := os.Lstat(filepath.Join(canonical, office.ConfigPath)); statErr == nil {
		return "", "", "", fmt.Errorf("destination already contains %s; use Load and trust", office.ConfigPath)
	} else if !os.IsNotExist(statErr) {
		return "", "", "", statErr
	}
	if clone && destinationExists {
		entries, readErr := os.ReadDir(canonical)
		if readErr != nil {
			return "", "", "", readErr
		}
		if len(entries) != 0 {
			return "", "", "", fmt.Errorf("clone destination must be absent or an empty directory")
		}
	}
	if err := validateSetupTree(canonical); err != nil {
		return "", "", "", err
	}
	if source == "" {
		if clone {
			return "", "", "", fmt.Errorf("clone source required")
		}
		return canonical, parent, "", nil
	}
	if !clone {
		return "", "", "", fmt.Errorf("create cannot include source")
	}
	if strings.ContainsAny(source, "\r\n\x00") {
		return "", "", "", fmt.Errorf("clone source must be an HTTPS URL, ssh:// URL, or absolute local repository path")
	}
	if source != "" {
		if filepath.IsAbs(source) {
			source, err = globalhome.CanonicalOffice(source)
			if err != nil {
				return "", "", "", err
			}
		} else {
			u, parseErr := url.Parse(source)
			if parseErr != nil || (u.Scheme != "https" && u.Scheme != "ssh") || u.Hostname() == "" || strings.HasPrefix(u.Hostname(), "-") || strings.ContainsAny(source, "\r\n\x00") {
				return "", "", "", fmt.Errorf("clone source must be an HTTPS URL, ssh:// URL, or absolute local repository path")
			}
		}
	}
	return canonical, parent, source, nil
}

// ValidateSetupTree is used by the hidden project helper after cloning and
// before setup can write into the destination.
func ValidateSetupTree(officeDir string) error { return validateSetupTree(officeDir) }

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
