package aicmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

type ticketShowIn struct {
	Ticket int `json:"ticket,omitempty" arg:"0,optional" jsonschema:"the ticket number; leave it out for your own ticket"`
}

type ticketListIn struct {
	Column     string `json:"column,omitempty" flag:"column" help:"only this column: planning, ready, in_progress, review or done" jsonschema:"only this column: planning, ready, in_progress, review or done"`
	Type       string `json:"type,omitempty" flag:"type" help:"only this type: story or task" jsonschema:"only this type: story or task"`
	Story      *int   `json:"story,omitempty" flag:"story" help:"only the tasks of this story" jsonschema:"only the tasks of this story"`
	Project    string `json:"project,omitempty" flag:"project" help:"only tickets of this project" jsonschema:"only tickets of this project (its id)"`
	Supervisor string `json:"supervisor,omitempty" flag:"supervisor" help:"only tickets this role supervises: user, ceo or pm" jsonschema:"only tickets this role supervises: user, ceo or pm"`
	Search     string `json:"search,omitempty" flag:"search" help:"only tickets whose title or description contain this text" jsonschema:"only tickets whose title or description contain this text"`
}

type ticketCreateIn struct {
	Type        string   `json:"type" flag:"type" required:"true" help:"story or task (required)" jsonschema:"story or task"`
	Title       string   `json:"title" flag:"title" required:"true" help:"ticket title (required)" jsonschema:"the ticket title"`
	Description string   `json:"description,omitempty" text:"description-file,empty" help:"read the description from FILE, or from standard input with -" jsonschema:"the description (Markdown)"`
	Projects    []string `json:"projects,omitempty" flag:"project" help:"project id; repeat for several projects" jsonschema:"the project ids; a task has one, or none with no_project"`
	NoProject   bool     `json:"no_project,omitempty" flag:"no-project" help:"create a task without a project (research, an investigation, a local experiment)" jsonschema:"create a task without a project (research, an investigation, a local experiment)"`
	Story       *int     `json:"story,omitempty" flag:"story" help:"the story a task belongs to" jsonschema:"the story a task belongs to"`
	Model       string   `json:"model,omitempty" flag:"model" help:"model id for its agent; leave unset so omo uses the role's configured models, set it only on request or for a reason you state on the ticket" jsonschema:"model id for its agent; leave it out so omo uses the role's configured models, set it only on request or for a reason you state on the ticket"`
	DependsOn   []int    `json:"depends_on,omitempty" flag:"depends-on" help:"ticket number this one depends on; repeat for several" jsonschema:"ticket numbers this one depends on"`
}

type ticketUpdateIn struct {
	Ticket      int       `json:"ticket" arg:"0" jsonschema:"the ticket number"`
	Title       *string   `json:"title,omitempty" flag:"title" help:"new title" jsonschema:"the new title"`
	Description *string   `json:"description,omitempty" text:"description-file,empty" help:"read the new description from FILE, or from standard input with -" jsonschema:"the new description (Markdown)"`
	BaseVersion *int      `json:"base_version,omitempty" flag:"base-version" help:"the description version you last read; a newer one refuses the change" jsonschema:"the description version you last read; a newer one refuses the change"`
	Model       *string   `json:"model,omitempty" flag:"model" help:"model id for its agent (\"\": the role's configured models); set it only on request or for a reason you state on the ticket" jsonschema:"model id for its agent (empty: the role's configured models); set it only on request or for a reason you state on the ticket"`
	Projects    *[]string `json:"projects,omitempty" flag:"project" help:"replace the projects; repeat for several" jsonschema:"replace the projects"`
	NoProject   bool      `json:"no_project,omitempty" flag:"no-project" help:"make the task one without a project" jsonschema:"make the task one without a project"`
}

