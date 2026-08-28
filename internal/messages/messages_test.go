package messages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsRenderWithoutAnOfficeDir(t *testing.T) {
	m, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := m.StartPrompt("ceo-ada"); !strings.Contains(got, "ceo-ada") || !strings.Contains(got, "omo ready") {
		t.Fatalf("start prompt = %q", got)
	}
	if got := m.MailNudge(); !strings.Contains(got, "omo inbox") {
		t.Fatalf("mail nudge = %q", got)
	}
	if got := m.StatusNudge(); !strings.Contains(got, "omo step") {
		t.Fatalf("status nudge = %q", got)
	}
	if got := m.RestartNote(); !strings.Contains(got, "restart") {
		t.Fatalf("restart note = %q", got)
	}
	for _, want := range []string{"git status", "before taking any action", "preserve every existing uncommitted change", "git checkout .", "git reset --hard", "never discard"} {
		if got := m.RestartNote(); !strings.Contains(got, want) {
			t.Errorf("restart note missing %q: %s", want, got)
		}
	}
	for _, want := range []string{"already entered review", "brief self-check", "context alignment", "omo done"} {
		if got := m.RestartReviewNote(); !strings.Contains(got, want) {
			t.Errorf("restart review note missing %q: %s", want, got)
		}
	}
}

func TestEveryMessageHasADefault(t *testing.T) {
	m, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range Names {
		got, err := m.Render(name, sampleData())
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if strings.TrimSpace(got) == "" {
			t.Errorf("%s rendered empty", name)
		}
	}
}

func TestOfficeOverrideWins(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDefaults(dir); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(dir, Dir, "start_prompt.txt")
	if err := os.WriteFile(custom, []byte("Hallo {{.Name}}, bitte `omo ready` ausführen."), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := m.StartPrompt("pm-alex")
	if !strings.Contains(got, "Hallo pm-alex") {
		t.Fatalf("override ignored: %q", got)
	}
}

func TestWriteDefaultsCreatesEveryFileAndDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDefaults(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range Names {
		if _, err := os.Stat(filepath.Join(dir, Dir, name+".txt")); err != nil {
			t.Errorf("missing %s.txt: %v", name, err)
		}
	}
	// A second run must not overwrite the user's edits.
	p := filepath.Join(dir, Dir, "mail_nudge.txt")
	os.WriteFile(p, []byte("MINE"), 0o644)
	if err := WriteDefaults(dir); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if string(raw) != "MINE" {
		t.Fatalf("WriteDefaults clobbered an edited template: %q", raw)
	}
}

func TestBrokenTemplateIsReportedNotSilentlyIgnored(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, Dir), 0o755)
	os.WriteFile(filepath.Join(dir, Dir, "mail_nudge.txt"), []byte("{{.Unclosed"), 0o644)
	if _, err := Load(dir); err == nil {
		t.Fatal("a malformed template must fail loudly at startup")
	}
}

func TestRenderUnknownMessage(t *testing.T) {
	m, _ := Load(t.TempDir())
	if _, err := m.Render("nope", nil); err == nil {
		t.Fatal("expected an error for an unknown message name")
	}
}

func TestReviewGoalCarriesDiffAndGoal(t *testing.T) {
	m, _ := Load(t.TempDir())
	got := m.ReviewGoal(ReviewData{JobID: 7, Title: "add health", Goal: "implement /health", Branch: "omo/job-7", Diff: "+++ health.go"})
	for _, want := range []string{"7", "add health", "implement /health", "omo/job-7", "+++ health.go", "small, unambiguous issue", "Reject substantive"} {
		if !strings.Contains(got, want) {
			t.Errorf("review goal missing %q:\n%s", want, got)
		}
	}
}

func TestFirefighterGoalKeepsIncidentIDLine(t *testing.T) {
	m, _ := Load(t.TempDir())
	got := m.FirefighterGoal(IncidentData{ID: 3, Agent: "developer-mia", Class: "stuck", Detail: "no output", Snapshot: "job #1"})
	// The exact "INCIDENT_ID: <n>" line is a contract: the firefighter and
	// omo's own resolve flow parse it back out.
	if !strings.Contains(got, "INCIDENT_ID: 3") {
		t.Fatalf("firefighter goal must contain the INCIDENT_ID line:\n%s", got)
	}
}

func sampleData() any {
	return map[string]any{
		"Name": "developer-jason", "JobID": 1, "Title": "t", "Goal": "g",
		"Branch": "omo/job-1", "Diff": "d", "ID": 1, "Agent": "a",
		"Class": "stuck", "Detail": "x", "Snapshot": "s", "Report": "r",
		"Role": "developer", "Attempts": 3, "Window": "30s", "Count": 2,
	}
}
