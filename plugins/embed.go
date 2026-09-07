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

const (
	NudgeName   = "nudge"
	ToolsName   = "tools"
	toolsMarker = ".omo-bundled"
)

//go:embed nudge/* tools/*
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

// EnsureNudge installs the bundled nudge plugin only when it is missing.
func EnsureNudge(officeDir string) (bool, error) {
	return ensure(officeDir, NudgeName)
}

// EnsureTools installs the bundled tools plugin only when it is missing.
func EnsureTools(officeDir string) (bool, error) {
	return ensure(officeDir, ToolsName)
}

// ToolsBundled reports whether tools was installed by omo rather than being an
// unrelated local plugin that happens to use the same name.
func ToolsBundled(officeDir string) bool {
	raw, err := os.ReadFile(filepath.Join(officeDir, ".omo", "plugins", ToolsName, toolsMarker))
	return err == nil && string(raw) == "builtin:tools\n"
}

// EnsureDefaults installs each missing bundled plugin without overwriting local
// edits. The explicit setup update flow stages and replaces them separately.
func EnsureDefaults(officeDir string) ([]string, error) {
	var installed []string
	for _, name := range []string{NudgeName, ToolsName} {
		created, err := ensure(officeDir, name)
		if err != nil {
			return nil, err
		}
		if created {
			installed = append(installed, name)
		}
	}
	return installed, nil
}

func ensure(officeDir, name string) (bool, error) {
	root := filepath.Join(officeDir, ".omo", "plugins")
	target := filepath.Join(root, name)
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
	stage, err := os.MkdirTemp(root, ".builtin-"+name+"-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(stage)
	if err := fs.WalkDir(files, name, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(name, path)
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
	if name == ToolsName {
		if err := os.WriteFile(filepath.Join(stage, toolsMarker), []byte("builtin:tools\n"), 0o644); err != nil {
			return false, err
		}
	}
	if err := os.Rename(stage, target); err != nil {
		return false, err
	}
	return true, nil
}