// check refuses what needs no server: conflicting or missing changes.
func (in ticketUpdateIn) check() error {
	changed := in.Title != nil || in.Model != nil
	switch {
	case in.NoProject && in.Projects != nil:
		return fmt.Errorf("--no-project and --project exclude each other")
	case in.NoProject, in.Projects != nil:
		changed = true
	}
	if !changed && in.Description == nil {
		return fmt.Errorf("nothing to update: give --title, --description-file, --model, --project or --no-project")
	}
	if in.BaseVersion != nil {
		if in.Description == nil {
			return fmt.Errorf("--base-version only applies with --description-file")
		}
		if *in.BaseVersion < 1 {
			return fmt.Errorf("--base-version needs the version from \"description version:\" in ticket show, at least 1; a ticket without a description cannot be checked")
		}
	}
	return nil
}

type ticketMoveIn struct {
	Ticket int    `json:"ticket" arg:"0" jsonschema:"the ticket number"`
	Column string `json:"column" arg:"1" jsonschema:"planning, ready, in_progress, review or done"`
}

type ticketIn struct {
	Ticket int `json:"ticket" arg:"0" jsonschema:"the ticket number"`
}

type ticketCancelIn struct {
	Ticket             int  `json:"ticket" arg:"0" jsonschema:"the ticket number"`
	DeleteBranch       bool `json:"delete_branch,omitempty" flag:"delete-branch" help:"also delete the ticket's work branch" jsonschema:"also delete the ticket's work branch"`
	DiscardUncommitted bool `json:"discard_uncommitted,omitempty" flag:"discard-uncommitted" help:"with --delete-branch: also discard uncommitted changes in the worktree (user only)" jsonschema:"with delete_branch: also discard uncommitted changes in the worktree (user only)"`
}

type ticketRemoveProjectIn struct {
	Ticket  int    `json:"ticket" arg:"0" jsonschema:"the ticket number"`
	Project string `json:"project" arg:"1" jsonschema:"the project id"`
	Discard bool   `json:"discard,omitempty" flag:"discard" help:"also delete the branch's commits its base branch lacks and the worktree's uncommitted changes" jsonschema:"also delete the branch's commits its base branch lacks and the worktree's uncommitted changes; only when the user agreed to lose them"`
}

type linkIn struct {
	Ticket    int  `json:"ticket" arg:"0" jsonschema:"the ticket that depends"`
	DependsOn *int `json:"depends_on" flag:"depends-on" help:"the ticket N depends on (required)" jsonschema:"the ticket it depends on"`
	Remove    bool `json:"remove,omitempty" flag:"remove" help:"remove the dependency instead of adding it" jsonschema:"remove the dependency instead of adding it"`
}

