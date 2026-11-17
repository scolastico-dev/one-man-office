// Command fakemcp is the scripted MCP server (internal/mcprelay/fakemcp)
// as a program of its own, for the browser tests: a stdio server that omo
// starts and relays like any configured server. It refuses to run unless
// FAKEMCP_SERVER=1 is set, so it cannot be started by accident; the
// server's settings carry that variable in their environment.
package main

import (
	"fmt"
	"os"

	"github.com/scolastico-dev/one-man-office/internal/mcprelay/fakemcp"
)

func main() {
	if !fakemcp.Enabled() {
		fmt.Fprintf(os.Stderr, "fakemcp: refusing to run: set %s=1 to run the scripted MCP server\n", fakemcp.EnvEnable)
		os.Exit(2)
	}
	os.Exit(fakemcp.Main())
}
