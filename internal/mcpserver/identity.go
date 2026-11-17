// Package mcpserver serves omo's MCP endpoint: omo's own board tools and the
// tools of the servers omo relays, for the identity every request is bound
// to.
package mcpserver

import (
	"context"

	"github.com/scolastico-dev/one-man-office/internal/board"
)

// Class is the kind of caller of the MCP endpoint.
type Class int

const (
	// ClassAgent is an agent session omo started.
	ClassAgent Class = iota + 1
	// ClassUser is a connection omo cannot tie to an agent session (a
	// program started outside omo, a personal token, an OAuth grant). It
	// gets no omo tools, only the relayed servers marked global.
	ClassUser
)

// Identity is who an MCP request acts as. It is resolved for every request
// from the credential of that request, never from a tool argument.
type Identity struct {
	Class Class
	// Actor is the resolved session of an agent; the zero Actor for the
	// user class, which acts on nothing of the board.
	Actor board.Actor
	// Session is the agent session id; "" for the user class. It is never
	// sent to agents.
	Session string
	// Instance is the key of per-session upstream instances: the session
	// id, the bridge's instance id or "user".
	Instance string
	// Workdir is the folder of per-session upstream instances.
	Workdir string
	// Local is true for the socket and false for TCP.
	Local bool
	// Via names the credential: "socket", "session", "pat", "oauth" or
	// "none".
	Via string
}

type identityKey struct{}

// WithIdentity returns ctx carrying id.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// IdentityFrom returns the identity WithIdentity put into ctx.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}
