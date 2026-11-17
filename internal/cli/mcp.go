package cli

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/aicmd"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/mcpbridge"
)

// addMCPCommand adds `omo mcp`: the bridge agent programs start as their
// MCP server, a look at what it offers, the personal tokens of MCP clients
// and omo's entry in the agent programs.
func addMCPCommand(root *cobra.Command) {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "omo's MCP server for agent programs",
		Long: "omo serves its `omo ai` commands as MCP tools, and the tools of the MCP servers it relays,\n" +
			"at /mcp. Agent programs reach it through `omo mcp stdio`, which acts as the agent session\n" +
			"of OMO_AGENT_ID, as `omo ai` does. `omo mcp install` registers that bridge in the agent\n" +
			"programs; `omo mcp token` manages the tokens other MCP clients reach /mcp with.",
		Args: cobra.NoArgs,
	}
	version := root.Version
	stdio := &cobra.Command{
		Use:   "stdio",
		Short: "Serve omo's MCP endpoint on standard input and output",
		Long: "Serve omo's MCP endpoint on standard input and output, for an agent program that starts it\n" +
			"as an MCP server. It reaches omo over its local socket (OMO_SOCKET, else the omo home's)\n" +
			"as the agent session of OMO_AGENT_ID; without it the connection is tied to no omo agent\n" +
			"session and offers none of omo's board tools.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			session, dial, err := mcpbridge.Environment()
			if err != nil {
				return err
			}
			return mcpbridge.Run(cmd.Context(), mcpbridge.Options{
				Dial: dial, Session: session, In: cmd.InOrStdin(), Out: cmd.OutOrStdout(),
				Log: log.New(cmd.ErrOrStderr(), "omo mcp stdio: ", 0), Version: version,
			})
		},
	}
	status := &cobra.Command{
		Use:   "status",
		Short: "Show what omo's MCP endpoint offers here",
		Long: "Connect to omo's MCP endpoint as `omo mcp stdio` would in this terminal and list the tools\n" +
			"it offers.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			session, dial, err := mcpbridge.Environment()
			if err != nil {
				return err
			}
			st, err := mcpbridge.Inspect(cmd.Context(), dial, session, version)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			who := "not tied to an omo agent session (OMO_AGENT_ID is not set)"
			if session != "" {
				who = "the agent session of OMO_AGENT_ID"
			}
			fmt.Fprintf(out, "connection: %s\n", who)
			if st.Instructions != "" {
				fmt.Fprintf(out, "instructions: %s\n", st.Instructions)
			}
			fmt.Fprintf(out, "tools (%d):", len(st.Tools))
			if len(st.Tools) == 0 {
				fmt.Fprintln(out, " none")
				return nil
			}
			fmt.Fprintf(out, "\n    %s\n", strings.Join(st.Tools, "\n    "))
			return nil
		},
	}
	mcpCmd.AddCommand(stdio, status, mcpTokenCommand(), mcpClientCommand(api.MCPClientInstall), mcpClientCommand(api.MCPClientRemove))
	root.AddCommand(mcpCmd)
}

// mcpTokenCommand is `omo mcp token`: the personal access tokens MCP
// clients outside omo reach /mcp with, through the running omo's routes.
func mcpTokenCommand() *cobra.Command {
	token := &cobra.Command{
		Use:   "token",
		Short: "Manage the personal tokens MCP clients reach omo with",
		Long: "Manage the personal access tokens MCP clients outside omo send as bearer to /mcp. A token\n" +
			"is tied to no omo agent session: it reaches only the relayed servers marked global, never\n" +
			"omo's board tools. omo must be running.",
		Args: cobra.NoArgs,
	}
	var expires string
	create := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a token and print its secret once",
		Long: "Create a personal access token named NAME and print its secret. omo keeps only a hash: the\n" +
			"secret cannot be shown again. --expires ends it after a duration (90d, 12h) or on a date\n" +
			"(2027-01-31); without it the token lasts until it is revoked.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := api.MCPTokenRequest{Name: args[0]}
			if expires != "" {
				at, err := parseExpiry(expires, time.Now())
				if err != nil {
					return err
				}
				req.ExpiresAt = &at
			}
			c, err := apiclient.FromEnvironment()
			if err != nil {
				return err
			}
			var created api.MCPTokenCreated
			if err := c.Do(cmd.Context(), http.MethodPost, "/api/mcp/tokens", req, &created); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "Token %s created (id %s). Its secret is shown only now:\n%s\n", aicmd.OneLine(created.Token.Name), created.Token.ID, created.Secret)
			return nil
		},
	}
	create.Flags().StringVar(&expires, "expires", "", "end the token after a duration (90d, 12h) or on a date (2027-01-31)")
	list := &cobra.Command{
		Use:   "list",
		Short: "List the personal tokens and OAuth grants",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := apiclient.FromEnvironment()
			if err != nil {
				return err
			}
			var tokens []api.MCPToken
			if err := c.Do(cmd.Context(), http.MethodGet, "/api/mcp/tokens", nil, &tokens); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(tokens) == 0 {
				fmt.Fprintln(out, "No tokens or grants.")
				return nil
			}
			for _, t := range tokens {
				fmt.Fprintf(out, "%s  %-5s  %s  created %s, last used %s, expires %s\n", t.ID, t.Kind, aicmd.OneLine(t.Name),
					t.CreatedAt.Local().Format("2006-01-02"), dayOrNever(t.LastUsedAt, "never"), dayOrNever(t.ExpiresAt, "never"))
			}
			return nil
		},
	}
	revoke := &cobra.Command{
		Use:   "revoke ID",
		Short: "Revoke a personal token or an OAuth grant",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := apiclient.FromEnvironment()
			if err != nil {
				return err
			}
			if err := c.Do(cmd.Context(), http.MethodDelete, "/api/mcp/tokens/"+url.PathEscape(args[0]), nil, nil); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Revoked %s.\n", aicmd.OneLine(args[0]))
			return nil
		},
	}
	token.AddCommand(create, list, revoke)
	return token
}

