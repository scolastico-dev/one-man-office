package cli

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scolastico-dev/one-man-office/internal/mcpbridge"
)

// addMCPCommand adds `omo mcp`: the bridge agent programs start as their
// MCP server, and a look at what it offers.
func addMCPCommand(root *cobra.Command) {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "omo's MCP server for agent programs",
		Long: "omo serves its `omo ai` commands as MCP tools, and the tools of the MCP servers it relays,\n" +
			"at /mcp. Agent programs reach it through `omo mcp stdio`, which acts as the agent session\n" +
			"of OMO_AGENT_ID, as `omo ai` does.",
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
	mcpCmd.AddCommand(stdio, status)
	root.AddCommand(mcpCmd)
}
