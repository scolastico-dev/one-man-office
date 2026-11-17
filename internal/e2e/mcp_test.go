package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// A developer the engine started calls omo's tools through the MCP bridge
// (`omo mcp stdio`, started by the agent with its own environment) and acts
// as its own session: its comment lands on its own ticket with it as the
// author, and the work goes on to Done as usual.
func TestAgentCommentsThroughTheMCPBridgeEndToEnd(t *testing.T) {
	w := newWorld(t, config{scripts: map[string]string{"developer": `ready
mcp|comment|{"ticket":$TICKET,"body":"written through the bridge by $NAME"}
mcp|whoami
write|fake-$TICKET.txt|done by $NAME
commit|feat: fake work for #$TICKET
done|fake work
wait-loop|10m`}})
	n := w.task("Comment over MCP", api.ReviewAgent, api.WhenApprovedMerge)
	w.waitColumn(n, api.ColumnDone, "")
	cs, err := w.user.Comments(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	var dev string
	for _, s := range w.sessions(n) {
		if s.Role == api.RoleDeveloper {
			dev = s.Name
		}
	}
	found := false
	for _, c := range cs {
		if strings.HasPrefix(c.Body, "written through the bridge by ") {
			found = true
			if c.Author.Name != dev || c.Author.Role != api.RoleDeveloper || c.Body != "written through the bridge by "+dev {
				t.Errorf("comment %q by %+v, want by the developer %s", c.Body, c.Author, dev)
			}
		}
	}
	if !found {
		t.Fatalf("no comment through the bridge on #%d:\n%s", n, w.runner.transcript())
	}
	if tr := w.runner.transcript(); !strings.Contains(tr, "fake: mcp whoami:") {
		t.Fatalf("whoami through the bridge missing:\n%s", tr)
	}
	w.noCrashes(n)
}
