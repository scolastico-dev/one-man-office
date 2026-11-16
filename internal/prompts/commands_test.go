package prompts_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/cli"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// pending lists commands and flags the prompts teach that the command line
// does not have yet. Remove an entry as soon as it exists; the test fails
// while an entry is listed that exists.
var pending = []string{}

// segment is the text after "omo ai " up to the end of a code span, a
// sentence break or the line.
var segment = regexp.MustCompile("omo ai ([^`;)\n]*)")

// word is a command name: lower-case words joined by "-" (remove-project).
var word = regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)

var flagWord = regexp.MustCompile(`^--[a-z][a-z-]*`)

func TestDefaultPromptsUseExistingCommands(t *testing.T) {
	root := cli.Root("test")
	seenPending := map[string]bool{}
	for name, text := range prompts.Defaults() {
		for _, m := range segment.FindAllStringSubmatch(text, -1) {
			fields := strings.Fields(m[1])
			args := []string{"ai"}
			i := 0
			for ; i < len(fields); i++ {
				if !word.MatchString(fields[i]) {
					break
				}
				args = append(args, fields[i])
			}
			if len(args) == 1 {
				continue // "omo ai" itself, e.g. "omo ai --help"
			}
			cmd, rest, err := root.Find(args)
			path := strings.Join(args[1:len(args)-len(rest)], " ")
			if err != nil || cmd.Name() == "ai" {
				t.Errorf("%s: %q is not a command", name, m[0])
				continue
			}
			if len(rest) > 0 && cmd.HasSubCommands() {
				full := path + " " + rest[0]
				if slices.Contains(pending, full) {
					seenPending[full] = true
					continue
				}
				t.Errorf("%s: %q: %q has no subcommand %q", name, m[0], path, rest[0])
				continue
			}
			if cmd.HasSubCommands() {
				t.Errorf("%s: %q names a command group, not a command", name, m[0])
			}
			for _, f := range fields[i:] {
				fl := flagWord.FindString(f)
				if fl == "" {
					continue
				}
				key := path + " " + fl
				if cmd.Flag(strings.TrimPrefix(fl, "--")) != nil {
					if slices.Contains(pending, key) {
						t.Errorf("%q exists now; remove it from pending", key)
					}
					continue
				}
				if slices.Contains(pending, key) {
					seenPending[key] = true
					continue
				}
				t.Errorf("%s: %q: %q has no flag %s", name, m[0], path, fl)
			}
		}
	}
	for _, p := range pending {
		words := append([]string{"ai"}, strings.Fields(p)...)
		if !strings.HasPrefix(words[len(words)-1], "--") {
			if cmd, rest, err := root.Find(words); err == nil && len(rest) == 0 && cmd.Name() == words[len(words)-1] {
				t.Errorf("%q exists now; remove it from pending", p)
			}
		}
		if !seenPending[p] {
			t.Errorf("pending entry %q is not used by any prompt", p)
		}
	}
}

// The user's instructions live in their own prompt item, so a new default
// for a role keeps what the user wrote, and a reset restores the empty
// default.
func TestUserInstructionsSurviveNewDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	s, err := settings.Open(home, settings.Options{PromptDefaults: prompts.Defaults()})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutPrompt("developer-instructions", "Write commit messages in German.\n"); err != nil {
		t.Fatal(err)
	}

	newer := prompts.Defaults()
	newer["developer"] += "\nA new rule.\n"
	s, err = settings.Open(home, settings.Options{PromptDefaults: newer})
	if err != nil {
		t.Fatal(err)
	}
	text, custom, ok := s.Prompt("developer")
	if !ok || custom || text != newer["developer"] {
		t.Fatalf("developer = %q %v %v, want the new default", text, custom, ok)
	}
	text, custom, ok = s.Prompt("developer-instructions")
	if !ok || !custom || text != "Write commit messages in German.\n" {
		t.Fatalf("developer-instructions = %q %v %v, want the user's text", text, custom, ok)
	}
	if err := s.ResetPrompt("developer-instructions"); err != nil {
		t.Fatal(err)
	}
	if text, custom, ok = s.Prompt("developer-instructions"); !ok || custom || text != "" {
		t.Fatalf("after reset = %q %v %v, want the empty default", text, custom, ok)
	}
}