func ticketCommands() []Command {
	return []Command{
		Def("ticket show", "Print one ticket with its description (default: your own ticket)", everyone, ticketShow).help("show [N]",
			`Print a ticket: a block of "key: value" lines, a blank line, then the
description, indented by four spaces. Without N, an agent sees the ticket it
works on. Every image in the description is followed by the local path of its
file.`,
			"  omo ai ticket show 12"),
		Def("ticket list", "List tickets, one per line: #N type column title", everyone, ticketList).help("list",
			`List the tickets you may read, one per line as "#<number> <type> <column> <title>".
Filters combine. Done tickets are limited to the most recent ones.`,
			"  omo ai ticket list --column ready --type task"),
		Def("ticket create", "Create a ticket and print it as #N type column title", everyone, ticketCreate).help("create --type story|task --title TITLE [flags]",
			`Create a story or a task. The description comes from a file, or from standard
input with --description-file - (no shell quoting needed).

A task has one project (--project), or none with --no-project: a task without
a project is for work that changes no repository, such as research, an
investigation across repositories or a local proof of concept. Its agent works
in a working folder omo makes for it (and keeps after the task is done), may
read the projects but not change them, and hands in its findings; approving it
merges and exports nothing. Code changes to a project need a task with that
project.`,
			`  omo ai ticket create --type task --title "Add retry" --project api --story 3 --description-file - <<'EOF'
  Retry failed uploads three times.
  EOF
  omo ai ticket create --type task --no-project --title "Compare the upload libraries" --description-file - <<'EOF'
  Which of the two libraries retries uploads, and how? Write the findings as comments.
  EOF`),
		Def("ticket update", "Change a ticket's title, description, model or projects", everyone, ticketUpdate).help("update N [flags]",
			`Change the given fields of ticket N and print it as #N type column title.
The description comes from a file, or from standard input with
--description-file -. --model "" returns to the models configured for the
role.
--base-version N (the "description version:" of ticket show) refuses the new
description if someone changed it since you read version N. The description
is stored first; if the other changes are then refused, the error says so.
--project replaces the projects only while the ticket has no branch yet; a
project that has one is taken off with omo ai ticket remove-project.
--no-project makes a task one without a project, under the same rule; once
work on a task without a project started, its projects stay as they are.`,
			`  omo ai ticket update 12 --title "Add retry to uploads"
  omo ai ticket update 12 --base-version 3 --description-file - <<'EOF'
  Retry failed uploads three times.
  EOF`),
		Def("ticket move", "Move ticket N to a column (planning, ready, in_progress, review, done)", everyone, ticketMove).help("move N COLUMN",
			`Move a ticket to another column and print it as #N type column title. Only the
moves the board rules allow are accepted; a refusal says why.`,
			"  omo ai ticket move 12 ready"),
		Def("ticket retry", "Retry the failed step of ticket N and print it as #N type column title", everyone, ticketRetry).help("retry N",
			`Start again the step of ticket N that stopped and needs a person: its agent
start, an agent that ended without handing in, or its merge. The user retries
every ticket, the CEO the tickets it supervises, a product manager the tasks of
its story.`,
			"  omo ai ticket retry 12"),
		Def("ticket cancel", "Cancel ticket N and print it as #N type column title", everyone, ticketCancel).help("cancel N [--delete-branch [--discard-uncommitted]]",
			`Close ticket N as cancelled; cancelling a story also cancels its open tasks.
The user cancels every ticket, the CEO tickets it created, a product manager
tasks it created in its story. --delete-branch asks omo to also delete the ticket's
work branch. A worktree with uncommitted changes keeps the branch; only the
user may add --discard-uncommitted to throw those changes away.`,
			"  omo ai ticket cancel 12 --delete-branch"),
		// The route is the user's only, and the user has no omo tools.
		Def("ticket remove-workdir", "Remove the working folder of done ticket N, a task without a project", nil, ticketRemoveWorkdir).help("remove-workdir N",
			`Remove the working folder omo made for ticket N, a task without a project,
with everything in it, and print the ticket as #N type column title. omo keeps
the folder after the task is done, so the user can look at what its agent left
there; it is removed only this way. Only the user removes it, only once the
ticket is done, and not while a terminal is open in it.`,
			"  omo ai ticket remove-workdir 12").commandLineOnly(),
		Def("ticket remove-project", "Take a project off ticket N, with the worktree and branch omo made for it", everyone, ticketRemoveProject).help("remove-project N PROJECT [--discard]",
			`Take PROJECT off ticket N and print it as #N type column title. omo removes
the worktree it made for the ticket in that project and deletes the ticket's
branch there (only a branch omo made for this ticket; any other is kept).
Agents of the ticket that only wait in that project are ended; while one works
there, the removal is refused.

Allowed while the ticket is in Planning, in Ready For Dev, or in Review waiting
for the user (also after a failed export). A ticket keeps at least one project
here: a ticket with a branch does not become one without a project (a task
without a project is created as one, or changed with omo ai ticket update
--no-project while it has no branch). A story keeps a project while a task of
it that is not done uses it; done
tasks keep the project as their history. The user removes projects from every
ticket, the CEO from tickets it created or supervises.

If the branch has commits its base branch does not contain, or the worktree has
uncommitted changes, the removal is refused and lists them. --discard deletes
them with the project; use it only when the user agreed to lose them.`,
			`  omo ai ticket remove-project 1 github.com-acme-preview
  omo ai ticket remove-project 1 github.com-acme-preview --discard`),
		Def("link", "Make ticket N depend on ticket M (or remove that dependency)", everyone, link).help("link N --depends-on M [--remove]",
			`Make ticket N wait for ticket M, or with --remove drop that dependency. A
dependency that would form a cycle is refused and the cycle is named.`,
			"  omo ai link 12 --depends-on 11"),
	}
}

