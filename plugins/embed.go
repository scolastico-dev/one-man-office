// Package bundledplugins holds the plugins shipped inside the omo binary.
// Only pushover is bundled; the other folders next to this file (bugreport)
// are sources kept in the repository and are neither embedded nor offered.
package bundledplugins

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
)

// Pushover is the name of the bundled Pushover plugin.
const Pushover = "pushover"

//go:embed pushover
var files embed.FS

// names are the bundled plugins, each a folder of files.
var names = []string{Pushover}

// ErrUnknown is the error for a name that is not a bundled plugin.
var ErrUnknown = errors.New("no bundled plugin has this name")

// Names lists the bundled plugins in order.
func Names() []string { return append([]string(nil), names...) }

// Has reports whether name is a bundled plugin.
func Has(name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// Files lists the files of a bundled plugin, relative to its folder, with
// forward slashes, sorted.
func Files(name string) ([]string, error) {
	if !Has(name) {
		return nil, ErrUnknown
	}
	var out []string
	err := fs.WalkDir(files, name, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, p[len(name)+1:])
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// ReadFile is the content of the file rel of a bundled plugin.
func ReadFile(name, rel string) ([]byte, error) {
	if !Has(name) {
		return nil, ErrUnknown
	}
	return files.ReadFile(path.Join(name, rel))
}

// Digest fingerprints a bundled plugin: SHA-256 over its sorted relative
// paths and contents, each followed by a NUL byte. Two builds with the same
// plugin files have the same digest.
func Digest(name string) (string, error) {
	paths, err := Files(name)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, p := range paths {
		raw, err := files.ReadFile(name + "/" + p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00", p)
		h.Write(raw)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CopyTo writes the files of a bundled plugin into the folder dir, which
// must exist: folders with mode 0700, files with mode 0600.
func CopyTo(name, dir string) error {
	paths, err := Files(name)
	if err != nil {
		return err
	}
	for _, p := range paths {
		raw, err := files.ReadFile(name + "/" + p)
		if err != nil {
			return err
		}
		dest := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dest, raw, 0o600); err != nil {
			return err
		}
	}
	return nil
}
