package settings

import (
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// UsageScriptInfo names a usage script, says whether it has a default and a
// file, and which profiles use it.
type UsageScriptInfo = api.UsageScriptInfo

// maxUsageScript is the largest usage script, in bytes.
const maxUsageScript = 64 << 10

func (s *Store) usageDir() string { return filepath.Join(s.root, "usage") }

func (s *Store) usagePath(name string) string {
	return filepath.Join(s.usageDir(), name+".lua")
}

func (s *Store) usageKind() *kind {
	return &kind{
		section: SectionUsage,
		dir:     s.usageDir(),
		ext:     ".lua",
		limit:   maxUsageScript,
		load: func(name string, data []byte) error {
			// Whether the script compiles is the tracker's to show: a
			// hand edit that does not is still the script in effect.
			if err := checkScriptText(string(data)); err != nil {
				return err
			}
			s.scripts[name] = string(data)
			return nil
		},
		drop: func(name string) bool {
			_, ok := s.scripts[name]
			delete(s.scripts, name)
			return ok
		},
	}
}

// checkScriptText checks the text of a usage script: not blank, at most
// maxUsageScript bytes, UTF-8, no hidden characters. The error is a
// *FieldError for "text".
func checkScriptText(text string) error {
	if strings.TrimSpace(text) == "" {
		return fieldErr("text", "a usage script must not be empty")
	}
	if len(text) > maxUsageScript {
		return fieldErr("text", "a usage script must be at most %d KiB", maxUsageScript>>10)
	}
	if !utf8.ValidString(text) {
		return fieldErr("text", "a usage script must be valid UTF-8 text")
	}
	if hiddenInText(text) {
		return fieldErr("text", "a usage script must not hold control, invisible or direction characters other than line breaks and tabs")
	}
	return nil
}

// hiddenInText reports whether a multi-line text holds a character omo never
// shows as it is, other than line feeds, tabs and the carriage return of a
// CRLF line end.
func hiddenInText(text string) bool {
	for i, r := range text {
		switch {
		case r == '\n', r == '\t':
		case r == '\r':
			if !strings.HasPrefix(text[i+1:], "\n") {
				return true
			}
		case textsafe.IsHidden(r):
			return true
		}
	}
	return false
}

// UsageScripts lists the usage scripts that have a default or a file, sorted
// by name, with the profiles that name each (sorted) and the file's revision
// (NoRevision for a default without a file).
func (s *Store) UsageScripts() []UsageScriptInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := map[string]bool{}
	for name := range s.opts.UsageDefaults {
		names[name] = true
	}
	for name := range s.scripts {
		names[name] = true
	}
	out := make([]UsageScriptInfo, 0, len(names))
	for name := range names {
		_, def := s.opts.UsageDefaults[name]
		_, custom := s.scripts[name]
		out = append(out, UsageScriptInfo{
			Name:       name,
			HasDefault: def,
			Customized: custom,
			UsedBy:     s.scriptUsersLocked(name),
			Revision:   s.revisionLocked(s.usagePath(name)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// scriptUsersLocked lists the profiles naming a usage script, sorted. It
// runs with the state lock held.
func (s *Store) scriptUsersLocked(name string) []string {
	users := []string{}
	for _, p := range s.profiles {
		if p.UsageScript == name {
			users = append(users, p.Name)
		}
	}
	slices.Sort(users)
	return users
}

// UsageScript returns the text in effect for a usage script: its file when
// there is one, else its default. ok is false when there is neither.
func (s *Store) UsageScript(name string) (text string, customized, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if t, found := s.scripts[name]; found {
		return t, true, true
	}
	t, found := s.opts.UsageDefaults[name]
	return t, false, found
}

// UsageDefault returns the embedded default of a usage script; ok is false
// for a script without one.
func (s *Store) UsageDefault(name string) (text string, ok bool) {
	text, ok = s.opts.UsageDefaults[name]
	return text, ok
}

// scriptExistsLocked reports whether a usage script has a default or a
// loaded file. It runs with the state lock held.
func (s *Store) scriptExistsLocked(name string) bool {
	if _, ok := s.scripts[name]; ok {
		return true
	}
	_, ok := s.opts.UsageDefaults[name]
	return ok
}

// PutUsageScriptIf writes a usage script (usage/<name>.lua), creating it or
// customizing its default, guarded by a revision as PutPromptIf: rev
// NoRevision means create only (for a script with a default: only while it
// is not customized); "" checks nothing. The name must match the id pattern;
// the text must not be blank, at most 64 KiB of UTF-8 without hidden
// characters, and pass Options.ValidateUsageScript. Refusals are code
// validation naming the field "name" or "text".
func (s *Store) PutUsageScriptIf(rev, name, text string) error {
	if !idPattern.MatchString(name) {
		return apiError(api.CodeValidation, fieldErr("name", "the script name must match %s", idPattern))
	}
	if err := checkScriptText(text); err != nil {
		return apiError(api.CodeValidation, err)
	}
	if s.opts.ValidateUsageScript != nil {
		if err := s.opts.ValidateUsageScript(name, text); err != nil {
			return apiError(api.CodeValidation, &FieldError{Field: "text", Message: err.Error()})
		}
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionUsage, name, rev)
		if err != nil {
			return changes, err
		}
		if err := s.writeFile(s.usagePath(name), []byte(text)); err != nil {
			return changes, err
		}
		s.scripts[name] = text
		return append(changes, Change{Section: SectionUsage, Name: name}), nil
	})
}

// ResetUsageScriptIf removes a usage script's file, guarded by a revision as
// PutUsageScriptIf. A script with a default is then the default again
// (resetting one that is not customized does nothing); a script without one
// is gone, which is refused with code in_use, naming the profiles, while a
// profile names it. An unknown script gives not_found. A file that could not
// be loaded can be removed too.
func (s *Store) ResetUsageScriptIf(rev, name string) error {
	if !idPattern.MatchString(name) {
		return api.Errorf(api.CodeNotFound, "no usage script %s", showKey(name))
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionUsage, name, rev)
		if err != nil {
			return changes, err
		}
		_, loaded := s.scripts[name]
		_, def := s.opts.UsageDefaults[name]
		path := s.usagePath(name)
		onDisk := fileExists(path)
		if !loaded && !onDisk {
			if def {
				return changes, nil
			}
			return changes, api.Errorf(api.CodeNotFound, "no usage script %s", name)
		}
		if !def {
			if users := s.scriptUsersLocked(name); len(users) > 0 {
				return changes, api.Errorf(api.CodeInUse, "usage script %s is used by the profiles %s", name, strings.Join(users, ", "))
			}
		}
		if err := s.removeFile(path); err != nil {
			return changes, err
		}
		delete(s.scripts, name)
		return append(changes, Change{Section: SectionUsage, Name: name, Removed: true}), nil
	})
}
