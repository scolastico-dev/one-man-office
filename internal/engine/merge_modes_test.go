package engine

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// setMergeDefault sets the general merge default of one kind of merge.
func (h *harness) setMergeDefault(kind, mode string) {
	h.t.Helper()
	g := h.st.General()
	switch kind {
	case "story":
		g.MergeDefaults.Story = mode
	case "task":
		g.MergeDefaults.Task = mode
	case "story_task":
		g.MergeDefaults.StoryTask = mode
	}
	if err := h.st.SetGeneral(g); err != nil {
		h.t.Fatal(err)
	}
}

// setProjectMerge sets the project's own merge mode of one kind of merge.
func (h *harness) setProjectMerge(kind, mode string) {
	h.t.Helper()
	var u settings.ProjectUpdate
	switch kind {
	case "story":
		u.MergeStory = &mode
	case "task":
		u.MergeTask = &mode
	case "story_task":
		u.MergeStoryTask = &mode
	}
	if _, err := h.st.UpdateProject(h.project, u); err != nil {
		h.t.Fatal(err)
	}
}

// headOf returns the parents and the message of branch's head.
func headOf(t *testing.T, repo, branch string) (parents []string, message string) {
	t.Helper()
	return strings.Fields(gitRun(t, repo, "log", "-1", "--format=%P", branch)),
		gitRun(t, repo, "log", "-1", "--format=%B", branch)
}

// Each kind of merge uses the mode in effect for it: the project's own
// mode, else the general default. The other kinds keep theirs.
func TestMergeModeOfEachKind(t *testing.T) {
	squash, merge := api.MergeModeSquash, api.MergeModeMerge
	other := map[string]string{squash: merge, merge: squash}
	for _, c := range []struct {
		kind, mode string
		// own sets the mode as the project's own, with the opposite
		// general default; otherwise it is the general default.
		own bool
	}{
		{"task", squash, false},
		{"task", merge, true},
		{"story_task", squash, true},
		{"story_task", merge, false},
		{"story", squash, false},
		{"story", merge, true},
	} {
		name := fmt.Sprintf("%s %s own=%v", c.kind, c.mode, c.own)
		t.Run(name, func(t *testing.T) {
			h := gitHarness(t)
			if c.own {
				h.setMergeDefault(c.kind, other[c.mode])
				h.setProjectMerge(c.kind, c.mode)
			} else {
				h.setMergeDefault(c.kind, c.mode)
			}
			switch c.kind {
			case "task":
				mainBefore := gitRun(t, h.repo, "rev-parse", "main")
				n := h.skippedTask("Add b")
				h.finishTask(n, h.project, "b.txt", "b\n")
				h.steps(2)
				if tk := h.ticket(n); tk.Column != api.ColumnDone {
					t.Fatalf("task is %s", tk.Column)
				}
				checkMerged(t, h, "main", mainBefore, c.mode, "Merge: Add b", "Add b")
				gitRun(t, h.repo, "cat-file", "-e", "main:b.txt")
			case "story_task":
				s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Story", Review: api.ReviewSkipped})
				h.startStory(s)
				integration := h.ticket(s).Projects[0].Branch
				intBefore := gitRun(t, h.repo, "rev-parse", integration)
				task := h.storyTask(s, h.project, "Write the docs")
				h.finishTask(task, h.project, "docs.txt", "docs\n")
				taskBranch := h.ticket(task).Projects[0].Branch
				h.steps(2)
				if tk := h.ticket(task); tk.Column != api.ColumnDone || tk.Resolution != api.ResolutionCompleted {
					t.Fatalf("task is %s/%s", tk.Column, tk.Resolution)
				}
				checkMerged(t, h, integration, intBefore, c.mode, "Merge: Write the docs", "Write the docs")
				if slices.Contains(h.branches(h.repo), taskBranch) {
					t.Fatalf("the task's branch %s was kept", taskBranch)
				}
				// The story then merges as stories do (merge commit by
				// default) and brings the task's work to main.
				h.handIn(s)
				h.steps(2)
				if c := h.ticket(s).Column; c != api.ColumnDone {
					t.Fatalf("story is %s", c)
				}
				if parents, _ := headOf(t, h.repo, "main"); len(parents) != 2 {
					t.Fatalf("story merge has parents %v", parents)
				}
				gitRun(t, h.repo, "cat-file", "-e", "main:docs.txt")
			case "story":
				s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Story", Review: api.ReviewSkipped})
				h.startStory(s)
				integration := h.ticket(s).Projects[0].Branch
				task := h.storyTask(s, h.project, "One")
				h.finishTask(task, h.project, "one.txt", "1\n")
				h.steps(2)
				// The task kept the default of its own kind: a merge commit.
				if parents, _ := headOf(t, h.repo, integration); len(parents) != 2 {
					t.Fatalf("the task's merge into the story has parents %v", parents)
				}
				mainBefore := gitRun(t, h.repo, "rev-parse", "main")
				h.handIn(s)
				h.steps(2)
				if c := h.ticket(s).Column; c != api.ColumnDone {
					t.Fatalf("story is %s", c)
				}
				checkMerged(t, h, "main", mainBefore, c.mode, "Merge: Story",
					"Story\n\nTasks:\n- One")
				gitRun(t, h.repo, "cat-file", "-e", "main:one.txt")
			}
			if len(h.branches(h.repo)) != 0 && c.kind != "story_task" {
				t.Fatalf("branches left: %v", h.branches(h.repo))
			}
		})
	}
}

// checkMerged checks that branch moved from before by one commit made in
// mode: a merge commit with mergeMsg, or a squash commit (one parent,
// before) with squashMsg.
func checkMerged(t *testing.T, h *harness, branch, before, mode, mergeMsg, squashMsg string) {
	t.Helper()
	parents, msg := headOf(t, h.repo, branch)
	if mode == api.MergeModeSquash {
		if len(parents) != 1 || parents[0] != before {
			t.Fatalf("%s: squash commit has parents %v, want %s", branch, parents, before)
		}
		if msg != squashMsg {
			t.Fatalf("%s: squash message %q, want %q", branch, msg, squashMsg)
		}
		return
	}
	if len(parents) != 2 || parents[0] != before {
		t.Fatalf("%s: merge commit has parents %v, want %s and the ticket's branch", branch, parents, before)
	}
	if msg != mergeMsg {
		t.Fatalf("%s: merge message %q, want %q", branch, msg, mergeMsg)
	}
}

// A squashed task's branch is deleted by the squash commit omo recorded for
// it, even when the mode was changed back and the squash was partly
// reverted before the cleanup ran.
func TestCleanupOfASquashedTaskGoesByItsSquashCommit(t *testing.T) {
	h := gitHarness(t)
	h.setMergeDefault("task", api.MergeModeSquash)
	n := h.skippedTask("Squashed")
	h.finishTask(n, h.project, "one.txt", "1\n")
	changed := false
	h.e.pause = func(point string) {
		if point == "before-remove" && !changed {
			changed = true
			h.setMergeDefault("task", api.MergeModeMerge)
			gitRun(t, h.repo, "rm", "-q", "one.txt")
			gitRun(t, h.repo, "commit", "-q", "-m", "revert one.txt")
		}
	}
	h.steps(2)
	h.e.pause = nil
	if !changed {
		t.Fatal("the cleanup never ran")
	}
	if c := h.ticket(n).Column; c != api.ColumnDone {
		t.Fatalf("task is %s", c)
	}
	if len(h.branches(h.repo)) != 0 {
		t.Fatalf("the squashed task's branch was kept: %v", h.branches(h.repo))
	}
}
