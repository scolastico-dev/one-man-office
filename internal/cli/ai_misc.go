package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func addAIMiscCommands(ai *cobra.Command) {
	ai.AddCommand(&cobra.Command{
		Use:     "ready",
		Short:   "Tell omo this agent is up and print the instructions for its work",
		Long:    "Announce that the agent is ready and print the prompt that tells it what to do.",
		Example: "  omo ai ready",
		Args:    cobra.NoArgs,
		RunE: aiRun(func(a *aiCall, _ []string) error {
			prompt, err := a.client.Ready(a.ctx)
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) { fmt.Fprintln(w, prompt) })
		}),
	})

	ai.AddCommand(&cobra.Command{
		Use:     "whoami",
		Short:   "Print who you are on the board and which ticket you work on",
		Long:    "Print your role, name and own ticket (the user has no ticket).",
		Example: "  omo ai whoami",
		Args:    cobra.NoArgs,
		RunE: aiRun(func(a *aiCall, _ []string) error {
			me, err := a.client.Me(a.ctx)
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) {
				fmt.Fprintf(w, "role: %s\n", me.Actor.Role)
				fmt.Fprintf(w, "name: %s\n", orNone(me.Actor.Name))
				if me.Ticket != nil {
					fmt.Fprintf(w, "ticket: #%d\n", *me.Ticket)
				} else {
					fmt.Fprintln(w, "ticket: none")
				}
			})
		}),
	})

	wait := &cobra.Command{
		Use:   "wait [--timeout DUR]",
		Short: "Block until omo has something for you or the timeout passes, then print why",
		Long: `Wait for the next event meant for you (an answer, a review result, a released
dependency). Prints the reason it returned. DUR is a duration such as 90s or 10m.`,
		Example: "  omo ai wait --timeout 10m",
		Args:    cobra.NoArgs,
		RunE: aiRun(func(a *aiCall, _ []string) error {
			timeout, _ := a.cmd.Flags().GetString("timeout")
			d, err := time.ParseDuration(timeout)
			if err != nil || d <= 0 {
				return fmt.Errorf("--timeout %q is not a duration such as 90s or 10m", timeout)
			}
			reason, err := a.client.Wait(a.ctx, timeout)
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) { fmt.Fprintln(w, reason) })
		}),
	}
	wait.Flags().String("timeout", "10m", "longest time to wait, for example 90s or 10m")
	ai.AddCommand(wait)

	done := &cobra.Command{
		Use:   "done [--approve|--reject] [--comment-file FILE|-] [TEXT...]",
		Short: "Hand in your work on your ticket (reviewers give a verdict)",
		Long: `Report that your work is finished and print the ticket. Reviewers must pass
--approve or --reject; a rejection needs a comment saying what to fix. The
comment is the words after the flags, or a file, or standard input with
--comment-file -.`,
		Example: `  omo ai done "Retries implemented, tests pass"
  omo ai done --reject "The retry loop never stops on a 4xx answer"`,
		Args: cobra.ArbitraryArgs,
		RunE: aiRun(func(a *aiCall, args []string) error {
			flags := a.cmd.Flags()
			approve, _ := flags.GetBool("approve")
			reject, _ := flags.GetBool("reject")
			if approve && reject {
				return fmt.Errorf("give either --approve or --reject, not both")
			}
			file, _ := flags.GetString("comment-file")
			comment, err := readBody(a.cmd, "comment-file", file, args, true)
			if err != nil {
				return err
			}
			req := api.DoneRequest{Comment: comment}
			switch {
			case approve:
				req.Verdict = "approve"
			case reject:
				req.Verdict = "reject"
			}
			t, err := a.client.Done(a.ctx, req)
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) {
				fmt.Fprintln(w, ticketLine(t.TicketSummary))
				if t.ReviewStage != "" {
					fmt.Fprintf(w, "review stage: %s (round %d)\n", t.ReviewStage, t.ReviewRound)
				}
			})
		}),
	}
	done.Flags().Bool("approve", false, "reviewers: approve the work")
	done.Flags().Bool("reject", false, "reviewers: reject the work (needs a comment)")
	done.Flags().String("comment-file", "", "read the comment from FILE, or from standard input with -")
	ai.AddCommand(done)

	project := &cobra.Command{
		Use:     "project",
		Short:   "List and search the registered projects",
		Example: "  omo ai project list",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	plist := &cobra.Command{
		Use:   "list [--all]",
		Short: "List projects, one per line: id name path",
		Long: `List the registered projects as "<id> <name> <path>". The id is what --project
takes. Disabled projects are only listed with --all and are marked.`,
		Example: "  omo ai project list",
		Args:    cobra.NoArgs,
		RunE: aiRun(func(a *aiCall, _ []string) error {
			all, _ := a.cmd.Flags().GetBool("all")
			return listProjects(a, "", all)
		}),
	}
	plist.Flags().Bool("all", false, "include disabled projects")
	project.AddCommand(plist)
	project.AddCommand(&cobra.Command{
		Use:     "search Q",
		Short:   "Find projects whose name or path contains Q",
		Long:    `List the projects matching Q in the same form as "project list".`,
		Example: "  omo ai project search api",
		Args:    cobra.ExactArgs(1),
		RunE: aiRun(func(a *aiCall, args []string) error {
			return listProjects(a, args[0], false)
		}),
	})
	ai.AddCommand(project)

	model := &cobra.Command{
		Use:     "model",
		Short:   "List the models you may choose for tickets",
		Example: "  omo ai model list",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	model.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List models, one per line: id name (cli) description",
		Long: `List the models that can be set on a ticket with --model, as
"<id> <name> (<cli>) <description>". Agents only see selectable models.`,
		Example: "  omo ai model list",
		Args:    cobra.NoArgs,
		RunE: aiRun(func(a *aiCall, _ []string) error {
			models, err := a.client.Models(a.ctx)
			if err != nil {
				return err
			}
			return a.emit(func(w io.Writer) {
				if len(models) == 0 {
					fmt.Fprintln(w, "no models")
				}
				for _, m := range models {
					line := fmt.Sprintf("%s %s (%s)", m.ID, m.Name, m.CLI)
					if m.Description != "" {
						line += " " + m.Description
					}
					if !m.Selectable {
						line += " [not selectable]"
					}
					fmt.Fprintln(w, line)
				}
			})
		}),
	})
	ai.AddCommand(model)
}

func listProjects(a *aiCall, q string, all bool) error {
	projects, err := a.client.Projects(a.ctx, q, all)
	if err != nil {
		return err
	}
	return a.emit(func(w io.Writer) {
		if len(projects) == 0 {
			fmt.Fprintln(w, "no projects")
		}
		for _, p := range projects {
			line := fmt.Sprintf("%s %s %s", p.ID, p.Name, p.Path)
			if !p.Enabled {
				line += " [disabled]"
			}
			if p.Missing {
				line += " [missing]"
			}
			fmt.Fprintln(w, line)
		}
	})
}
