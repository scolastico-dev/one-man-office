package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockc"
)

// Firefighter and smoke-alarm powers: incidents, agent control, office pause.
func addPowerCommands(root *cobra.Command) {
	estop := &cobra.Command{
		Use:   "estop",
		Short: "Immediately stop omo (user, CEO, or firefighter)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, agentID, err := activeOfficeCaller()
			if err != nil {
				return err
			}
			if err := sockc.Call(endpoint, agentID, "office.estop", nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "emergency stop requested")
			return nil
		},
	}
	root.AddCommand(estop)

	incident := &cobra.Command{Use: "incident", Short: "Incident operations (smoke alarm / firefighter)"}
	var ic proto.IncidentCreateArgs
	icCreate := &cobra.Command{
		Use:   "create",
		Short: "File an incident (smoke alarm) — requests a firefighter",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var res proto.IncidentCreateResponse
			if err := call("incident.create", ic, &res); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "incident %d filed\n", res.ID)
			return nil
		},
	}
	icCreate.Flags().StringVar(&ic.Agent, "agent", "", "affected agent name (required)")
	icCreate.Flags().StringVar(&ic.Class, "class", "", "stuck|looping|drifting|too-slow|other (required)")
	icCreate.Flags().StringVar(&ic.Detail, "detail", "", "evidence")
	icCreate.MarkFlagRequired("agent")
	icCreate.MarkFlagRequired("class")

	var report string
	icResolve := &cobra.Command{
		Use:   "resolve <id>",
		Short: "Resolve an incident and file the report to the user (firefighter)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid incident id %q", args[0])
			}
			return call("incident.resolve", proto.IncidentResolveArgs{ID: id, Report: report}, nil)
		},
	}
	icResolve.Flags().StringVar(&report, "report", "", "incident report sent to the user (required)")
	icResolve.MarkFlagRequired("report")
	incident.AddCommand(icCreate, icResolve)

	agent := &cobra.Command{Use: "agent", Short: "List agents or control them (control is firefighter-only)"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List living agents and their current steps",
		RunE: func(cmd *cobra.Command, _ []string) error {
			var agents []proto.AgentStatus
			if err := call("agent.list", nil, &agents); err != nil {
				return err
			}
			for _, a := range agents {
				fmt.Fprintf(cmd.OutOrStdout(), "%-24s %-16s %-9s job=%-4d %s\n", a.Name, a.Role, a.State, a.JobID, a.Step)
			}
			return nil
		},
	}
	kill := &cobra.Command{
		Use:   "kill <name-or-role>",
		Short: "Kill an agent (its job is requeued)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call("agent.kill", proto.AgentNameArgs{Name: args[0]}, nil)
		},
	}
	restart := &cobra.Command{
		Use:   "restart <name-or-role>",
		Short: "Kill an agent and let the supervisor respawn its work",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return call("agent.restart", proto.AgentNameArgs{Name: args[0]}, nil)
		},
	}
	agent.AddCommand(list, kill, restart)

	office := &cobra.Command{Use: "office", Short: "Office spawning controls (CEO / firefighter)"}
	pause := &cobra.Command{Use: "pause", Short: "Pause spawning new agents",
		RunE: func(*cobra.Command, []string) error { return call("office.pause", nil, nil) }}
	resume := &cobra.Command{Use: "resume", Short: "Resume spawning",
		RunE: func(*cobra.Command, []string) error { return call("office.resume", nil, nil) }}
	haltSpawns := &cobra.Command{Use: "halt-spawns", Short: "CEO: halt new work-agent spawns (smoke alarm remains active)",
		RunE: func(*cobra.Command, []string) error { return call("office.halt-spawns", nil, nil) }}
	resumeSpawns := &cobra.Command{
		Use:   "resume-spawns",
		Short: "User or CEO: exit safe mode and allow all agent spawns",
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, agentID, err := activeOfficeCaller()
			if err != nil {
				return err
			}
			if err := sockc.Call(endpoint, agentID, "office.resume-spawns", nil, nil); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "full office spawning resumed")
			return nil
		},
	}
	office.AddCommand(pause, resume, haltSpawns, resumeSpawns)

	root.AddCommand(incident, agent, office)
}

func activeOfficeCaller() (endpoint, agentID string, err error) {
	if endpoint, agentID, err = sockc.Env(); err == nil {
		return endpoint, agentID, nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	endpoint, running, err := office.Running(dir)
	if err != nil {
		return "", "", err
	}
	if !running {
		return "", "", fmt.Errorf("no running omo office found at %s", filepath.Join(dir, office.LockPath))
	}
	return endpoint, "user", nil
}
