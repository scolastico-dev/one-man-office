// Package claudetrust pre-approves an office's working directories in the
// Claude Code config.
//
// Claude Code asks "do you trust the files in this folder?" the first time it
// runs in a directory. An omo agent has no human to answer it: the dialog
// swallows the start prompt and the agent never reaches `omo ready`. omo
// therefore records the same consent the user would give by pressing yes —
// per directory, in ~/.claude.json — before spawning.
package claudetrust

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// The config is a single file shared by every concurrent spawn.
var mu sync.Mutex

// DefaultPath is the Claude Code config location.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude.json"), nil
}

// Ensure trusts dir in the user's Claude Code config.
func Ensure(dir string) error {
	path, err := DefaultPath()
	if err != nil {
		return err
	}
	return EnsureIn(path, dir)
}

// EnsureIn is Ensure against an explicit config path (used by tests).
func EnsureIn(path, dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("claudetrust: %q must be absolute — claude keys projects by absolute path", dir)
	}
	mu.Lock()
	defer mu.Unlock()

	cfg := map[string]any{}
	mode := os.FileMode(0o600)
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return fmt.Errorf("claudetrust: %s is not valid JSON, refusing to overwrite it: %w", path, err)
		}
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	projects, _ := cfg["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
	}
	entry, _ := projects[dir].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
	}
	entry["hasTrustDialogAccepted"] = true
	// A zero onboarding count shows the welcome screen, which eats the start
	// prompt just like the trust dialog does.
	if n, _ := entry["projectOnboardingSeenCount"].(float64); n < 1 {
		entry["projectOnboardingSeenCount"] = 1
	}
	projects[dir] = entry
	cfg["projects"] = projects

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// Write via a temp file in the same directory, then rename: a crash or a
	// concurrent reader must never see a half-written config.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude.json.omo-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
