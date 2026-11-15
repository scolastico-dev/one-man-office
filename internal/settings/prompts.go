package settings

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
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

// PutPrompt stores text as the override of a prompt. The name must match the
// id pattern and the text must not be blank.
func (s *Store) PutPrompt(name, text string) error {
	if !idPattern.MatchString(name) {
		return api.Errorf(api.CodeValidation, "prompt name %q must match %s", name, idPattern)
	}
	if strings.TrimSpace(text) == "" {
		return api.Errorf(api.CodeValidation, "a prompt must not be empty; reset it to use the default")
	}
	if !utf8.ValidString(text) {
		return api.Errorf(api.CodeValidation, "a prompt must be valid UTF-8 text")
	}
	return s.mutate(func() ([]Change, error) {
		if err := s.writeFile(s.promptPath(name), []byte(text)); err != nil {
			return nil, err
		}
		s.prompts[name] = text
		return []Change{{Section: SectionPrompts, Name: name}}, nil
	})
}

// ResetPrompt removes a prompt's override so the default applies again. A
// prompt without override and default gives not_found; resetting a prompt
// that is not customized does nothing.
func (s *Store) ResetPrompt(name string) error {
	return s.mutate(func() ([]Change, error) {
		_, loaded := s.prompts[name]
		path := s.promptPath(name)
		_, hasDefault := s.opts.PromptDefaults[name]
		if !idPattern.MatchString(name) || (!loaded && !fileExists(path) && !hasDefault) {
			return nil, api.Errorf(api.CodeNotFound, "no prompt %s", name)
		}
		if !loaded && !fileExists(path) {
			return nil, nil
		}
		if err := s.removeFile(path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		delete(s.prompts, name)
		return []Change{{Section: SectionPrompts, Name: name, Removed: true}}, nil
	})
}
