package mcpbridge

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/apiclient"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/transport"
)

// Environment reads the bridge's identity and socket the way `omo ai` does:
// the session from OMO_AGENT_ID (unset is no session; set but blank is
// apiclient.ErrEmptyAgentID), the socket from OMO_SOCKET, else the socket
// of the omo home. The home's socket is looked up again for every
// connection, because a restarted omo listens on a new one.
func Environment() (session string, dial func(context.Context) (net.Conn, error), err error) {
	session, isAgent := os.LookupEnv("OMO_AGENT_ID")
	if isAgent {
		session = strings.TrimSpace(session)
		if session == "" {
			return "", nil, apiclient.ErrEmptyAgentID
		}
	}
	if socket := os.Getenv("OMO_SOCKET"); socket != "" {
		return session, func(ctx context.Context) (net.Conn, error) { return transport.Dial(ctx, socket) }, nil
	}
	root, err := globalhome.Dir()
	if err != nil {
		return "", nil, err
	}
	run := filepath.Join(root, "run")
	return session, func(ctx context.Context) (net.Conn, error) {
		socket, err := transport.ClientEndpoint(run)
		if err != nil {
			return nil, apiclient.ErrNotRunning
		}
		return transport.Dial(ctx, socket)
	}, nil
}
