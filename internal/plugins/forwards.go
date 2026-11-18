package plugins

import (
	"context"
	"github.com/scolastico-dev/one-man-office/internal/api"
)

// ForwardAdd extends the ordinary registry options with a stable task identity.
// Plugin identity is supplied by the bound hook, never by its input table.
type ForwardAdd struct {
	api.CreateProxyForward
	TaskID string `json:"task_id,omitempty"`
}

type ForwardActivity struct {
	ActiveRequests   int    `json:"active_requests"`
	ActiveWebSockets int    `json:"active_websockets"`
	EverConnected    bool   `json:"ever_connected"`
	NoClientSince    string `json:"no_client_since"`
	Revision         uint64 `json:"revision"`
}

type Forward struct {
	api.ProxyForward
	Activity ForwardActivity `json:"activity"`
}

// ForwardDesk is the server adapter. Every method receives an identity bound
// by the runtime. No user-facing endpoint accepts that identity as an option.
type ForwardDesk interface {
	AddForward(context.Context, string, ForwardAdd) (Forward, error)
	ListForwards(context.Context, string) ([]Forward, error)
	RemoveForward(context.Context, string, string) error
	AttachForward(context.Context, string, string, string) error
	WatchForward(context.Context, string, string, float64) error
}
