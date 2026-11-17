package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// NoRevision is the revision of a settings item that has no file.
const NoRevision = api.NoRevision

// revisionOf is the revision of a file whose bytes hash to sum: the first 16
// hex digits of the SHA-256.
func revisionOf(sum [32]byte) string { return hex.EncodeToString(sum[:8]) }

// revisionLocked is the revision of the file at path as the store last read
// or wrote it. It runs with the state lock held.
func (s *Store) revisionLocked(path string) string {
	st := s.files[path]
	if st == nil {
		return NoRevision
	}
	if !st.read {
		// A file that exists but could not be read: a revision of its error,
		// so a write built on what the page showed can replace it.
		msg := ""
		if st.err != nil {
			msg = st.err.Message
		}
		return revisionOf(sha256.Sum256([]byte("unreadable\x00" + msg)))
	}
	return revisionOf(st.sum)
}

// Revision returns the revision of a settings item: the first 16 hex digits
// of the SHA-256 of its file's bytes as the store last read or wrote them, or
// NoRevision when the item has no file. name is empty for the general
// settings; section is one of the Section constants. An unknown section or a
// name that cannot be a file gives NoRevision.
func (s *Store) Revision(section, name string) string {
	k, path, ok := s.item(section, name)
	if !ok || k == nil {
		return NoRevision
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.revisionLocked(path)
}

// item returns the kind and file path of a settings item.
func (s *Store) item(section, name string) (*kind, string, bool) {
	for _, k := range s.kinds {
		if k.section != section {
			continue
		}
		if k.dir == "" {
			return k, k.file, name == ""
		}
		if !k.namePattern().MatchString(name) {
			return nil, "", false
		}
		return k, filepath.Join(k.dir, name+k.ext), true
	}
	return nil, "", false
}

// guard re-reads the file of an item, so that a hand edit not yet found by
// polling is loaded now, and then compares rev with the file's revision. An
// empty rev checks nothing. The changes the re-read found are returned (marked
// as coming from disk) for delivery with the write's own, also when the
// revision does not match. It runs with the state lock held.
func (s *Store) guard(section, name, rev string) ([]Change, error) {
	k, path, ok := s.item(section, name)
	if !ok || k == nil {
		return nil, api.Errorf(api.CodeInternal, "no settings file for %s %s", section, name)
	}
	var changes []Change
	if c, found := s.check(k, name, path, true); found {
		c.FromDisk = true
		changes = append(changes, c)
	}
	if rev == "" || rev == s.revisionLocked(path) {
		return changes, nil
	}
	return changes, api.Errorf(api.CodeStaleSettings, "%s changed since you loaded it. Load it again to see the change.", s.relPath(path))
}

// relPath is path below the omo home, with slashes.
func (s *Store) relPath(path string) string {
	rel, err := filepath.Rel(s.root, path)
	if err != nil {
		return filepath.Base(path)
	}
	return filepath.ToSlash(rel)
}

// FieldError is a validation failure of one settings value. Field is the
// dotted key of the value ("health.silent_after", "workspace_roots.1.path",
// "models.sonnet.env.FOO", "text"); Message names the field in words too, so
// it reads on its own in a file error. Messages never quote a value: a value
// may be a secret.
type FieldError struct {
	Field   string
	Message string
}

// Error implements error.
func (e *FieldError) Error() string { return e.Message }

// fieldErr builds a FieldError.
func fieldErr(field, format string, args ...any) *FieldError {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

// apiError maps a validation failure to an *api.Error with code and, for a
// FieldError, Details api.FieldDetail naming the field. Other errors keep
// their message.
func apiError(code string, err error) error {
	var fe *FieldError
	if errors.As(err, &fe) {
		e := api.Errorf(code, "%s", fe.Message)
		e.Details = api.FieldDetail{Field: fe.Field}
		return e
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae
	}
	return api.Errorf(code, "%v", err)
}

// hasHidden reports whether s holds a character omo never shows as it is.
func hasHidden(s string) bool {
	return strings.ContainsFunc(s, textsafe.IsHidden)
}

// maxShownKey is the most of a key a message shows.
const maxShownKey = 64

// showKey shows a key (a variable or model name) in a message: as it is when
// it is plain text, else quoted with every hidden or non-ASCII character
// escaped; cut after maxShownKey characters. Keys may appear in messages;
// values never do.
func showKey(k string) string {
	cut := false
	if r := []rune(k); len(r) > maxShownKey {
		k, cut = string(r[:maxShownKey]), true
	}
	if !utf8.ValidString(k) || hasHidden(k) || strings.ContainsAny(k, " \"'") || k == "" {
		k = strconv.QuoteToASCII(k)
	}
	if cut {
		k += "…"
	}
	return k
}

// sortedKeys returns the keys of m in order, so the first error reported is
// always the same one.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
