package aicmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// ticketLine is the one-line form of a ticket: "#<n> <type> <column> <title>".
func ticketLine(t api.TicketSummary) string {
	line := fmt.Sprintf("#%d %s %s %s", t.Number, t.Type, t.Column, OneLine(t.Title))
	if t.ReviewStage == api.ReviewStageAwaitingMerge {
		line += " [exported, waiting for the merge]"
	}
	return line
}

func numbers(ns []int) string {
	if len(ns) == 0 {
		return "none"
	}
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = "#" + strconv.Itoa(n)
	}
	return strings.Join(parts, ", ")
}

// bodyIndent prefixes every line of text written by people or agents, so no
// such line starts at the left margin, where only omo's own lines are.
const bodyIndent = "    "

// printBody prints text with every line, empty ones included, indented by
// bodyIndent. Carriage returns count as line breaks, so none can return to
// the left margin inside a line; every other character textsafe.IsHidden
// names except the tab is printed as \u{N}, so nothing in the text can
// steer the reader's terminal or hide or reorder what it reads.
func printBody(w io.Writer, text string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		fmt.Fprintln(w, bodyIndent+visibleLine(line))
	}
}

// visibleLine writes every character textsafe.IsHidden names, except the
// tab, as \u{N} (upper-case hexadecimal, as the board shows them).
func visibleLine(s string) string {
	hidden := func(r rune) bool { return r != '\t' && textsafe.IsHidden(r) }
	if !strings.ContainsFunc(s, hidden) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if hidden(r) {
			fmt.Fprintf(&b, `\u{%X}`, r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// OneLine returns s as one line for printing after bodyIndent: line breaks
// and the other characters textsafe.IsHidden names become spaces, so
// nothing in s can start a line of its own, move the cursor or hide or
// reorder text.
func OneLine(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if textsafe.IsHidden(r) {
			return ' '
		}
		return r
	}, s))
}

// showTicket prints a ticket as a block of "key: value" lines, a blank line
// and the description, indented.
func showTicket(w io.Writer, t api.Ticket) {
	fmt.Fprintf(w, "#%d %s: %s\n", t.Number, t.Type, OneLine(t.Title))
	fmt.Fprintf(w, "column: %s\n", t.Column)
	if t.Resolution != "" {
		fmt.Fprintf(w, "resolution: %s\n", t.Resolution)
	}
	if t.Story != nil {
		fmt.Fprintf(w, "story: #%d\n", *t.Story)
	} else {
		fmt.Fprintln(w, "story: none")
	}
	projects := make([]string, len(t.Projects))
	for i, p := range t.Projects {
		projects[i] = OneLine(p.Project)
	}
	fmt.Fprintf(w, "projects: %s\n", orNone(strings.Join(projects, ", ")))
	for _, p := range t.Projects {
		fmt.Fprintf(w, "base branch (%s): %s\n", OneLine(p.Project), orNone(OneLine(p.BaseBranch)))
	}
	for _, p := range t.Projects {
		if p.EndCommit != "" {
			// What the ticket's diff ran between when it was done; git
			// diff START END in the project shows it.
			fmt.Fprintf(w, "diff commits (%s): %s..%s\n", OneLine(p.Project), OneLine(p.StartCommit), OneLine(p.EndCommit))
		}
	}
	noProject := len(t.Projects) == 0 && t.Type == api.TypeTask
	if noProject {
		fmt.Fprintf(w, "working folder: %s\n", orNone(OneLine(t.Workdir)))
	}
	fmt.Fprintf(w, "supervisor: %s\n", t.Supervisor)
	fmt.Fprintf(w, "created by: %s\n", t.CreatedBy.Role)
	fmt.Fprintf(w, "model: %s\n", orNone(OneLine(t.Model)))
	fmt.Fprintf(w, "review: %s\n", t.EffectiveReview)
	if noProject {
		fmt.Fprintln(w, "when approved: done (nothing to merge or export)")
	} else {
		fmt.Fprintf(w, "when approved: %s\n", t.EffectiveWhenApproved)
	}
	if t.EffectiveTaskRelease != nil {
		fmt.Fprintf(w, "task release: %s\n", *t.EffectiveTaskRelease)
	}
	if t.ReviewStage != "" {
		fmt.Fprintf(w, "review stage: %s (round %d)\n", t.ReviewStage, t.ReviewRound)
	}
	if t.ReviewStage == api.ReviewStageAwaitingMerge {
		fmt.Fprintln(w, "export status: exported, waiting for the merge")
		for _, p := range t.Projects {
			if p.ExportCommit != "" {
				fmt.Fprintf(w, "export target (%s): %s %s -> %s (commit %s)\n", OneLine(p.Project), OneLine(p.ExportRemote), OneLine(p.ExportBranch), OneLine(p.ExportBase), OneLine(p.ExportCommit))
			}
		}
	}
	if t.Attention != "" {
		fmt.Fprintln(w, "attention:")
		fmt.Fprintln(w, bodyIndent+OneLine(t.Attention))
	}
	if t.ExportURL != "" {
		fmt.Fprintf(w, "export (merged: %t):\n", t.ExportMerged)
		for _, u := range strings.Split(t.ExportURL, "\n") {
			fmt.Fprintln(w, bodyIndent+OneLine(u))
		}
	}
	fmt.Fprintf(w, "depends on: %s\n", numbers(t.DependsOn))
	fmt.Fprintf(w, "blocks: %s\n", numbers(t.Blocks))
	waiting := make([]string, len(t.WaitingFor))
	for i, x := range t.WaitingFor {
		waiting[i] = fmt.Sprintf("#%d (%s)", x.Ticket, x.Reason)
	}
	fmt.Fprintf(w, "waiting for: %s\n", orNone(strings.Join(waiting, ", ")))
	fmt.Fprintf(w, "open questions: %d (%d for the user)\n", t.OpenQuestions, t.QuestionsForUser)
	if t.Progress != nil {
		fmt.Fprintf(w, "progress: %d/%d tasks done\n", t.Progress.Done, t.Progress.Total)
	}
	agents := make([]string, len(t.Agents))
	for i, a := range t.Agents {
		agents[i] = fmt.Sprintf("%s %q %s", a.Role, a.Name, a.State)
	}
	fmt.Fprintf(w, "agents: %s\n", orNone(strings.Join(agents, "; ")))
	fmt.Fprintf(w, "comments: omo ai comments %d\n", t.Number)
	fmt.Fprintf(w, "description version: %d\n\n", t.DescriptionVersion)
	if strings.TrimSpace(t.Description) == "" {
		fmt.Fprintln(w, "(no description)")
		return
	}
	printBody(w, t.Description)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// unreachableLines prints the notice for roles nobody is behind.
func unreachableLines(w io.Writer, n int, roles []api.Role) {
	for _, r := range roles {
		fmt.Fprintf(w, "nobody is currently behind @%s on #%d; the comment is stored but no agent was notified\n", r, n)
	}
}
