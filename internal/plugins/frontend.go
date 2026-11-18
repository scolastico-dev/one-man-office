package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/homefile"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Frontend bounds apply both in install staging and when loading an installed
// tree. Every entry counts, including directories; the script counts once.
const (
	MaxFrontendScriptBytes = 1 << 20
	MaxFrontendBytes       = 64 << 20
	MaxFrontendEntries     = 10000
)

// publicPath accepts portable slash-separated paths only. Reserving private
// components throughout the tree keeps a future asset route from exposing them.
func publicPath(p string) error {
	bad := func() error {
		return errors.New("must be a portable relative path inside the plugin without private files, traversal or Windows special names")
	}
	if p == "" || len(p) > 512 || !utf8.ValidString(p) || strings.ContainsAny(p, "\\:\x00") || strings.ContainsFunc(p, func(r rune) bool { return unicode.IsControl(r) || textsafe.IsHidden(r) }) {
		return bad()
	}
	for _, part := range strings.Split(p, "/") {
		low := strings.ToLower(part)
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || strings.ContainsAny(part, `<>"|?*`) {
			return bad()
		}
		if low == "settings" || low == "settings.yaml" || low == "settings.yml" || low == "settings.json" || low == "plugin.json" {
			return bad()
		}
		stem, _, _ := strings.Cut(strings.ToUpper(part), ".")
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || (utf8.RuneCountInString(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && strings.ContainsAny(stem[3:], "123456789¹²³")) {
			return bad()
		}
	}
	return nil
}

// frontendNode checks every component with Lstat. No symlinks, including links
// within the plugin, are supported. Asset serving must repeat confinement checks
// when reading; validation is not a filesystem security boundary against edits.
func frontendNode(dir, rel string, folder bool) error {
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return errors.New("plugin root must be a directory, not a link")
	}
	cur := dir
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		info, err = os.Lstat(cur)
		if err != nil {
			return errors.New("declared frontend file or directory is missing or unreadable")
		}
		wantDir := i < len(parts)-1 || folder
		if (wantDir && !info.IsDir()) || (!wantDir && !info.Mode().IsRegular()) {
			return errors.New("frontend must contain directories and regular files only, without symlinks")
		}
	}
	return nil
}

func hashFrontendFile(h hash.Hash, dir, rel string, limit int64) (int64, error) {
	if err := publicPath(rel); err != nil {
		return 0, err
	}
	if err := frontendNode(dir, rel, false); err != nil {
		return 0, err
	}
	b, err := homefile.Read(filepath.Join(dir, filepath.FromSlash(rel)), limit)
	if err != nil {
		return 0, errors.New("frontend file cannot be read or exceeds its byte limit")
	}
	// Length framing prevents file-name/content boundary collisions.
	fmt.Fprintf(h, "%d:%s:%d:", len(rel), rel, len(b))
	_, _ = h.Write(b)
	return int64(len(b)), nil
}

func (m *Manifest) readFrontend(dir string, raw []byte) error {
	h := sha256.New()
	fmt.Fprintf(h, "%d:", len(raw))
	_, _ = h.Write(raw)
	if m.Frontend.Script != "" {
		total, err := hashFrontendFile(h, dir, m.Frontend.Script, MaxFrontendScriptBytes)
		if err != nil {
			return fmt.Errorf("frontend.script: %w", err)
		}
		if m.Frontend.Assets != "" {
			if err := frontendNode(dir, m.Frontend.Assets, true); err != nil {
				return fmt.Errorf("frontend.assets: %w", err)
			}
			entries := 1
			err := filepath.WalkDir(filepath.Join(dir, filepath.FromSlash(m.Frontend.Assets)), func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return errors.New("frontend assets cannot be read")
				}
				rel, err := filepath.Rel(dir, p)
				if err != nil {
					return errors.New("frontend assets must stay inside the plugin")
				}
				rel = filepath.ToSlash(rel)
				// The entry was already read and hashed, and counts exactly once even
				// when it is also part of the public assets directory.
				if rel == m.Frontend.Script && d.Type().IsRegular() {
					return nil
				}
				entries++
				if entries > MaxFrontendEntries {
					return errors.New("frontend has more than 10000 entries")
				}
				if err := publicPath(rel); err != nil {
					return err
				}
				if d.IsDir() {
					fmt.Fprintf(h, "directory:%d:%s:", len(rel), rel)
					return nil
				}
				if d.Type()&os.ModeSymlink != 0 || !d.Type().IsRegular() {
					return errors.New("frontend assets must be regular files, without symlinks")
				}
				n, err := hashFrontendFile(h, dir, rel, MaxFrontendBytes-total)
				total += n
				return err
			})
			if err != nil {
				return fmt.Errorf("frontend.assets: %w", err)
			}
		}
	}
	m.contentID = hex.EncodeToString(h.Sum(nil))
	return nil
}
