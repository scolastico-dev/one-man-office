package aicmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

type noInput struct{}

type waitIn struct {
	Timeout string `json:"timeout,omitempty" flag:"timeout" default:"10m" help:"longest time to wait, for example 90s or 10m" jsonschema:"longest time to wait, for example 90s or 10m"`
}

type doneIn struct {
	Approve bool    `json:"approve,omitempty" flag:"approve" help:"reviewers: approve the work" jsonschema:"reviewers: approve the work"`
	Reject  bool    `json:"reject,omitempty" flag:"reject" help:"reviewers: reject the work (needs a comment)" jsonschema:"reviewers: reject the work (needs a comment saying what to fix)"`
	URL     *string `json:"url,omitempty" flag:"url" help:"export agents: the address of what you published" jsonschema:"export jobs: the address of what you published"`
	Failed  *string `json:"failed,omitempty" flag:"failed" stdin:"give the message as --failed MESSAGE, or on standard input with --failed -" help:"export agents: why the export failed (- reads it from standard input)" jsonschema:"export jobs: why the export failed"`
	Comment *string `json:"comment,omitempty" arg:"0..." text:"comment-file,empty" help:"read the comment from FILE, or from standard input with -" jsonschema:"a comment on the hand-in or verdict (Markdown)"`
}

// check refuses combinations of a hand-in that cannot go together.
func (in doneIn) check() error {
	if in.Approve && in.Reject {
		return fmt.Errorf("give either --approve or --reject, not both")
	}
	if in.URL == nil && in.Failed == nil {
		return nil
	}
	switch {
	case in.URL != nil && *in.URL == "", in.Failed != nil && *in.Failed == "":
		return fmt.Errorf("--url and --failed need a value")
	case in.URL != nil && in.Failed != nil:
		return fmt.Errorf("give either --url or --failed, not both")
	case in.Approve || in.Reject:
		return fmt.Errorf("--url and --failed cannot be combined with --approve or --reject")
	case in.Comment != nil:
		return fmt.Errorf("--url and --failed take no comment")
	}
	return nil
}

type projectListIn struct {
	All bool `json:"all,omitempty" flag:"all" help:"include disabled projects" jsonschema:"include disabled projects"`
}

type projectSearchIn struct {
	Query string `json:"query" arg:"0" jsonschema:"text the project's name or path contains"`
}

func miscCommands() []Command {
	return []Command{
		Def("ready", "Tell omo this agent is up and print the instructions for its work", agentSession, ready).help("ready",
			"Announce that the agent is ready and print the prompt that tells it what to do.",
			"  omo ai ready"),
		Def("whoami", "Print who you are on the board and which ticket you work on", everyone, whoami).help("whoami",
			"Print your role, name and own ticket (the user has no ticket).",
			"  omo ai whoami"),
		// It blocks for up to a day: the command line only.
		Def("wait", "Block until omo has something for you or the timeout passes, then print why", nil, wait).help("wait [--timeout DUR]",
			`Wait for the next event meant for you (an answer, a review result, a released
dependency). Prints the reason it returned. DUR is a duration such as 90s or 10m.`,
			"  omo ai wait --timeout 10m").commandLineOnly(),
		Def("done", "Hand in your work on your ticket (reviewers give a verdict)", handsIn, done).help("done [--approve|--reject|--url URL|--failed MESSAGE|-] [--comment-file FILE|-] [TEXT...|-]",
			`Report that your work is finished and print the ticket. Reviewers must pass
--approve or --reject; a rejection needs a comment saying what to fix. The
comment is the words after the flags, or a file, or standard input with a
lone - or --comment-file -. An export agent passes --url with the address of
what it published, or --failed with a message saying why it could not
(--failed - reads it from standard input); both stand alone, without
--approve, --reject or a comment.`,
			`  omo ai done "Retries implemented, tests pass"
  omo ai done --reject "The retry loop never stops on a 4xx answer"
  omo ai done --url https://github.com/org/repo/pull/7
  omo ai done --failed "no permission to push"`),
		Def("export-text", "Export jobs: have omo check title.txt and body.md for the pull request", exportJob, exportText).help("export-text",
			`Check the pull request's title and body that you wrote into title.txt and
body.md in your folder. omo copies each one it can use to pr-title.txt and
pr-body.md, which the command in your instructions sends; for one it cannot use
(missing, empty, too long, naming an omo ticket) its own text stays there and
the reason is printed. Fix the file and run it again if you like.`,
			"  omo ai export-text"),
		Def("project list", "List projects, one per line: id name path and branches", everyone, projectList).help("list [--all]",
			`List the registered projects as "<id> <name> <path> [on <branch>, default <branch>]".
The id is what --project takes. "on" names the branch checked out in the
project folder, which a new ticket is based on; "default" the repository's
default branch. A project whose folder is not on its default branch is
marked "not the default branch"; a branch omo cannot tell is left out.
Disabled projects are only listed with --all and are marked.`,
			"  omo ai project list"),
		Def("project search", "Find projects whose name or path contains Q", everyone, projectSearch).help("search Q",
			`List the projects matching Q in the same form as "project list".`,
			"  omo ai project search api"),
		Def("model list", "List models, one per line: id name (cli) description", everyone, modelList).help("list",
			`List the models that can be set on a ticket with --model, as
"<id> <name> (<cli>) <description>". Agents only see selectable models.`,
			"  omo ai model list"),
	}
}

