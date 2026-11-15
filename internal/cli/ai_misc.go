package cli

import (
	"fmt"
	"io"
	"strings"
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
			return a.emit(func(w io.Writer) { printMe(w, me) })
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
		Use:   "done [--approve|--reject|--url URL|--failed MESSAGE|-] [--comment-file FILE|-] [TEXT...|-]",
		Short: "Hand in your work on your ticket (reviewers give a verdict)",
		Long: `Report that your work is finished and print the ticket. Reviewers must pass
--approve or --reject; a rejection needs a comment saying what to fix. The
comment is the words after the flags, or a file, or standard input with a
lone - or --comment-file -. An export agent passes --url with the address of
what it published, or --failed with a message saying why it could not
(--failed - reads it from standard input); both stand alone, without
--approve, --reject or a comment.`,
		Example: `  omo ai done "Retries implemented, tests pass"
  omo ai done --reject "The retry loop never stops on a 4xx answer"
  omo ai done --url https://github.com/org/repo/pull/7
  omo ai done --failed "no permission to push"`,
		Args: cobra.ArbitraryArgs,
		RunE: aiRun(func(a *aiCall, args []string) error {
			flags := a.cmd.Flags()
			approve, _ := flags.GetBool("approve")
			reject, _ := flags.GetBool("reject")
			if approve && reject {
				return fmt.Errorf("give either --approve or --reject, not both")
			}
			url, _ := flags.GetString("url")
			failed, _ := flags.GetString("failed")
			urlSet, failedSet := flags.Changed("url"), flags.Changed("failed")
			if urlSet || failedSet {
				switch {
				case url == "" && urlSet, failed == "" && failedSet:
					return fmt.Errorf("--url and --failed need a value")
				case urlSet && failedSet:
					return fmt.Errorf("give either --url or --failed, not both")
				case approve || reject:
					return fmt.Errorf("--url and --failed cannot be combined with --approve or --reject")
				case len(args) > 0 || flags.Changed("comment-file"):
					return fmt.Errorf("--url and --failed take no comment")
				}
				if failed == stdinMark {
					raw, err := io.ReadAll(a.cmd.InOrStdin())
					if err != nil {
						return fmt.Errorf("read standard input: %w", err)
					}
					if failed, err = checkText(string(raw), "standard input", "give the message as --failed MESSAGE, or on standard input with --failed -", false); err != nil {
						return err
					}
				}
				t, err := a.client.Done(a.ctx, api.DoneRequest{URL: url, Error: failed})
				if err != nil {
					return err
				}
				return a.emit(func(w io.Writer) { fmt.Fprintln(w, ticketLine(t.TicketSummary)) })
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
	done.Flags().String("url", "", "export agents: the address of what you published")
	done.Flags().String("failed", "", "export agents: why the export failed (- reads it from standard input)")
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
		Short: "List projects, one per line: id name path and branches",
		Long: `List the registered projects as "<id> <name> <path> [on <branch>, default <branch>]".
The id is what --project takes. "on" names the branch checked out in the
project folder, which a new ticket is based on; "default" the repository's
default branch. A project whose folder is not on its default branch is
marked "not the default branch"; a branch omo cannot tell is left out.
Disabled projects are only listed with --all and are marked.`,
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
					fmt.Fprintln(w, modelLine(m))
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
			fmt.Fprintln(w, projectLine(p))
		}
	})
}

// printMe prints the answer of whoami.
func printMe(w io.Writer, me api.Me) {
	fmt.Fprintf(w, "role: %s\n", me.Actor.Role)
	fmt.Fprintf(w, "name: %s\n", orNone(oneLine(me.Actor.Name)))
	if me.Ticket != nil {
		fmt.Fprintf(w, "ticket: #%d\n", *me.Ticket)
	} else {
		fmt.Fprintln(w, "ticket: none")
	}
}

// modelLine is the one-line form of a model; its text comes from the
// settings files and goes through oneLine.
func modelLine(m api.Model) string {
	line := fmt.Sprintf("%s %s (%s)", oneLine(m.ID), oneLine(m.Name), oneLine(m.CLI))
	if m.Description != "" {
		line += " " + oneLine(m.Description)
	}
	if !m.Selectable {
		line += " [not selectable]"
	}
	return line
}

// projectLine is the one-line form of a project. Names and paths can come
// from folder names an agent made: they go through oneLine.
func projectLine(p api.Project) string {
	line := fmt.Sprintf("%s %s %s", oneLine(p.ID), oneLine(p.Name), oneLine(p.Path))
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
		parts = append(parts, "on "+oneLine(p.CurrentBranch))
	}
	if p.DefaultBranch != "" {
		parts = append(parts, "default "+oneLine(p.DefaultBranch))
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