// summaryLine answers a ticket as its one-line form.
func summaryLine(c *apiclient.Client, t api.Ticket) Result {
	return last(c, ticketLine(t.TicketSummary)+"\n")
}

func ticketShow(ctx context.Context, c *apiclient.Client, in ticketShowIn) (Result, error) {
	n := in.Ticket
	if n == 0 {
		me, err := c.Me(ctx)
		if err != nil {
			return Result{}, err
		}
		if me.Ticket == nil {
			return Result{}, fmt.Errorf("no ticket number given and you have no ticket of your own (use: omo ai ticket show N)")
		}
		n = *me.Ticket
	}
	t, err := c.Ticket(ctx, n)
	if err != nil {
		return Result{}, err
	}
	value := json.RawMessage(c.LastBody())
	t.Description = newImageNotes(ctx, c).annotate(t.Description)
	return Result{Text: textOf(func(w *strings.Builder) { showTicket(w, t) }), Value: value}, nil
}

func ticketList(ctx context.Context, c *apiclient.Client, in ticketListIn) (Result, error) {
	f := api.TicketFilter{Column: api.Column(in.Column), Type: api.TicketType(in.Type), Project: in.Project, Search: in.Search, Story: in.Story}
	if in.Supervisor != "" {
		switch api.Role(in.Supervisor) {
		case api.RoleUser, api.RoleCEO, api.RolePM:
			f.Supervisor = api.Role(in.Supervisor)
		default:
			return Result{}, fmt.Errorf("--supervisor %q is not user, ceo or pm", in.Supervisor)
		}
	}
	res, err := c.Tickets(ctx, f)
	if err != nil {
		return Result{}, err
	}
	return last(c, textOf(func(w *strings.Builder) {
		if len(res.Tickets) == 0 {
			fmt.Fprintln(w, "no tickets")
		}
		for _, t := range res.Tickets {
			fmt.Fprintln(w, ticketLine(t))
		}
		if res.NextDoneBefore > 0 {
			fmt.Fprintln(w, "older done tickets exist but are not listed")
		}
	})), nil
}

func ticketCreate(ctx context.Context, c *apiclient.Client, in ticketCreateIn) (Result, error) {
	req := api.CreateTicket{Type: api.TicketType(in.Type), Title: in.Title, Description: in.Description, Projects: in.Projects}
	switch {
	case in.NoProject && len(req.Projects) > 0:
		return Result{}, fmt.Errorf("--no-project and --project exclude each other")
	case req.Type == api.TypeTask && !in.NoProject && len(req.Projects) == 0:
		return Result{}, fmt.Errorf("a task needs --project <id>, or --no-project for a task without a project (research, an investigation, a local experiment)")
	case in.NoProject && req.Type != api.TypeTask:
		return Result{}, fmt.Errorf("--no-project is for tasks; a story names its projects with --project")
	}
	req.Story, req.Model, req.DependsOn = in.Story, in.Model, in.DependsOn
	t, err := c.CreateTicket(ctx, req)
	if err != nil {
		return Result{}, err
	}
	return summaryLine(c, t), nil
}