func ready(ctx context.Context, c *apiclient.Client, _ noInput) (Result, error) {
	prompt, err := c.Ready(ctx)
	if err != nil {
		return Result{}, err
	}
	return last(c, prompt+"\n"), nil
}

func whoami(ctx context.Context, c *apiclient.Client, _ noInput) (Result, error) {
	me, err := c.Me(ctx)
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) { printMe(w, me) })), nil
}

func wait(ctx context.Context, c *apiclient.Client, in waitIn) (Result, error) {
	d, err := time.ParseDuration(in.Timeout)
	if err != nil || d <= 0 {
		return Result{}, fmt.Errorf("--timeout %q is not a duration such as 90s or 10m", in.Timeout)
	}
	reason, err := c.Wait(ctx, in.Timeout)
	if err != nil {
		return Result{}, err
	}
	return last(c, reason+"\n"), nil
}

func done(ctx context.Context, c *apiclient.Client, in doneIn) (Result, error) {
	if in.URL != nil || in.Failed != nil {
		req := api.DoneRequest{}
		if in.URL != nil {
			req.URL = *in.URL
		}
		if in.Failed != nil {
			req.Error = *in.Failed
		}
		t, err := c.Done(ctx, req)
		if err != nil {
			return Result{}, err
		}
		return summaryLine(c, t), nil
	}
	req := api.DoneRequest{}
	if in.Comment != nil {
		req.Comment = *in.Comment
	}
	switch {
	case in.Approve:
		req.Verdict = "approve"
	case in.Reject:
		req.Verdict = "reject"
	}
	t, err := c.Done(ctx, req)
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) {
		fmt.Fprintln(w, ticketLine(t.TicketSummary))
		if t.ReviewStage != "" {
			fmt.Fprintf(w, "review stage: %s (round %d)\n", t.ReviewStage, t.ReviewRound)
		}
	})), nil
}

func exportText(ctx context.Context, c *apiclient.Client, _ noInput) (Result, error) {
	out, err := c.ExportText(ctx)
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) { printExportText(w, out) })), nil
}

func projectList(ctx context.Context, c *apiclient.Client, in projectListIn) (Result, error) {
	return listProjects(ctx, c, "", in.All)
}

func projectSearch(ctx context.Context, c *apiclient.Client, in projectSearchIn) (Result, error) {
	return listProjects(ctx, c, in.Query, false)
}

func listProjects(ctx context.Context, c *apiclient.Client, q string, all bool) (Result, error) {
	projects, err := c.Projects(ctx, q, all)
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) {
		if len(projects) == 0 {
			fmt.Fprintln(w, "no projects")
		}
		for _, p := range projects {
			fmt.Fprintln(w, projectLine(p))
		}
	})), nil
}

func modelList(ctx context.Context, c *apiclient.Client, _ noInput) (Result, error) {
	models, err := c.Models(ctx)
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) {
		if len(models) == 0 {
			fmt.Fprintln(w, "no models")
		}
		for _, m := range models {
			fmt.Fprintln(w, modelLine(m))
		}
	})), nil
}

// printExportText prints the answer of export-text.
func printExportText(w io.Writer, t api.ExportText) {
	fmt.Fprintf(w, "title (%s): %s\n", textFrom(t.TitleFrom), OneLine(t.Title))
	fmt.Fprintf(w, "body (%s): %d bytes\n", textFrom(t.BodyFrom), t.BodyBytes)
	for _, p := range t.Problems {
		fmt.Fprintf(w, "not used: %s\n", OneLine(p))
	}
}

func textFrom(from string) string {
	if from == api.TextFromAgent {
		return "yours"
	}
	return "omo's own"
}

// printMe prints the answer of whoami.
func printMe(w io.Writer, me api.Me) {
	fmt.Fprintf(w, "role: %s\n", me.Actor.Role)
	fmt.Fprintf(w, "name: %s\n", orNone(OneLine(me.Actor.Name)))
	if me.Ticket != nil {
		fmt.Fprintf(w, "ticket: #%d\n", *me.Ticket)
	} else {
		fmt.Fprintln(w, "ticket: none")
	}
}

// modelLine is the one-line form of a model; its text comes from the
// settings files and goes through oneLine.
func modelLine(m api.Model) string {
	line := fmt.Sprintf("%s %s (%s)", OneLine(m.ID), OneLine(m.Name), OneLine(m.CLI))
	if m.Description != "" {
		line += " " + OneLine(m.Description)
	}
	if !m.Selectable {
		line += " [not selectable]"
	}
	return line
}

// projectLine is the one-line form of a project. Names and paths can come
// from folder names an agent made: they go through oneLine.
func projectLine(p api.Project) string {
	line := fmt.Sprintf("%s %s %s", OneLine(p.ID), OneLine(p.Name), OneLine(p.Path))
	if !p.Enabled {
		line += " [disabled]"
	}
	if p.Missing {
		line += " [missing]"
	}
	return line + branchNote(p)
}

// branchNote describes a project's current and default branch, or returns
// "" when neither is known.
func branchNote(p api.Project) string {
	var parts []string
	if p.CurrentBranch != "" {
		parts = append(parts, "on "+OneLine(p.CurrentBranch))
	}
	if p.DefaultBranch != "" {
		parts = append(parts, "default "+OneLine(p.DefaultBranch))
	}
	if len(parts) == 0 {
		return ""
	}
	note := " [" + strings.Join(parts, ", ")
	if p.CurrentBranch != "" && p.DefaultBranch != "" && p.CurrentBranch != p.DefaultBranch {
		note += ": not the default branch"
	}
	return note + "]"
}
