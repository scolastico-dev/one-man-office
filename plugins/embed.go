// Package bundledplugins owns the example plugins shipped with omo.
package bundledplugins

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	NudgeName       = "nudge"
	ToolsName       = "tools"
	FilebrowserName = "filebrowser"
	toolsMarker     = ".omo-bundled"
)

type Scope string

const (
	OfficeScope Scope = "office"
	GlobalScope Scope = "global"
)

// Definition describes the installation scope owned by a bundled plugin.
type Definition struct {
	Name  string
	Scope Scope
}

var definitions = map[string]Definition{
	NudgeName:       {Name: NudgeName, Scope: OfficeScope},
	ToolsName:       {Name: ToolsName, Scope: OfficeScope},
	FilebrowserName: {Name: FilebrowserName, Scope: GlobalScope},
}

// DefinitionFor returns the bundled plugin definition for name.
func DefinitionFor(name string) (Definition, bool) {
	definition, ok := definitions[name]
	return definition, ok
}

//go:embed nudge/* tools/* filebrowser/*
var files embed.FS

// DefaultFiles lists bundled plugin files relative to the plugin installation
// root. Callers use it to preview an explicit embedded-asset replacement.
func DefaultFiles() ([]string, error) {
	return filesForScope(nil)
}

// OfficeFiles lists only bundled assets owned by an office installation.
func OfficeFiles() ([]string, error) {
	return filesForScope(func(definition Definition) bool { return definition.Scope == OfficeScope })
}

func filesForScope(include func(Definition) bool) ([]string, error) {
	var paths []string
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			if include != nil {
				name := strings.SplitN(filepath.ToSlash(path), "/", 2)[0]
				definition, ok := DefinitionFor(name)
				if !ok || !include(definition) {
					return nil
				}
			}
			paths = append(paths, filepath.ToSlash(path))
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

// DefaultsDigest fingerprints the office-scoped bundled plugin set. Offices
// record this generation rather than hashing their editable copies, so local
// customization is not mistaken for an available bundled update.
func DefaultsDigest() (string, error) {
	h := sha256.New()
	paths, err := OfficeFiles()
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		raw, err := files.ReadFile(path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00", path)
		h.Write(raw)
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// EnsureNudge installs the bundled nudge plugin only when it is missing.
func EnsureNudge(officeDir string) (bool, error) {
	return ensure(filepath.Join(officeDir, ".omo", "plugins"), NudgeName)
}

// EnsureTools installs the bundled tools plugin only when it is missing.
func EnsureTools(officeDir string) (bool, error) {
	return ensure(filepath.Join(officeDir, ".omo", "plugins"), ToolsName)
}

// EnsureAt installs a bundled plugin below an explicit plugin root. It only
// creates a missing directory and never replaces an existing installation.
func EnsureAt(root, name string) (bool, error) {
	if _, ok := definitions[name]; !ok {
		return false, fmt.Errorf("unknown bundled plugin %q", name)
	}
	return ensure(root, name)
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
		created, err := ensure(filepath.Join(officeDir, ".omo", "plugins"), name)
		if err != nil {
			return nil, err
		}
		if created {
			installed = append(installed, name)
		}
	}
	return installed, nil
}

func ensure(root, name string) (bool, error) {
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
