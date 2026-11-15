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
	"bytes"
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

// PathForEnv resolves the Claude trust-state file for a profile environment.
// A custom CLAUDE_CONFIG_DIR owns its own .claude.json; otherwise Claude uses
// .claude.json directly below the effective HOME.
func PathForEnv(env map[string]string) (string, error) {
	if root := filepath.Clean(env["CLAUDE_CONFIG_DIR"]); root != "." && root != "" {
		return filepath.Join(root, ".claude.json"), nil
	}
	if home := filepath.Clean(env["HOME"]); home != "." && home != "" {
		return filepath.Join(home, ".claude.json"), nil
	}
	return DefaultPath()
}

// EnsureForEnv trusts dir in the Claude account selected by env.
func EnsureForEnv(env map[string]string, dir string) error {
	path, err := PathForEnv(env)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return EnsureIn(path, dir)
}

// EnsureIn is Ensure against an explicit config path (used by tests).
func EnsureIn(path, dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("claudetrust: %q must be absolute — claude keys projects by absolute path", dir)
	}
	trusted := func(cfg map[string]any) bool {
		projects, _ := cfg["projects"].(map[string]any)
		entry, _ := projects[dir].(map[string]any)
		accepted, _ := entry["hasTrustDialogAccepted"].(bool)
		seen, _ := entry["projectOnboardingSeenCount"].(float64)
		return accepted && seen >= 1
	}
	return update(path, trusted, func(cfg map[string]any) {
		projects, _ := cfg["projects"].(map[string]any)
		if projects == nil {
			projects = map[string]any{}
		}
		entry, _ := projects[dir].(map[string]any)
		if entry == nil {
			entry = map[string]any{}
		}
		entry["hasTrustDialogAccepted"] = true
		// A zero onboarding count shows the welcome screen, which eats the
		// start prompt just like the trust dialog does.
		if n, _ := entry["projectOnboardingSeenCount"].(float64); n < 1 {
			entry["projectOnboardingSeenCount"] = 1
		}
		projects[dir] = entry
		cfg["projects"] = projects
	})
}

// ForgetForEnv removes dir's project entry from the Claude account selected
// by env, keeping every other entry and setting. A missing file or entry is
// not an error, so removing a folder twice is harmless.
func ForgetForEnv(env map[string]string, dir string) error {
	path, err := PathForEnv(env)
	if err != nil {
		return err
	}
	return ForgetIn(path, dir)
}

// ForgetIn is ForgetForEnv against an explicit config path.
func ForgetIn(path, dir string) error {
	forgotten := func(cfg map[string]any) bool {
		projects, _ := cfg["projects"].(map[string]any)
		_, ok := projects[dir]
		return !ok
	}
	return update(path, forgotten, func(cfg map[string]any) {
		projects, _ := cfg["projects"].(map[string]any)
		delete(projects, dir)
	})
}

// updateAttempts bounds how often update starts over because the file
// changed under it.
const updateAttempts = 5

// Test hooks: run after omo prepared its new file and before it checks the
// current one, and after it replaced the file.
var beforeReplace, afterReplace func(path string)

// update applies change to the config at path unless done already reports
// the wanted state. Claude Code writes the same file and honours no lock,
// so the edit is kept as safe as it can be: omo changes only its own key on
// the freshest content, replaces the file only if it is still byte for byte
// what was read, atomically, and reads it again to verify the change is
// there; if the file changed in between, it starts over, a few times. A
// file that cannot be parsed is never written. A missing file counts as an
// empty config; once done holds for it, nothing is written.
func update(path string, done func(map[string]any) bool, change func(map[string]any)) error {
	mu.Lock()
	defer mu.Unlock()
	for attempt := 0; attempt < updateAttempts; attempt++ {
		raw, exists, err := readFile(path)
		if err != nil {
			return err
		}
		cfg, err := parse(path, raw, exists)
		if err != nil {
			return err
		}
		if done(cfg) {
			return nil
		}
		change(cfg)
		mode := os.FileMode(0o600)
		if fi, err := os.Stat(path); err == nil {
			mode = fi.Mode().Perm()
		}
		replaced, err := replaceIfUnchanged(path, cfg, mode, raw, exists)
		if err != nil {
			return err
		}
		if !replaced {
			continue
		}
		raw, exists, err = readFile(path)
		if err != nil {
			return err
		}
		after, err := parse(path, raw, exists)
		if err != nil {
			return err
		}
		if done(after) {
			return nil
		}
	}
	return fmt.Errorf("claudetrust: %s kept changing while omo updated it; gave up after %d attempts", path, updateAttempts)
}

func readFile(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func parse(path string, raw []byte, exists bool) (map[string]any, error) {
	cfg := map[string]any{}
	if !exists {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("claudetrust: %s is not valid JSON, refusing to overwrite it: %w", path, err)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return cfg, nil
}

// replaceIfUnchanged writes cfg to a temporary file next to path, then
// renames it over path only if path still holds read (or still does not
// exist). It reports whether it replaced the file.
func replaceIfUnchanged(path string, cfg map[string]any, mode os.FileMode, read []byte, existed bool) (bool, error) {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	// Write via a temp file in the same directory, then rename: a crash or a
	// concurrent reader must never see a half-written config.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claude.json.omo-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return false, err
	}
	if beforeReplace != nil {
		beforeReplace(path)
	}
	current, exists, err := readFile(path)
	if err != nil {
		return false, err
	}
	if exists != existed || !bytes.Equal(current, read) {
		return false, nil
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return false, err
	}
	if afterReplace != nil {
		afterReplace(path)
	}
	return true, nil
}
