package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func addAITicketCommands(ai *cobra.Command) {
	ticket := &cobra.Command{
		Use:     "ticket",
		Short:   "Read, create, update and move tickets",
		Example: "  omo ai ticket show 12",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	ticket.AddCommand(&cobra.Command{
		Use:   "show [N]",
		Short: "Print one ticket with its description (default: your own ticket)",
		Long: `Print a ticket: a block of "key: value" lines, a blank line, then the
description, indented by four spaces. Without N, an agent sees the ticket it
works on. Every image in the description is followed by the local path of its
file.`,
		Example: "  omo ai ticket show 12",
		Args:    cobra.MaximumNArgs(1),
		RunE: aiRun(func(a *aiCall, args []string) error {
			var n int
			if len(args) == 1 {
				var err error
				if n, err = ticketNumber(args[0]); err != nil {
					return err
				}
			} else {
				me, err := a.client.Me(a.ctx)
				if err != nil {
					return err
				}
				if me.Ticket == nil {
					return fmt.Errorf("no ticket number given and you have no ticket of your own (use: omo ai ticket show N)")
				}
				n = *me.Ticket
			}
			t, err := a.client.Ticket(a.ctx, n)
			if err != nil {
				return err
			}
			t.Description = a.withImagePaths(t.Description)
			return a.emit(func(w io.Writer) { showTicket(w, t) })
		}),
	})

	list := &cobra.Command{
		Use:   "list",
		Short: "List tickets, one per line: #N type column title",
		Long: `List the tickets you may read, one per line as "#<number> <type> <column> <title>".
Filters combine. Done tickets are limited to the most recent ones.`,
		Example: "  omo ai ticket list --column ready --type task",
		Args:    cobra.NoArgs,
		RunE: aiRun(func(a *aiCall, _ []string) error {
			f := api.TicketFilter{}
			flags := a.cmd.Flags()
			column, _ := flags.GetString("column")
			typ, _ := flags.GetString("type")
			f.Column, f.Type = api.Column(column), api.TicketType(typ)
			f.Project, _ = flags.GetString("project")
			f.Search, _ = flags.GetString("search")
			if flags.Changed("story") {
				s, _ := flags.GetInt("story")
				f.Story = &s
			}
			if sup, _ := flags.GetString("supervisor"); sup != "" {
				switch api.Role(sup) {
				case api.RoleUser, api.RoleCEO, api.RolePM:
					f.Supervisor = api.Role(sup)
				default:
					return fmt.Errorf("--supervisor %q is not user, ceo or pm", sup)
				}
			}
			res, err := a.client.Tickets(a.ctx, f)
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) {
				if len(res.Tickets) == 0 {
					fmt.Fprintln(w, "no tickets")
				}
				for _, t := range res.Tickets {
					fmt.Fprintln(w, ticketLine(t))
				}
				if res.NextDoneBefore > 0 {
					fmt.Fprintln(w, "older done tickets exist but are not listed")
				}
			})
		}),
	}
	list.Flags().String("column", "", "only this column: planning, ready, in_progress, review or done")
	list.Flags().String("type", "", "only this type: story or task")
	list.Flags().Int("story", 0, "only the tasks of this story")
	list.Flags().String("project", "", "only tickets of this project")
	list.Flags().String("supervisor", "", "only tickets this role supervises: user, ceo or pm")
	list.Flags().String("search", "", "only tickets whose title or description contain this text")
	ticket.AddCommand(list)

	create := &cobra.Command{
		Use:   "create --type story|task --title TITLE [flags]",
		Short: "Create a ticket and print it as #N type column title",
		Long: `Create a story or a task. The description comes from a file, or from standard
input with --description-file - (no shell quoting needed).`,
		Example: `  omo ai ticket create --type task --title "Add retry" --project api --story 3 --description-file - <<'EOF'
  Retry failed uploads three times.
  EOF`,
		Args: cobra.NoArgs,
		RunE: aiRun(func(a *aiCall, _ []string) error {
			flags := a.cmd.Flags()
			typ, _ := flags.GetString("type")
			title, _ := flags.GetString("title")
			file, _ := flags.GetString("description-file")
			req := api.CreateTicket{Type: api.TicketType(typ), Title: title}
			if file != "" {
				text, err := readBody(a.cmd, "description-file", file, nil, true)
				if err != nil {
					return err
				}
				req.Description = text
			}
			req.Projects, _ = flags.GetStringArray("project")
			if flags.Changed("story") {
				s, _ := flags.GetInt("story")
				req.Story = &s
			}
			req.Model, _ = flags.GetString("model")
			req.DependsOn, _ = flags.GetIntSlice("depends-on")
			t, err := a.client.CreateTicket(a.ctx, req)
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) { fmt.Fprintln(w, ticketLine(t.TicketSummary)) })
		}),
	}
	create.Flags().String("type", "", "story or task (required)")
	create.Flags().String("title", "", "ticket title (required)")
	create.Flags().String("description-file", "", "read the description from FILE, or from standard input with -")
	create.Flags().StringArray("project", nil, "project id; repeat for several projects")
	create.Flags().Int("story", 0, "the story a task belongs to")
	create.Flags().String("model", "", "model id for the agent that works on it")
	create.Flags().IntSlice("depends-on", nil, "ticket number this one depends on; repeat for several")
	_ = create.MarkFlagRequired("type")
	_ = create.MarkFlagRequired("title")
	ticket.AddCommand(create)

	update := &cobra.Command{
		Use:   "update N [flags]",
		Short: "Change a ticket's title, description, model or projects",
		Long: `Change the given fields of ticket N and print it as #N type column title.
The description comes from a file, or from standard input with
--description-file -. --model "" returns to the default model.
--base-version N (the "description version:" of ticket show) refuses the new
description if someone changed it since you read version N. The description
is stored first; if the other changes are then refused, the error says so.`,
		Example: `  omo ai ticket update 12 --title "Add retry to uploads" --model fast
  omo ai ticket update 12 --base-version 3 --description-file - <<'EOF'
  Retry failed uploads three times.
  EOF`,
		Args: cobra.ExactArgs(1),
		RunE: aiRun(func(a *aiCall, args []string) error {
			n, err := ticketNumber(args[0])
			if err != nil {
				return err
			}
			flags := a.cmd.Flags()
			var patch api.UpdateTicket
			changed := false
			if flags.Changed("title") {
				v, _ := flags.GetString("title")
				patch.Title, changed = api.Some(v), true
			}
			if flags.Changed("model") {
				v, _ := flags.GetString("model")
				if v == "" {
					patch.Model = api.Null[string]()
				} else {
					patch.Model = api.Some(v)
				}
				changed = true
			}
			if flags.Changed("project") {
				v, _ := flags.GetStringArray("project")
				patch.Projects, changed = api.Some(v), true
			}
			file, _ := flags.GetString("description-file")
			if !changed && file == "" {
				return fmt.Errorf("nothing to update: give --title, --description-file, --model or --project")
			}
			base := 0
			if flags.Changed("base-version") {
				if file == "" {
					return fmt.Errorf("--base-version only applies with --description-file")
				}
				base, _ = flags.GetInt("base-version")
				if base < 1 {
					return fmt.Errorf("--base-version needs the version from \"description version:\" in ticket show, at least 1; a ticket without a description cannot be checked")
				}
			}
			var described *api.DescriptionVersion
			if file != "" {
				text, err := readBody(a.cmd, "description-file", file, nil, true)
				if err != nil {
					return err
				}
				v, err := a.client.SetDescription(a.ctx, n, api.DescriptionUpdate{Text: text, BaseVersion: base})
				if err != nil {
					return err
				}
				described = &v
			}
			if changed {
				if _, err := a.client.UpdateTicket(a.ctx, n, patch); err != nil {
					if described != nil {
						return fmt.Errorf("the description of #%d was changed (now version %d), but the other changes were refused: %w",
							n, described.Number, explain(err))
					}
					return err
				}
			}
			t, err := a.client.Ticket(a.ctx, n)
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) { fmt.Fprintln(w, ticketLine(t.TicketSummary)) })
		}),
	}
	update.Flags().String("title", "", "new title")
	update.Flags().String("description-file", "", "read the new description from FILE, or from standard input with -")
	update.Flags().Int("base-version", 0, "the description version you last read; a newer one refuses the change")
	update.Flags().String("model", "", "model id (empty: default model)")
	update.Flags().StringArray("project", nil, "replace the projects; repeat for several")
	ticket.AddCommand(update)

	ticket.AddCommand(&cobra.Command{
		Use:   "move N COLUMN",
		Short: "Move ticket N to a column (planning, ready, in_progress, review, done)",
		Long: `Move a ticket to another column and print it as #N type column title. Only the
moves the board rules allow are accepted; a refusal says why.`,
		Example: "  omo ai ticket move 12 ready",
		Args:    cobra.ExactArgs(2),
		RunE: aiRun(func(a *aiCall, args []string) error {
			n, err := ticketNumber(args[0])
			if err != nil {
				return err
			}
			t, err := a.client.Move(a.ctx, n, api.MoveRequest{Column: api.Column(strings.TrimSpace(args[1]))})
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) { fmt.Fprintln(w, ticketLine(t.TicketSummary)) })
		}),
	})
	retry := &cobra.Command{
		Use:   "retry N",
		Short: "Retry the failed step of ticket N and print it as #N type column title",
		Long: `Start again the step of ticket N that stopped and needs a person: its agent
start, an agent that ended without handing in, or its merge. The user retries
every ticket, the CEO tickets it supervises.`,
		Example: "  omo ai ticket retry 12",
		Args:    cobra.ExactArgs(1),
		RunE: aiRun(func(a *aiCall, args []string) error {
			n, err := ticketNumber(args[0])
			if err != nil {
				return err
			}
			t, err := a.client.Retry(a.ctx, n)
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) { fmt.Fprintln(w, ticketLine(t.TicketSummary)) })
		}),
	}
	ticket.AddCommand(retry)
	cancel := &cobra.Command{
		Use:   "cancel N [--delete-branch [--discard-uncommitted]]",
		Short: "Cancel ticket N and print it as #N type column title",
		Long: `Close ticket N as cancelled; cancelling a story also cancels its open tasks.
The user cancels every ticket, the CEO tickets it created, a product manager
tasks it created in its story. --delete-branch asks omo to also delete the ticket's
work branch. A worktree with uncommitted changes keeps the branch; only the
user may add --discard-uncommitted to throw those changes away.`,
		Example: "  omo ai ticket cancel 12 --delete-branch",
		Args:    cobra.ExactArgs(1),
		RunE: aiRun(func(a *aiCall, args []string) error {
			n, err := ticketNumber(args[0])
			if err != nil {
				return err
			}
			del, _ := a.cmd.Flags().GetBool("delete-branch")
			discard, _ := a.cmd.Flags().GetBool("discard-uncommitted")
			if discard && !del {
				return fmt.Errorf("--discard-uncommitted only applies together with --delete-branch")
			}
			t, err := a.client.Cancel(a.ctx, n, api.CancelRequest{DeleteBranch: del, DiscardUncommitted: discard})
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) { fmt.Fprintln(w, ticketLine(t.TicketSummary)) })
		}),
	}
	cancel.Flags().Bool("delete-branch", false, "also delete the ticket's work branch")
	cancel.Flags().Bool("discard-uncommitted", false, "with --delete-branch: also discard uncommitted changes in the worktree (user only)")
	ticket.AddCommand(cancel)
	ai.AddCommand(ticket)

	link := &cobra.Command{
		Use:   "link N --depends-on M [--remove]",
		Short: "Make ticket N depend on ticket M (or remove that dependency)",
		Long: `Make ticket N wait for ticket M, or with --remove drop that dependency. A
dependency that would form a cycle is refused and the cycle is named.`,
		Example: "  omo ai link 12 --depends-on 11",
		Args:    cobra.ExactArgs(1),
		RunE: aiRun(func(a *aiCall, args []string) error {
			n, err := ticketNumber(args[0])
			if err != nil {
				return err
			}
			if !a.cmd.Flags().Changed("depends-on") {
				return fmt.Errorf("--depends-on M is required")
			}
			m, _ := a.cmd.Flags().GetInt("depends-on")
			if m < 1 {
				return fmt.Errorf("--depends-on needs a ticket number")
			}
			if remove, _ := a.cmd.Flags().GetBool("remove"); remove {
				if _, err := a.client.RemoveDependency(a.ctx, n, m); err != nil {
					return err
				}
				return a.emit(func(w io.Writer) { fmt.Fprintf(w, "#%d no longer depends on #%d\n", n, m) })
			}
			if _, err := a.client.AddDependency(a.ctx, n, m); err != nil {
				return err
			}
			return a.emit(func(w io.Writer) { fmt.Fprintf(w, "#%d now depends on #%d\n", n, m) })
		}),
	}
	link.Flags().Int("depends-on", 0, "the ticket N depends on (required)")
	link.Flags().Bool("remove", false, "remove the dependency instead of adding it")
	ai.AddCommand(link)
}
