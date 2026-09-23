// Package bundledplugins owns the example plugins shipped with omo.
package bundledplugins

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
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
	MarkerName      = ".omo-bundled"
	toolsMarker     = MarkerName
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

// Marker records the embedded source and content generation of an owned
// bundled plugin installation.
type Marker struct {
	Source string
	Digest string
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

// PluginDigest fingerprints one embedded plugin using sorted relative paths
// and path-NUL/content-NUL framing.
func PluginDigest(name string) (string, error) {
	if _, ok := definitions[name]; !ok {
		return "", fmt.Errorf("unknown bundled plugin %q", name)
	}
	h := sha256.New()
	paths, err := pluginFiles(name)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		raw, err := files.ReadFile(name + "/" + path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00", path)
		h.Write(raw)
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// ReadMarker reads a strict source=<source>, digest=<sha256> marker.
func ReadMarker(path string) (Marker, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Marker{}, err
	}
	lines := strings.Split(string(raw), "\n")
	if len(lines) != 3 || lines[2] != "" || !strings.HasPrefix(lines[0], "source=") || !strings.HasPrefix(lines[1], "digest=") {
		return Marker{}, fmt.Errorf("malformed bundled marker")
	}
	marker := Marker{Source: strings.TrimPrefix(lines[0], "source="), Digest: strings.TrimPrefix(lines[1], "digest=")}
	if !strings.HasPrefix(marker.Source, "builtin:") || len(marker.Source) == len("builtin:") || strings.ContainsAny(marker.Source, "=\\/\x00\t \r\n") {
		return Marker{}, fmt.Errorf("malformed bundled marker source")
	}
	if len(marker.Digest) != sha256.Size*2 || marker.Digest != strings.ToLower(marker.Digest) {
		return Marker{}, fmt.Errorf("malformed bundled marker digest")
	}
	decoded, err := hex.DecodeString(marker.Digest)
	if err != nil || len(decoded) != sha256.Size {
		return Marker{}, fmt.Errorf("malformed bundled marker digest")
	}
	return marker, nil
}

// EnsureNudge installs the bundled nudge plugin only when it is missing.
func EnsureNudge(officeDir string) (bool, error) {
	return ensure(filepath.Join(officeDir, ".omo", "plugins"), NudgeName)
}

// EnsureTools installs the bundled tools plugin only when it is missing.
func EnsureTools(officeDir string) (bool, error) {
	return ensure(filepath.Join(officeDir, ".omo", "plugins"), ToolsName)
}

// EnsureAt installs or refreshes an owned global bundled plugin below an
// explicit plugin root. Existing markerless directories remain untouched.
func EnsureAt(root, name string) (bool, error) {
	if _, ok := definitions[name]; !ok {
		return false, fmt.Errorf("unknown bundled plugin %q", name)
	}
	if definitions[name].Scope == GlobalScope {
		return ensureGlobal(root, name, false, nil)
	}
	return ensure(root, name)
}

// EnsureOwnedAt refreshes a markerless global bundled plugin once when the
// caller has an explicit builtin configuration record authorizing adoption.
func EnsureOwnedAt(root, name string) (bool, error) {
	definition, ok := definitions[name]
	if !ok || definition.Scope != GlobalScope {
		return false, fmt.Errorf("unknown global bundled plugin %q", name)
	}
	return ensureGlobal(root, name, true, nil)
}

// EnsureAtWithCommit activates an owned global bundle and invokes commit while
// the previous active copy is still recoverable. A failed commit restores the
// previous copy (or removes a newly created copy).
func EnsureAtWithCommit(root, name string, adoptMarkerless bool, commit func() error) (bool, error) {
	definition, ok := definitions[name]
	if !ok || definition.Scope != GlobalScope {
		return false, fmt.Errorf("unknown global bundled plugin %q", name)
	}
	return ensureGlobal(root, name, adoptMarkerless, commit)
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
	if err := copyEmbedded(stage, name); err != nil {
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

func pluginFiles(name string) ([]string, error) {
	var paths []string
	err := fs.WalkDir(files, name, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			paths = append(paths, strings.TrimPrefix(filepath.ToSlash(path), name+"/"))
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func copyEmbedded(stage, name string) error {
	return fs.WalkDir(files, name, func(path string, entry fs.DirEntry, walkErr error) error {
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
	})
}

func ensureGlobal(root, name string, adoptMarkerless bool, commit func() error) (bool, error) {
	digest, err := PluginDigest(name)
	if err != nil {
		return false, err
	}
	target := filepath.Join(root, name)
	info, err := os.Stat(target)
	if err == nil && !info.IsDir() {
		return false, fmt.Errorf("%s exists but is not a directory", target)
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if os.IsNotExist(err) {
		return stageAndActivate(root, name, digest, false, commit)
	}
	marker, err := ReadMarker(filepath.Join(target, MarkerName))
	if err == nil {
		if marker.Source != "builtin:"+name || marker.Digest == digest {
			if commit == nil {
				return false, nil
			}
			return false, commit()
		}
		return stageAndActivate(root, name, digest, true, commit)
	}
	if adoptMarkerless && errors.Is(err, fs.ErrNotExist) {
		return stageAndActivate(root, name, digest, true, commit)
	}
	if commit != nil {
		return false, commit()
	}
	return false, nil
}

func stageAndActivate(root, name, digest string, replace bool, commit func() error) (bool, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return false, err
	}
	stage, err := os.MkdirTemp(root, ".builtin-"+name+"-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(stage)
	if err := copyEmbedded(stage, name); err != nil {
		return false, err
	}
	if err := os.WriteFile(filepath.Join(stage, MarkerName), []byte("source=builtin:"+name+"\ndigest="+digest+"\n"), 0o644); err != nil {
		return false, err
	}
	if !replace {
		target := filepath.Join(root, name)
		if err := os.Rename(stage, target); err != nil {
			return false, err
		}
		if commit != nil {
			if err := commit(); err != nil {
				return false, errors.Join(err, os.RemoveAll(target))
			}
		}
		return true, nil
	}
	return replaceActive(root, name, stage, commit)
}

func replaceActive(root, name, stage string, commit func() error) (bool, error) {
	target := filepath.Join(root, name)
	backup, err := os.MkdirTemp(root, ".builtin-old-"+name+"-")
	if err != nil {
		return false, err
	}
	if err := os.Remove(backup); err != nil {
		return false, err
	}
	if err := os.Rename(target, backup); err != nil {
		return false, err
	}
	if err := os.Rename(stage, target); err != nil {
		restoreErr := os.Rename(backup, target)
		if restoreErr != nil {
			return false, errors.Join(err, fmt.Errorf("restore previous bundled plugin: %w", restoreErr))
		}
		return false, err
	}
	if commit != nil {
		if err := commit(); err != nil {
			if removeErr := os.RemoveAll(target); removeErr != nil {
				return false, errors.Join(err, fmt.Errorf("remove failed bundled activation: %w", removeErr))
			}
			restoreErr := os.Rename(backup, target)
			if restoreErr != nil {
				return false, errors.Join(err, fmt.Errorf("restore previous bundled plugin: %w", restoreErr))
			}
			return false, err
		}
	}
	if err := os.RemoveAll(backup); err != nil {
		return true, err
	}
	return true, nil
}
