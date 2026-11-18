package settings

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/testpath"
)

// abs names an absolute path of a test ("/ws") as one that is absolute on
// the platform the test runs on; see testpath.Abs.
func abs(path string) string { return testpath.Abs(path) }

// openTest opens a store in an isolated omo home and records every change.
func openTest(t *testing.T) (*Store, string, *changeLog) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	log := &changeLog{}
	s, err := Open(home, Options{OnChange: log.add})
	if err != nil {
		t.Fatal(err)
	}
	return s, home, log
}

type changeLog struct {
	mu      sync.Mutex
	changes []Change
}

func (l *changeLog) add(c Change) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.changes = append(l.changes, c)
}

func (l *changeLog) take() []Change {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.changes
	l.changes = nil
	return c
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOpenNeverFailsOnInvalidFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	writeFile(t, filepath.Join(home, "settings.yaml"), "limits:\n  tasks: [")
	writeFile(t, filepath.Join(home, "projects", "a.yaml"), "name: [")
	writeFile(t, filepath.Join(home, "projects", "Bad Name.yaml"), "name: x\npath: "+abs("/x")+"\n")
	s, err := Open(home, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.General().Limits.Tasks; got != Defaults().Limits.Tasks {
		t.Fatalf("limits.tasks = %d, want default", got)
	}
	errs := s.Errors()
	if len(errs) != 3 {
		t.Fatalf("errors = %+v, want 3", errs)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "settings.yaml"))
	if string(raw) != "limits:\n  tasks: [" {
		t.Fatalf("broken settings.yaml was overwritten: %q", raw)
	}
}

func TestReloadIgnoresUnchangedAndTempFiles(t *testing.T) {
	s, home, log := openTest(t)
	writeFile(t, filepath.Join(home, "projects", ".omo-write-123"), "garbage")
	if ch := s.Reload(); len(ch) != 0 {
		t.Fatalf("changes = %+v, want none", ch)
	}
	if len(log.take()) != 0 || len(s.Errors()) != 0 {
		t.Fatal("unexpected change or error")
	}
}

func TestVanishedInvalidFileClearsError(t *testing.T) {
	s, home, _ := openTest(t)
	p := filepath.Join(home, "projects", "x.yaml")
	writeFile(t, p, "name: [")
	s.Reload()
	if len(s.Errors()) != 1 {
		t.Fatalf("errors = %+v", s.Errors())
	}
	os.Remove(p)
	s.Reload()
	if len(s.Errors()) != 0 {
		t.Fatalf("errors = %+v, want none", s.Errors())
	}
}

func TestYmlFileIsReportedNotDropped(t *testing.T) {
	s, home, _ := openTest(t)
	writeFile(t, filepath.Join(home, "projects", "x.yml"), "name: x\n")
	s.Reload()
	errs := s.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0].Message, ".yaml") {
		t.Fatalf("errors = %+v", errs)
	}
}

func TestRunPicksUpHandEdits(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	got := make(chan Change, 4)
	s, err := Open(home, Options{PollInterval: 10 * time.Millisecond, OnChange: func(c Change) { got <- c }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	writeFile(t, filepath.Join(home, "settings.yaml"), "limits:\n  tasks: 9\n")
	select {
	case c := <-got:
		if c.Section != SectionGeneral || s.General().Limits.Tasks != 9 {
			t.Fatalf("change = %+v, general = %+v", c, s.General())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hand edit not picked up")
	}
	cancel()
	<-done
}

// max_running_agents was replaced by limits.tasks and limits.stories and is
// not read any more: a file that still has it is a settings error like any
// unknown key, the defaults apply, and the file is left for the user to fix.
func TestRemovedMaxRunningAgentsIsUnknown(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	const text = "max_running_agents: 4\nceo:\n  enabled: false\n"
	writeFile(t, filepath.Join(home, "settings.yaml"), text)
	s, err := Open(home, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if g := s.General(); g.Limits != Defaults().Limits || !g.CEO.Enabled {
		t.Fatalf("general = %+v, want the defaults", g)
	}
	errs := s.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "max_running_agents") {
		t.Fatalf("errors = %+v, want one naming max_running_agents", errs)
	}
	if raw, _ := os.ReadFile(filepath.Join(home, "settings.yaml")); string(raw) != text {
		t.Fatalf("settings.yaml was rewritten: %q", raw)
	}
}
