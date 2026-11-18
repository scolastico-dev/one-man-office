package api

import (
	"encoding/json"
	"time"
)

// Values of MCPServer.Transport.
const (
	MCPTransportStdio = "stdio"
	MCPTransportHTTP  = "http"
	MCPTransportSSE   = "sse"
)

// Values of MCPServer.Instances (stdio servers only).
const (
	// MCPInstancesPerSession starts one process per session in that
	// session's folder.
	MCPInstancesPerSession = "per_session"
	// MCPInstancesShared starts one process for every caller.
	MCPInstancesShared = "shared"
)

// Values of MCPServer.Auth.
const (
	MCPAuthNone    = "none"
	MCPAuthHeaders = "headers"
	MCPAuthOAuth   = "oauth"
)

// MCPServer is an upstream MCP server omo relays: the payload of PUT
// /api/mcp/servers/{name} and an item of GET /api/mcp/servers. Its JSON names
// equal the keys of mcpservers/<name>.yaml; the name comes from the file
// name. Values (env, headers, OAuth client secret) are stored and answered
// as they are.
type MCPServer struct {
	Name    string `yaml:"-" json:"name"`
	Enabled bool   `yaml:"enabled" json:"enabled"`
	// Description is one optional line shown on the settings page.
	Description string `yaml:"description,omitempty" json:"description"`
	// Transport is MCPTransportStdio, MCPTransportHTTP or MCPTransportSSE.
	Transport string `yaml:"transport" json:"transport"`

	// Command, Args, Env, Cwd and Instances are for stdio servers. Env
	// values and header values may hold ${VAR} and ${VAR:-default}
	// references to omo's environment. Cwd "" is the folder of the instance.
	Command   string            `yaml:"command,omitempty" json:"command"`
	Args      []string          `yaml:"args,omitempty" json:"args"`
	Env       map[string]string `yaml:"env,omitempty" json:"env"`
	Cwd       string            `yaml:"cwd,omitempty" json:"cwd"`
	Instances string            `yaml:"instances,omitempty" json:"instances"`

	// URL, Headers, HeadersCommand, Auth and OAuth are for http and sse
	// servers. HeadersCommand is an argv run before connecting that prints
	// a JSON object of headers.
	URL            string            `yaml:"url,omitempty" json:"url"`
	Headers        map[string]string `yaml:"headers,omitempty" json:"headers"`
	HeadersCommand []string          `yaml:"headers_command,omitempty" json:"headers_command"`
	// Auth is MCPAuthNone, MCPAuthHeaders or MCPAuthOAuth.
	Auth  string   `yaml:"auth,omitempty" json:"auth"`
	OAuth MCPOAuth `yaml:"oauth,omitempty" json:"oauth"`

	// Roles are the agent roles (ceo, pm, developer, reviewer, export)
	// whose sessions see the server's tools; empty is every role.
	Roles []string `yaml:"roles,omitempty" json:"roles"`
	// Global also offers the server's tools to connections that are not
	// tied to an omo agent session (programs started outside omo, personal
	// tokens, OAuth grants). Off, only omo's agent sessions see them.
	Global   bool          `yaml:"global,omitempty" json:"global"`
	Tools    MCPToolFilter `yaml:"tools,omitempty" json:"tools"`
	Timeouts MCPTimeouts   `yaml:"timeouts,omitempty" json:"timeouts"`
	// Source says where a server taken over from an agent program came
	// from ("claude", "codex@<account>", ...); informational only.
	Source string `yaml:"source,omitempty" json:"source"`
	// Revision identifies the content of the file for If-Match. It is
	// ignored in PUT bodies.
	Revision string `yaml:"-" json:"revision,omitempty"`
}

// MCPOAuth is the OAuth client of an upstream server with auth oauth. An
// empty ClientID registers a client through the server's discovery.
type MCPOAuth struct {
	ClientID     string   `yaml:"client_id,omitempty" json:"client_id"`
	ClientSecret string   `yaml:"client_secret,omitempty" json:"client_secret"`
	Scopes       []string `yaml:"scopes,omitempty" json:"scopes"`
	// Resource overrides the RFC 8707 resource sent to the server's
	// authorization server.
	Resource string `yaml:"resource,omitempty" json:"resource"`
}

