package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// ticketLine is the one-line form of a ticket: "#<n> <type> <column> <title>".
func ticketLine(t api.TicketSummary) string {
	return fmt.Sprintf("#%d %s %s %s", t.Number, t.Type, t.Column, t.Title)
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

// showTicket prints a ticket as a block of "key: value" lines, a blank line
// and the description.
func showTicket(w io.Writer, t api.Ticket) {
	fmt.Fprintf(w, "#%d %s: %s\n", t.Number, t.Type, t.Title)
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
		projects[i] = p.Project
	}
	fmt.Fprintf(w, "projects: %s\n", orNone(strings.Join(projects, ", ")))
	fmt.Fprintf(w, "supervisor: %s\n", t.Supervisor)
	fmt.Fprintf(w, "created by: %s\n", t.CreatedBy.Role)
	fmt.Fprintf(w, "model: %s\n", orNone(t.Model))
	fmt.Fprintf(w, "review: %s\n", t.EffectiveReview)
	fmt.Fprintf(w, "when approved: %s\n", t.EffectiveWhenApproved)
	if t.EffectiveTaskRelease != nil {
		fmt.Fprintf(w, "task release: %s\n", *t.EffectiveTaskRelease)
	}
	if t.ReviewStage != "" {
		fmt.Fprintf(w, "review stage: %s (round %d)\n", t.ReviewStage, t.ReviewRound)
	}
	if t.Attention != "" {
		fmt.Fprintf(w, "attention: %s\n", t.Attention)
	}
	if t.ExportURL != "" {
		fmt.Fprintf(w, "export: %s (merged: %t)\n", t.ExportURL, t.ExportMerged)
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
	fmt.Fprintf(w, "description version: %d\n\n", t.DescriptionVersion)
	if strings.TrimSpace(t.Description) == "" {
		fmt.Fprintln(w, "(no description)")
		return
	}
	fmt.Fprintln(w, strings.TrimRight(t.Description, "\n"))
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
