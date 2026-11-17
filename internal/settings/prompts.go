package settings

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/homefile"
)

// PromptInfo names a prompt and says whether a file overrides its default.
type PromptInfo = api.PromptInfo

func (s *Store) promptDir() string { return filepath.Join(s.root, "prompts") }

func (s *Store) promptPath(name string) string {
	return filepath.Join(s.promptDir(), name+".md")
}

func (s *Store) promptKind() *kind {
	return &kind{
		section: SectionPrompts,
		dir:     s.promptDir(),
		ext:     ".md",
		limit:   homefile.MaxPromptFile,
		load: func(name string, data []byte) error {
			if !utf8.Valid(data) {
				return errors.New("a prompt must be valid UTF-8 text")
			}
			s.prompts[name] = string(data)
			return nil
		},
		drop: func(name string) bool {
			_, ok := s.prompts[name]
			delete(s.prompts, name)
			return ok
		},
	}
}

// Prompts lists the prompts that have a default or an override, sorted by
// name.
func (s *Store) Prompts() []PromptInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := map[string]bool{}
	for name := range s.opts.PromptDefaults {
		names[name] = true
	}
	for name := range s.prompts {
		names[name] = true
	}
	out := make([]PromptInfo, 0, len(names))
	for name := range names {
		_, custom := s.prompts[name]
		out = append(out, PromptInfo{Name: name, Customized: custom})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Prompt returns the text in effect for a prompt: the override when there is
// one, else the default. ok is false when there is neither.
func (s *Store) Prompt(name string) (text string, customized bool, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if t, found := s.prompts[name]; found {
		return t, true, true
	}
	t, found := s.opts.PromptDefaults[name]
	return t, false, found
}

// maxPromptSize is the largest prompt text, in bytes.
const maxPromptSize = homefile.MaxPromptFile

// isInstructions reports whether name is a role's instructions prompt, whose
// default is empty.
func isInstructions(name string) bool { return strings.HasSuffix(name, "-instructions") }

// PutPromptIf stores text as the override of a prompt. Only prompts that have
// a default can be saved (others give not_found). The text must not be blank,
// except for an instructions prompt, where blank text is a reset.
// Options.ValidatePrompt checks the text first.
// It is guarded by a revision: the prompt's file is read
// again first, and when rev is not empty and differs from the file's revision
// (NoRevision when the prompt is not customized) the write is refused with
// code stale_settings.
func (s *Store) PutPromptIf(rev, name, text string) error {
	if !s.knownPrompt(name) {
		return api.Errorf(api.CodeNotFound, "no prompt %s", showKey(name))
	}
	if isInstructions(name) && strings.TrimSpace(text) == "" {
		return s.ResetPromptIf(rev, name)
	}
	if strings.TrimSpace(text) == "" {
		return apiError(api.CodeValidation, fieldErr("text", "a prompt must not be empty; reset it to use the default"))
	}
	if !utf8.ValidString(text) {
		return apiError(api.CodeValidation, fieldErr("text", "a prompt must be valid UTF-8 text"))
	}
	if len(text) > maxPromptSize {
		return apiError(api.CodeValidation, fieldErr("text", "a prompt must be at most %d KiB", maxPromptSize>>10))
	}
	if s.opts.ValidatePrompt != nil {
		if err := s.opts.ValidatePrompt(name, text); err != nil {
			return apiError(api.CodeValidation, &FieldError{Field: "text", Message: err.Error()})
		}
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionPrompts, name, rev)
		if err != nil {
			return changes, err
		}
		if err := s.writeFile(s.promptPath(name), []byte(text)); err != nil {
			return changes, err
		}
		s.prompts[name] = text
		return append(changes, Change{Section: SectionPrompts, Name: name}), nil
	})
}

// knownPrompt reports whether name is a prompt omo has a default for: only
// those are ever read.
func (s *Store) knownPrompt(name string) bool {
	_, ok := s.opts.PromptDefaults[name]
	return ok && idPattern.MatchString(name)
}

// ResetPromptIf removes a prompt's override so the default applies again. A
// prompt without a default gives not_found; resetting a prompt that is not
// customized does nothing.
// It is guarded by a revision, as PutPromptIf.
func (s *Store) ResetPromptIf(rev, name string) error {
	if !s.knownPrompt(name) {
		return api.Errorf(api.CodeNotFound, "no prompt %s", showKey(name))
	}
	return s.mutate(func() ([]Change, error) {
		changes, err := s.guard(SectionPrompts, name, rev)
		if err != nil {
			return changes, err
		}
		_, loaded := s.prompts[name]
		path := s.promptPath(name)
		if !loaded && !fileExists(path) {
			return changes, nil
		}
		if err := s.removeFile(path); err != nil {
			return changes, err
		}
		delete(s.prompts, name)
		return append(changes, Change{Section: SectionPrompts, Name: name, Removed: true}), nil
	})
}