// MCPToolFilter selects upstream tools by their upstream names: an empty
// Allow allows every tool; Deny applies after Allow.
type MCPToolFilter struct {
	Allow []string `yaml:"allow,omitempty" json:"allow"`
	Deny  []string `yaml:"deny,omitempty" json:"deny"`
}

// MCPTimeouts bound the start of a server (connect and list its tools) and
// one tool call. Zero is omo's default.
type MCPTimeouts struct {
	Startup Duration `yaml:"startup,omitempty" json:"startup"`
	Call    Duration `yaml:"call,omitempty" json:"call"`
}

// Values of MCPServerState.State.
const (
	MCPStateStopped    = "stopped"
	MCPStateStarting   = "starting"
	MCPStateRunning    = "running"
	MCPStateFailed     = "failed"
	MCPStateNeedsLogin = "needs_login"
	MCPStateDisabled   = "disabled"
)

// MCPServerState is the state of a relayed server: an item of GET
// /api/mcp/servers and the data of mcp.changed. It never holds a value of
// the server's env or headers.
type MCPServerState struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// Instances is the number of running processes or connections.
	Instances int `json:"instances"`
	// Tools are the names omo offers for the server's tools.
	Tools []string `json:"tools"`
	// Omitted are upstream tools omo does not offer, with the reason.
	Omitted   []MCPOmittedTool `json:"omitted"`
	LastError string           `json:"last_error"`
	StartedAt *time.Time       `json:"started_at"`
	Auth      MCPAuthState     `json:"auth"`
	// OffersResources and OffersPrompts say that the server has resources
	// or prompts, which omo does not relay.
	OffersResources bool `json:"offers_resources"`
	OffersPrompts   bool `json:"offers_prompts"`
}

// MCPOmittedTool is an upstream tool omo leaves out, and why.
type MCPOmittedTool struct {
	Tool   string `json:"tool"`
	Reason string `json:"reason"`
}

// Values of MCPAuthState.State.
const (
	MCPAuthStateNone       = "none"
	MCPAuthStateConnected  = "connected"
	MCPAuthStateNeedsLogin = "needs_login"
	MCPAuthStateError      = "error"
)

// MCPAuthState is the state of omo's sign-in at an upstream server with
// auth oauth.
type MCPAuthState struct {
	State     string     `json:"state"`
	ExpiresAt *time.Time `json:"expires_at"`
	Scopes    []string   `json:"scopes"`
}

// MCPServersResponse is the answer of GET /api/mcp/servers.
type MCPServersResponse struct {
	Servers []MCPServer      `json:"servers"`
	States  []MCPServerState `json:"states"`
	Errors  []FileError      `json:"errors"`
}

// MCPTestResult is the answer of POST /api/mcp/servers/test: the tools a
// draft server offers, or why it could not be started; nothing is saved.
type MCPTestResult struct {
	Tools           []string         `json:"tools"`
	Omitted         []MCPOmittedTool `json:"omitted"`
	Error           string           `json:"error"`
	OffersResources bool             `json:"offers_resources"`
	OffersPrompts   bool             `json:"offers_prompts"`
	Millis          int64            `json:"millis"`
}

// MCPLogLine is a line of GET /api/mcp/servers/{name}/log.
type MCPLogLine struct {
	At      time.Time `json:"at"`
	Message string    `json:"message"`
}

// MCPConnect is the answer of POST /api/mcp/servers/{name}/connect: the
// address the browser opens to sign in at the server's provider.
type MCPConnect struct {
	AuthorizeURL string `json:"authorize_url"`
}

// MCPSettings are the general MCP switches.
type MCPSettings struct {
	// Enabled serves /mcp and gives agents omo's MCP entry.
	Enabled bool      `yaml:"enabled" json:"enabled"`
	Inject  MCPInject `yaml:"inject" json:"inject"`
}