func ticketUpdate(ctx context.Context, c *apiclient.Client, in ticketUpdateIn) (Result, error) {
	n := in.Ticket
	var patch api.UpdateTicket
	changed := false
	if in.Title != nil {
		patch.Title, changed = api.Some(*in.Title), true
	}
	if in.Model != nil {
		if *in.Model == "" {
			patch.Model = api.Null[string]()
		} else {
			patch.Model = api.Some(*in.Model)
		}
		changed = true
	}
	switch {
	case in.NoProject:
		patch.Projects, changed = api.Some([]string{}), true
	case in.Projects != nil:
		patch.Projects, changed = api.Some(*in.Projects), true
	}
	var described *api.DescriptionVersion
	if in.Description != nil {
		base := 0
		if in.BaseVersion != nil {
			base = *in.BaseVersion
		}
		v, err := c.SetDescription(ctx, n, api.DescriptionUpdate{Text: *in.Description, BaseVersion: base})
		if err != nil {
			return Result{}, err
		}
		described = &v
	}
	if changed {
		if _, err := c.UpdateTicket(ctx, n, patch); err != nil {
			if described != nil {
				return Result{}, fmt.Errorf("the description of #%d was changed (now version %d), but the other changes were refused: %w",
					n, described.Number, Explain(err))
			}
			return Result{}, err
		}
	}
	t, err := c.Ticket(ctx, n)
	if err != nil {
		return Result{}, err
	}
	return summaryLine(c, t), nil
}

func ticketMove(ctx context.Context, c *apiclient.Client, in ticketMoveIn) (Result, error) {
	t, err := c.Move(ctx, in.Ticket, api.MoveRequest{Column: api.Column(strings.TrimSpace(in.Column))})
	if err != nil {
		return Result{}, err
	}
	return summaryLine(c, t), nil
}

func ticketRetry(ctx context.Context, c *apiclient.Client, in ticketIn) (Result, error) {
	t, err := c.Retry(ctx, in.Ticket)
	if err != nil {
		return Result{}, err
	}
	return summaryLine(c, t), nil
}

func ticketCancel(ctx context.Context, c *apiclient.Client, in ticketCancelIn) (Result, error) {
	if in.DiscardUncommitted && !in.DeleteBranch {
		return Result{}, fmt.Errorf("--discard-uncommitted only applies together with --delete-branch")
	}
	t, err := c.Cancel(ctx, in.Ticket, api.CancelRequest{DeleteBranch: in.DeleteBranch, DiscardUncommitted: in.DiscardUncommitted})
	if err != nil {
		return Result{}, err
	}
	return summaryLine(c, t), nil
}

func ticketRemoveWorkdir(ctx context.Context, c *apiclient.Client, in ticketIn) (Result, error) {
	t, err := c.RemoveWorkdir(ctx, in.Ticket)
	if err != nil {
		return Result{}, err
	}
	return summaryLine(c, t), nil
}

func ticketRemoveProject(ctx context.Context, c *apiclient.Client, in ticketRemoveProjectIn) (Result, error) {
	t, err := c.RemoveProject(ctx, in.Ticket, api.RemoveProjectRequest{Project: strings.TrimSpace(in.Project), Discard: in.Discard})
	if err != nil {
		return Result{}, err
	}
	return summaryLine(c, t), nil
}

func link(ctx context.Context, c *apiclient.Client, in linkIn) (Result, error) {
	if in.DependsOn == nil {
		return Result{}, fmt.Errorf("--depends-on M is required")
	}
	n, m := in.Ticket, *in.DependsOn
	if m < 1 {
		return Result{}, fmt.Errorf("--depends-on needs a ticket number")
	}
	if in.Remove {
		if _, err := c.RemoveDependency(ctx, n, m); err != nil {
			return Result{}, err
		}
		return last(c, fmt.Sprintf("#%d no longer depends on #%d\n", n, m)), nil
	}
	if _, err := c.AddDependency(ctx, n, m); err != nil {
		return Result{}, err
	}
	return last(c, fmt.Sprintf("#%d now depends on #%d\n", n, m)), nil
}
