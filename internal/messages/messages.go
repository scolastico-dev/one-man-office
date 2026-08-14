// Package messages holds every line omo itself puts in front of an agent or
// the user: the start prompt, the mail nudge, the goals it composes for
// reviewers and firefighters, and the failure notifications.
//
// Defaults are embedded in the binary and written into <office>/.omo/messages
// by `omo setup`. Editing a file there changes what omo says without a
// rebuild; deleting it falls back to the embedded default.
package messages

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/template"
)

//go:embed defaults/*.txt
var defaults embed.FS

// Dir is the per-office override directory, relative to the office root.
const Dir = ".omo/messages"

// Names is every customizable message, in a stable order.
var Names = []string{
	"ceo_gave_up",
	"ceo_goal",
	"firefighter_goal",
	"job_failed",
	"mail_nudge",
	"restart_note",
	"restart_review_note",
	"review_failed",
	"review_goal",
	"review_escalated",
	"review_override",
	"smokealarm_goal",
	"spawn_failed",
	"start_prompt",
}

// Set is a loaded, parsed collection of message templates.
type Set struct {
	tmpl map[string]*template.Template
}

// ReviewData fills review_goal.
type ReviewData struct {
	JobID  int64
	Title  string
	Goal   string
	Branch string
	Diff   string
}

// IncidentData fills firefighter_goal.
type IncidentData struct {
	ID       int64
	Agent    string
	Class    string
	Detail   string
	Snapshot string
}

// Load reads the embedded defaults, then lets <officeDir>/.omo/messages
// override any of them. A malformed override is an error: silently falling
// back would hide the user's edit.
func Load(officeDir string) (*Set, error) {
	s := &Set{tmpl: make(map[string]*template.Template, len(Names))}
	for _, name := range Names {
		text, err := defaultText(name)
		if err != nil {
			return nil, err
		}
		override := filepath.Join(officeDir, Dir, name+".txt")
		if raw, err := os.ReadFile(override); err == nil {
			text = string(raw)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		t, err := template.New(name).Parse(text)
		if err != nil {
			return nil, fmt.Errorf("messages: %s.txt: %w", name, err)
		}
		s.tmpl[name] = t
	}
	return s, nil
}

func defaultText(name string) (string, error) {
	raw, err := defaults.ReadFile("defaults/" + name + ".txt")
	if err != nil {
		return "", fmt.Errorf("messages: no embedded default for %q", name)
	}
	return string(raw), nil
}

// DefaultsDigest fingerprints the complete embedded message set. It tracks
// the binary's template generation without comparing against editable office
// files, so local customizations do not look stale by themselves.
func DefaultsDigest() (string, error) {
	h := sha256.New()
	for _, name := range Names {
		raw, err := defaults.ReadFile("defaults/" + name + ".txt")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00", name)
		h.Write(raw)
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// WriteDefaults materialises the templates into <officeDir>/.omo/messages,
// never overwriting a file the user has already edited.
func WriteDefaults(officeDir string) error {
	dir := filepath.Join(officeDir, Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range Names {
		path := filepath.Join(dir, name+".txt")
		if _, err := os.Stat(path); err == nil {
			continue // keep the user's version
		}
		text, err := defaultText(name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// Render executes one message by name.
func (s *Set) Render(name string, data any) (string, error) {
	t, ok := s.tmpl[name]
	if !ok {
		return "", fmt.Errorf("messages: unknown message %q (known: %v)", name, sortedNames(s.tmpl))
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("messages: %s: %w", name, err)
	}
	return b.String(), nil
}

func sortedNames(m map[string]*template.Template) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// must renders a message, falling back to the raw template name on error.
// Messages are parsed at load time, so an error here means bad data, not a
// bad template — never a reason to take the office down.
func (s *Set) must(name string, data any) string {
	out, err := s.Render(name, data)
	if err != nil {
		return fmt.Sprintf("[omo: could not render %s: %v]", name, err)
	}
	return trimTrailingNewline(out)
}

func trimTrailingNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// Typed accessors keep call sites readable and the placeholder names in one
// place.

func (s *Set) StartPrompt(name string) string {
	return s.must("start_prompt", map[string]any{"Name": name})
}

func (s *Set) MailNudge() string { return s.must("mail_nudge", nil) }

func (s *Set) RestartNote() string { return s.must("restart_note", nil) }

func (s *Set) RestartReviewNote() string { return s.must("restart_review_note", nil) }

func (s *Set) CEOGoal() string { return s.must("ceo_goal", nil) }

func (s *Set) SmokeAlarmGoal(report string) string {
	return s.must("smokealarm_goal", map[string]any{"Report": report})
}

func (s *Set) ReviewGoal(d ReviewData) string { return s.must("review_goal", d) }

func (s *Set) FirefighterGoal(d IncidentData) string { return s.must("firefighter_goal", d) }

func (s *Set) SpawnFailed(name, role string, attempts int) string {
	return s.must("spawn_failed", map[string]any{"Name": name, "Role": role, "Attempts": attempts})
}

func (s *Set) JobFailed(jobID int64, title string, count int) string {
	return s.must("job_failed", map[string]any{"JobID": jobID, "Title": title, "Count": count})
}

func (s *Set) ReviewFailed(jobID int64, count int) string {
	return s.must("review_failed", map[string]any{"JobID": jobID, "Count": count})
}

func (s *Set) ReviewEscalated(jobID int64, count int) string {
	return s.must("review_escalated", map[string]any{"JobID": jobID, "Count": count})
}

func (s *Set) ReviewOverride(jobID int64, notes string) string {
	return s.must("review_override", map[string]any{"JobID": jobID, "Notes": notes})
}

func (s *Set) CEOGaveUp(count int, window string) string {
	return s.must("ceo_gave_up", map[string]any{"Count": count, "Window": window})
}
