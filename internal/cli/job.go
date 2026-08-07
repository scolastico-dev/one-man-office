package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid job id %q", s)
	}
	return id, nil
}

func addJobCommands(root *cobra.Command) {
	job := &cobra.Command{Use: "job", Short: "Role-gated job queue operations"}

	var c proto.JobCreateArgs
	create := &cobra.Command{
		Use:   "create",
		Short: "Queue a new job",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res proto.JobCreateResponse
			if err := call("job.create", c, &res); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "job %d queued\n", res.ID)
			return nil
		},
	}
	create.Flags().StringVar(&c.Title, "title", "", "short job title (required)")
	create.Flags().StringVar(&c.Goal, "goal", "", "full goal text (required)")
	create.Flags().StringVar(&c.Role, "role", "", "product_manager|developer|freelancer (required)")
	create.Flags().StringVar(&c.Model, "model", "", "model profile override (must be selectable)")
	create.Flags().StringVar(&c.Repo, "repo", "", "repo key from omo.yaml (required for developer jobs)")
	create.Flags().Int64Var(&c.Parent, "parent", 0, "parent job id (lineage)")
	create.MarkFlagRequired("title")
	create.MarkFlagRequired("goal")
	create.MarkFlagRequired("role")

	list := &cobra.Command{
		Use:   "list",
		Short: "List jobs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var jobs []queue.Job
			if err := call("job.list", nil, &jobs); err != nil {
				return err
			}
			for _, j := range jobs {
				fmt.Fprintf(cmd.OutOrStdout(), "[%d] %-9s %-15s %s (assignee: %s)\n", j.ID, j.State, j.Role, j.Title, j.Assignee)
			}
			return nil
		},
	}

	show := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one job in full",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			var j queue.Job
			if err := call("job.show", proto.JobIDArgs{ID: id}, &j); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"id: %d\ntitle: %s\nrole: %s\nstate: %s\nassignee: %s\nrepo: %s\nbranch: %s\nparent: %d\nnote: %s\nresult: %s\ngoal:\n%s\n",
				j.ID, j.Title, j.Role, j.State, j.Assignee, j.Repo, j.Branch, j.ParentJob, j.Note, j.Result, j.Goal)
			return nil
		},
	}

	var verdictNotes string
	verdict := &cobra.Command{
		Use:   "verdict <id> <merge|reject>",
		Short: "Reviewer verdict for a job in review",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			if err := call("job.verdict", proto.VerdictArgs{JobID: id, Verdict: args[1], Notes: verdictNotes}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "verdict %s recorded for job %d\n", args[1], id)
			return nil
		},
	}
	verdict.Flags().StringVar(&verdictNotes, "notes", "", "findings (required for reject)")

	var overrideNotes string
	override := &cobra.Command{
		Use:   "override <id>",
		Short: "PM decision that a rejected review is out of scope or nitpicking",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			if overrideNotes == "" {
				return fmt.Errorf("--notes with the PM decision is required")
			}
			if err := call("job.review_override", proto.ReviewOverrideArgs{JobID: id, Notes: overrideNotes}, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "review overridden for job %d; current reviewer directed to merge\n", id)
			return nil
		},
	}
	override.Flags().StringVar(&overrideNotes, "notes", "", "why the rejection is nitpicking or out of scope (required)")

	cancel := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a job (firefighter)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			return call("job.cancel", proto.JobIDArgs{ID: id}, nil)
		},
	}
	requeue := &cobra.Command{
		Use:   "requeue <id>",
		Short: "Requeue a failed/cancelled job (firefighter)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			return call("job.requeue", proto.JobIDArgs{ID: id}, nil)
		},
	}

	job.AddCommand(create, list, show, verdict, override, cancel, requeue)
	root.AddCommand(job)
}
