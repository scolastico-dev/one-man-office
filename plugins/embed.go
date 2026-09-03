// Package bundledplugins owns the example plugins shipped with omo.
package bundledplugins

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const NudgeName = "nudge"

//go:embed nudge/*
var files embed.FS

// DefaultsDigest fingerprints every file in the bundled plugin set. Offices
// record this generation rather than hashing their editable copies, so local
// customization is not mistaken for an available bundled update.
func DefaultsDigest() (string, error) {
	h := sha256.New()
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s\x00", path)
		h.Write(raw)
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// EnsureNudge installs the bundled nudge plugin only when it is missing. User
// edits are preserved by ordinary setup and startup; the explicit setup
// update flow stages and replaces bundled plugins separately.
func EnsureNudge(officeDir string) (bool, error) {
	root := filepath.Join(officeDir, ".omo", "plugins")
	target := filepath.Join(root, NudgeName)
	if info, err := os.Stat(target); err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("%s exists but is not a directory", target)
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return false, err
	}
	stage, err := os.MkdirTemp(root, ".builtin-nudge-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(stage)
	if err := fs.WalkDir(files, "nudge", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel("nudge", path)
		if err != nil || rel == "." {
			return err
		}
		dest := filepath.Join(stage, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		raw, err := files.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, raw, 0o644)
	}); err != nil {
		return false, err
	}
	if err := os.Rename(stage, target); err != nil {
		return false, err
	}
	return true, nil
}
