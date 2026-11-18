package api

// ProxyForward maps an opaque DNS label to a web UI run by someone else.
// Ticket is null for a global forward. BasePath is the required app base
// path in path mode; Address includes StartPath and its query. The board
// returns a relative Address; the HTTP API adds omo's advertised origin.
type ProxyForward struct {
	ID          string `json:"id"`
	Ticket      *int   `json:"ticket"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Scheme      string `json:"scheme"`
	Name        string `json:"name"`
	StartPath   string `json:"start_path"`
	StripPrefix bool   `json:"strip_prefix"`
	VerifyTLS   bool   `json:"verify_tls"`
	BasePath    string `json:"base_path"`
	Address     string `json:"address"`
}

// CreateProxyForward is POST /api/proxy/forwards. An omitted/null ticket
// means global. Agent callers must supply their own ticket (the CLI/MCP
// command supplies it when omitted). Defaults: 127.0.0.1, http, /, false,
// false. Registering a forward never starts or probes its target.
type CreateProxyForward struct {
	Ticket      *int   `json:"ticket"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Scheme      string `json:"scheme"`
	Name        string `json:"name"`
	StartPath   string `json:"start_path"`
	StripPrefix bool   `json:"strip_prefix"`
	VerifyTLS   bool   `json:"verify_tls"`
}

// ProxyForwardList is GET /api/proxy/forwards, optionally filtered by ticket.
type ProxyForwardList struct {
	Forwards []ProxyForward `json:"forwards"`
}
