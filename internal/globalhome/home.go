// Package globalhome owns the user-wide omo home folder: where it is, what it
// contains, and the atomic writes used for the files inside it.
package globalhome

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// ErrOldInstallation is returned (wrapped) by Open when the home folder holds
// files of an older omo.
var ErrOldInstallation = errors.New("old omo installation")

const (
	markerName    = "layout.json"
	markerContent = "{\"layout\":2}\n"
)

// Layout holds the absolute paths of everything inside the omo home.
type Layout struct {
	Root         string
	Settings     string // settings.yaml
	Projects     string
	Profiles     string
	Roles        string
	Prompts      string
	Usage        string
	SkillSets    string
	Plugins      string
	DB           string // omo.db
	Run          string
	Worktrees    string
	CEO          string
	Logs         string
	ServerConfig string // server.yaml
	Token        string // token
}

// Dir resolves the omo home without creating files. OMO_HOME must be absolute.
func Dir() (string, error) {
	user, err := os.UserHomeDir()
	if err != nil && os.Getenv("OMO_HOME") == "" && runtime.GOOS != "windows" {
		return "", err
	}
	return resolveDir(runtime.GOOS, user, os.Getenv("APPDATA"), os.Getenv("OMO_HOME"))
}

func resolveDir(platform, user, app, override string) (string, error) {
	if override != "" {
		if !filepath.IsAbs(override) {
			return "", fmt.Errorf("OMO_HOME must be an absolute path")
		}
		return filepath.Clean(override), nil
	}
	if platform == "windows" {
		if app == "" || !filepath.IsAbs(app) {
			return "", fmt.Errorf("APPDATA must be an absolute path")
		}
		return filepath.Join(app, "omo"), nil
	}
	if user == "" || !filepath.IsAbs(user) {
		return "", fmt.Errorf("user home must be an absolute path")
	}
	return filepath.Join(user, ".local", "omo"), nil
}

func layoutFor(root string) Layout {
	return Layout{
		Root:         root,
		Settings:     filepath.Join(root, "settings.yaml"),
		Projects:     filepath.Join(root, "projects"),
		Profiles:     filepath.Join(root, "profiles"),
		Roles:        filepath.Join(root, "roles"),
		Prompts:      filepath.Join(root, "prompts"),
		Usage:        filepath.Join(root, "usage"),
		SkillSets:    filepath.Join(root, "skillsets"),
		Plugins:      filepath.Join(root, "plugins"),
		DB:           filepath.Join(root, "omo.db"),
		Run:          filepath.Join(root, "run"),
		Worktrees:    filepath.Join(root, "worktrees"),
		CEO:          filepath.Join(root, "ceo"),
		Logs:         filepath.Join(root, "logs"),
		ServerConfig: filepath.Join(root, "server.yaml"),
		Token:        filepath.Join(root, "token"),
	}
}

// Open creates the home tree and writes the layout marker on first use. It
// fails with ErrOldInstallation when the folder is not empty and has no marker.
func Open() (Layout, error) {
	root, err := Dir()
	if err != nil {
		return Layout{}, err
	}
	l := layoutFor(root)
	if err := checkMarker(root); err != nil {
		return Layout{}, err
	}
	for _, dir := range []string{l.Root, l.Run} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return Layout{}, err
		}
		if err := os.Chmod(dir, 0700); err != nil && runtime.GOOS != "windows" {
			return Layout{}, err
		}
		if err := protectFile(dir); err != nil {
			return Layout{}, err
		}
	}
	for _, dir := range []string{l.Projects, l.Profiles, l.Roles, l.Prompts, l.Usage, l.SkillSets, l.Plugins, l.Worktrees, l.CEO, l.Logs} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return Layout{}, err
		}
	}
	marker := filepath.Join(root, markerName)
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		if err := WriteFileAtomic(marker, []byte(markerContent), 0600); err != nil {
			return Layout{}, err
		}
	} else if err != nil {
		return Layout{}, err
	}
	return l, nil
}

func checkMarker(root string) error {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := os.Stat(filepath.Join(root, markerName)); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return oldInstallationError{root}
}

// oldInstallationError carries the user-facing message and matches
// ErrOldInstallation with errors.Is.
type oldInstallationError struct{ root string }

func (e oldInstallationError) Error() string {
	return fmt.Sprintf("omo: %s holds an installation of an older omo. This version cannot use it yet; set OMO_HOME to another folder.", e.root)
}

func (e oldInstallationError) Is(target error) bool { return target == ErrOldInstallation }