// MCPInject switches the per-session MCP entry of omo-started agents per
// agent program.
type MCPInject struct {
	Claude bool `yaml:"claude" json:"claude"`
	Codex  bool `yaml:"codex" json:"codex"`
}

// Values of MCPToken.Kind.
const (
	MCPTokenPAT   = "pat"
	MCPTokenOAuth = "oauth"
)

// MCPToken is a personal access token or an OAuth grant of an MCP client:
// an item of GET /api/mcp/tokens. The secret is never answered again after
// it was created.
type MCPToken struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Name is the token's name or the OAuth client's name.
	Name         string     `json:"name"`
	ClientID     string     `json:"client_id"`
	RedirectHost string     `json:"redirect_host"`
	Resource     string     `json:"resource"`
	Scope        string     `json:"scope"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	// RefreshExpiresAt is when an OAuth grant ends unless it is used.
	RefreshExpiresAt *time.Time `json:"refresh_expires_at"`
	LastUsedAt       *time.Time `json:"last_used_at"`
}

// MCPTokenRequest is the payload of POST /api/mcp/tokens.
type MCPTokenRequest struct {
	Name      string     `json:"name"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// MCPTokenCreated is the answer of POST /api/mcp/tokens; Secret is shown
// once.
type MCPTokenCreated struct {
	Token  MCPToken `json:"token"`
	Secret string   `json:"secret"`
}

// MCPClientTarget is one configuration of an agent program that can hold
// omo's MCP entry: the program's default file or the file of a profile's
// account folder.
type MCPClientTarget struct {
	ID string `json:"id"`
	// CLI is claude, codex or agy.
	CLI string `json:"cli"`
	// Account is the account folder, "" for the program's default.
	Account    string `json:"account"`
	ConfigPath string `json:"config_path"`
}

// Values of MCPClientState.State.
const (
	MCPClientAbsent    = "absent"
	MCPClientInstalled = "installed"
	MCPClientOutdated  = "outdated"
	MCPClientForeign   = "foreign"
)

// MCPClientState is what a target's file says about omo: an item of GET
// /api/mcp/clients. Entries are the other servers of the file, which omo
// can take over.
type MCPClientState struct {
	Target MCPClientTarget `json:"target"`
	State  string          `json:"state"`
	// Entry is the omo entry as JSON; null when there is none.
	Entry   json.RawMessage  `json:"entry"`
	Entries []MCPClientEntry `json:"entries"`
	// Error says why the file could not be read; State is then absent.
	Error string `json:"error"`
}

// MCPClientEntry is a server entry of an agent program's file, read as
// omo's server settings.
type MCPClientEntry struct {
	// ProjectScoped is inspection metadata used when checking a local removal.
	// It is independent of the explanatory Reason and never goes on the wire.
	ProjectScoped bool      `json:"-"`
	Name          string    `json:"name"`
	Server        MCPServer `json:"server"`
	Notes         []string  `json:"notes"`
	// Reason says why omo cannot take the entry over; "" when it can.
	Reason string `json:"reason"`
}

// Values of MCPClientPlanRequest.Action.
const (
	MCPClientInstall = "install"
	MCPClientRemove  = "remove"
)

// MCPClientPlanRequest is the payload of POST /api/mcp/clients/{target}/plan.
type MCPClientPlanRequest struct {
	Action string `json:"action"`
}

// MCPClientPlan is what omo would run to install or remove its entry in a
// target, shown before it is applied. Before and After are the omo entry
// as JSON (null when absent); Commands is empty when nothing is to do and
// Message says so.
type MCPClientPlan struct {
	Target   MCPClientTarget `json:"target"`
	Action   string          `json:"action"`
	File     string          `json:"file"`
	Commands [][]string      `json:"commands"`
	Before   json.RawMessage `json:"before"`
	After    json.RawMessage `json:"after"`
	Message  string          `json:"message"`
	// Hash identifies the plan; the apply sends it back.
	Hash string `json:"hash"`
}