// parseExpiry reads --expires: days (90d), a duration (12h) or a date.
func parseExpiry(v string, now time.Time) (time.Time, error) {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		if n, err := strconv.Atoi(days); err == nil && n > 0 {
			return now.AddDate(0, 0, n), nil
		}
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return now.Add(d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", v, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("--expires %q: give days (90d), a duration (12h) or a date (2027-01-31)", v)
}

func dayOrNever(t *time.Time, never string) string {
	if t == nil {
		return never
	}
	return t.Local().Format("2006-01-02")
}

// mcpClientCommand is `omo mcp install` or `omo mcp uninstall`: omo's
// entry in the agent programs' configuration, as the MCP page does it.
func mcpClientCommand(action string) *cobra.Command {
	use, short, verb := "install", "Register omo's MCP server in the agent programs", "registers"
	if action == api.MCPClientRemove {
		use, short, verb = "uninstall", "Remove omo's MCP server from the agent programs", "removes"
	}
	var yes bool
	cmd := &cobra.Command{
		Use:   use + " [PROGRAM|TARGET]",
		Short: short,
		Long: "Show how omo " + verb + " its MCP server (`omo mcp stdio`) in the configuration of the agent\n" +
			"programs: every program and profile account omo knows, the targets of one PROGRAM (claude,\n" +
			"codex, agy), or one TARGET by the id the MCP page shows. Nothing changes without --yes; omo\n" +
			"keeps a copy of every file it changes. omo must be running.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := apiclient.FromEnvironment()
			if err != nil {
				return err
			}
			var states []api.MCPClientState
			if err := c.Do(cmd.Context(), http.MethodGet, "/api/mcp/clients", nil, &states); err != nil {
				return err
			}
			var targets []api.MCPClientTarget
			for _, st := range states {
				if len(args) == 0 || args[0] == st.Target.ID || args[0] == st.Target.CLI {
					targets = append(targets, st.Target)
				}
			}
			if len(targets) == 0 {
				var ids []string
				for _, st := range states {
					ids = append(ids, st.Target.ID)
				}
				return fmt.Errorf("no agent program or target %q; the targets are: %s", aicmd.OneLine(args[0]), strings.Join(ids, ", "))
			}
			out := cmd.OutOrStdout()
			pending := 0
			for _, t := range targets {
				var plan api.MCPClientPlan
				if err := c.Do(cmd.Context(), http.MethodPost, "/api/mcp/clients/"+url.PathEscape(t.ID)+"/plan", api.MCPClientPlanRequest{Action: action}, &plan); err != nil {
					return fmt.Errorf("%s: %w", t.ID, err)
				}
				printClientPlan(out, plan)
				if len(plan.Commands) == 0 {
					continue
				}
				pending++
				if !yes {
					continue
				}
				var applied api.MCPClientApplied
				if err := c.Do(cmd.Context(), http.MethodPost, "/api/mcp/clients/"+url.PathEscape(t.ID)+"/apply", api.MCPClientApply{Plan: plan.Hash}, &applied); err != nil {
					return fmt.Errorf("%s: %w", t.ID, err)
				}
				fmt.Fprintf(out, "  done: %s\n", applied.State.State)
				if applied.Backup != "" {
					fmt.Fprintf(out, "  the file before is kept in %s\n", aicmd.OneLine(applied.Backup))
				}
			}
			switch {
			case pending == 0:
				fmt.Fprintln(out, "Nothing to do.")
			case !yes:
				fmt.Fprintln(out, "Nothing changed. Run again with --yes to apply.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "apply the changes shown")
	return cmd
}

func printClientPlan(out io.Writer, p api.MCPClientPlan) {
	fmt.Fprintf(out, "%s (%s):\n", aicmd.OneLine(p.Target.ID), aicmd.OneLine(p.File))
	if p.Message != "" {
		fmt.Fprintf(out, "  %s\n", aicmd.OneLine(p.Message))
	}
	for _, c := range p.Commands {
		quoted := make([]string, len(c))
		for i, a := range c {
			quoted[i] = aicmd.OneLine(a)
			if strings.ContainsAny(a, " \t\"'") || a == "" {
				quoted[i] = strconv.Quote(quoted[i])
			}
		}
		fmt.Fprintf(out, "  runs: %s\n", strings.Join(quoted, " "))
	}
}
