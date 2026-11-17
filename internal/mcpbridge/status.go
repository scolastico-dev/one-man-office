package mcpbridge

import (
	"context"
	"net"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Status is what an MCP client of the bridge gets from omo right now.
type Status struct {
	// Instructions are omo's instructions for this identity.
	Instructions string
	// Tools are the names of the tools offered, omo's and relayed ones.
	Tools []string
}

// Inspect connects to omo's endpoint as session ("" is none) and lists what
// a bridge with that identity offers.
func Inspect(ctx context.Context, dial func(context.Context) (net.Conn, error), session, version string) (Status, error) {
	u := &upstream{dial: dial, session: session, version: version, progress: func(context.Context, *mcp.ProgressNotificationParams) {}}
	defer u.close()
	cs, err := u.connect(ctx)
	if err != nil {
		return Status{}, err
	}
	st := Status{Tools: []string{}}
	if res := cs.InitializeResult(); res != nil {
		st.Instructions = res.Instructions
	}
	for t, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return Status{}, err
		}
		st.Tools = append(st.Tools, t.Name)
	}
	return st, nil
}
