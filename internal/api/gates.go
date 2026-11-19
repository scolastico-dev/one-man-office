package api

// GateMove names a move of a ticket that plugins can gate.
type GateMove string

const (
	// GateMoveRelease is the move from Planning to Ready For Dev.
	GateMoveRelease GateMove = "release"
	// GateMoveStart is the move from Ready For Dev to In Progress: the
	// start of the ticket's work.
	GateMoveStart GateMove = "start"
)

// GateState is where one plugin's gate of a ticket's move stands.
type GateState string

const (
	// GatePending: the plugin's gate hook has not answered yet.
	GatePending GateState = "pending"
	// GateHeld: the plugin holds the move until it gives its go-ahead.
	GateHeld GateState = "held"
	// GatePassed: the plugin gave its go-ahead.
	GatePassed GateState = "passed"
)

// GateChoice is one button a plugin offers on a ticket it holds: Action is
// the name of a manual hook of the same plugin.
type GateChoice struct {
	Label  string `json:"label"`
	Action string `json:"action"`
}

// TicketGate is one plugin's gate of a ticket's move.
type TicketGate struct {
	Plugin  string       `json:"plugin"`
	State   GateState    `json:"state"`
	Reason  string       `json:"reason"`
	Choices []GateChoice `json:"choices"`
}

// TicketGates is the move of a ticket that waits for plugins, with every
// gating plugin's state. The ticket stays in its column (From) until all
// plugins gave their go-ahead; then it goes on to To.
type TicketGates struct {
	Move  GateMove     `json:"move"`
	From  Column       `json:"from"`
	To    Column       `json:"to"`
	Gates []TicketGate `json:"gates"`
}

// GateReleaseRequest is the body of POST /api/tickets/{n}/gate/release: the
// go-ahead a plugin gives from a hook.
type GateReleaseRequest struct {
	Plugin string `json:"plugin"`
}

// GateCancelRequest is the body of POST /api/tickets/{n}/gate/cancel. Plugin
// names the plugin that cancels the held move from a hook; empty is the
// user's own cancel.
type GateCancelRequest struct {
	Plugin string `json:"plugin,omitempty"`
}

// TicketGateData is the data of ticket.held, ticket.released,
// ticket.refused, ticket.overridden and ticket.hold_cancelled. Plugin is ""
// when no single plugin is meant (all gave their go-ahead; the user
// overrode every hold).
type TicketGateData struct {
	Ticket  TicketSummary `json:"ticket"`
	Move    GateMove      `json:"move"`
	Plugin  string        `json:"plugin"`
	Reason  string        `json:"reason"`
	Choices []GateChoice  `json:"choices"`
}