// MCPClientApply is the payload of POST /api/mcp/clients/{target}/apply.
type MCPClientApply struct {
	Plan string `json:"plan"`
}

// MCPClientApplied is the answer of a plan's apply: the target's new state
// and the copy of its file made before.
type MCPClientApplied struct {
	State  MCPClientState `json:"state"`
	Backup string         `json:"backup"`
}

// MCPTakeover is the answer of GET /api/mcp/takeover: the servers of the
// agent programs' files omo can take over, and those that stay.
type MCPTakeover struct {
	Candidates []MCPTakeoverCandidate `json:"candidates"`
	Stays      []MCPTakeoverStay      `json:"stays"`
}

// MCPTakeoverCandidate is a server omo can take over from one or more
// files.
type MCPTakeoverCandidate struct {
	Key        string              `json:"key"`
	Name       string              `json:"name"`
	Server     MCPServer           `json:"server"`
	Sources    []MCPTakeoverSource `json:"sources"`
	Notes      []string            `json:"notes"`
	NeedsLogin bool                `json:"needs_login"`
	// Conflict says why the candidate is not chosen by default; "" when
	// it is.
	Conflict string `json:"conflict"`
}

// MCPTakeoverSource is an entry of a target's file.
type MCPTakeoverSource struct {
	Target    string `json:"target"`
	EntryName string `json:"entry_name"`
}

// MCPTakeoverStay is an entry omo does not take over, and why.
type MCPTakeoverStay struct {
	Target    string `json:"target"`
	EntryName string `json:"entry_name"`
	Reason    string `json:"reason"`
}

// MCPTakeoverApply is the payload of POST /api/mcp/takeover/apply.
type MCPTakeoverApply struct {
	Take          []MCPTakeoverPick `json:"take"`
	RemoveFromCLI bool              `json:"remove_from_cli"`
}

// MCPTakeoverPick is a chosen candidate under the name it gets in omo.
type MCPTakeoverPick struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// MCPTakeoverReport is the answer of POST /api/mcp/takeover/apply.
type MCPTakeoverReport struct {
	Results []MCPTakeoverResult `json:"results"`
	Backups []string            `json:"backups"`
}

// MCPTakeoverResult is what happened to one chosen candidate.
type MCPTakeoverResult struct {
	Name string `json:"name"`
	// Stored says that omo's server file was written; Error says why not.
	Stored     bool                 `json:"stored"`
	Error      string               `json:"error"`
	Removed    []MCPTakeoverRemoval `json:"removed"`
	NeedsLogin bool                 `json:"needs_login"`
}

// MCPTakeoverRemoval is the removal of a taken-over entry from a target.
type MCPTakeoverRemoval struct {
	Target string `json:"target"`
	Done   bool   `json:"done"`
	Error  string `json:"error"`
}

// Values of OAuthConsent.ClientKind.
const (
	OAuthClientCIMD       = "cimd"
	OAuthClientRegistered = "registered"
)

// OAuthConsent is the answer of GET /api/oauth/consent: what the consent
// page shows about a client that asks for access to omo's MCP endpoint. Such
// a client is tied to no agent session: it gets the global relayed servers
// and no board tools.
type OAuthConsent struct {
	ClientName string `json:"client_name"`
	ClientID   string `json:"client_id"`
	ClientKind string `json:"client_kind"`
	// RedirectHost is where the browser goes after the decision.
	RedirectHost string `json:"redirect_host"`
	// LocalhostRedirect says that redirect is a localhost address: the
	// answer goes to whatever program listens there.
	LocalhostRedirect bool   `json:"localhost_redirect"`
	Resource          string `json:"resource"`
}

// OAuthDecision is the payload of POST /api/oauth/consent: the query of the
// authorization request and the user's answer.
type OAuthDecision struct {
	Query   string `json:"query"`
	Approve bool   `json:"approve"`
}

// OAuthRedirect is the answer of POST /api/oauth/consent: the address the
// browser goes to next.
type OAuthRedirect struct {
	Redirect string `json:"redirect"`
}
