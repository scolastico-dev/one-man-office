package cli

import (
	"strings"
	"testing"
)

func TestAITaskWithoutProject(t *testing.T) {
	e := startAI(t)
	for _, args := range [][]string{
		{"--type", "task", "--title", "X"},
		{"--type", "task", "--title", "X", "--no-project", "--project", e.project},
		{"--type", "story", "--title", "X", "--no-project"},
	} {
		if _, err := e.run("", append([]string{"ticket", "create"}, args...)...); err == nil {
			t.Fatalf("create %v was accepted", args)
		}
	}
	if _, err := e.run("", "ticket", "create", "--type", "task", "--title", "X"); err == nil || !strings.Contains(err.Error(), "--no-project") {
		t.Fatalf("a task without --project: %v", err)
	}
	contains(t, e.must("", "ticket", "create", "--type", "task", "--no-project", "--title", "Research"), "#1 task planning Research")
	out := e.must("", "ticket", "show", "1")
	contains(t, out, "projects: none\n")
	contains(t, out, "working folder: none\n")
	contains(t, out, "when approved: done (nothing to merge or export)\n")

	// A project, and none again, while no work started.
	e.must("", "ticket", "update", "1", "--project", e.project)
	contains(t, e.must("", "ticket", "show", "1"), "projects: "+e.project+"\n")
	e.must("", "ticket", "update", "1", "--no-project")
	out = e.must("", "ticket", "show", "1")
	contains(t, out, "projects: none\n")
	if _, err := e.run("", "ticket", "update", "1", "--no-project", "--project", e.project); err == nil {
		t.Fatal("--no-project with --project was accepted")
	}

	help := e.must("", "ticket", "create", "--help")
	contains(t, help, "--no-project")
	contains(t, help, "research")
	contains(t, help, "Code changes to a project need a task with that")
	contains(t, e.must("", "ticket", "remove-workdir", "--help"), "only once the\nticket is done")
}
