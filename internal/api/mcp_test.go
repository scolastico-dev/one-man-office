package api

import (
	"encoding/json"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestMCPTypesWireNames pins the JSON names of the MCP wire types; the web's
// types.ts is checked against the same Go source.
func TestMCPTypesWireNames(t *testing.T) {
	server := MCPServer{Args: []string{}, Env: map[string]string{}, Headers: map[string]string{}, HeadersCommand: []string{},
		OAuth: MCPOAuth{Scopes: []string{}}, Roles: []string{}, Tools: MCPToolFilter{Allow: []string{}, Deny: []string{}}}
	state := MCPServerState{Tools: []string{}, Omitted: []MCPOmittedTool{}, Auth: MCPAuthState{Scopes: []string{}}}
	clientState := MCPClientState{Entries: []MCPClientEntry{}}
	cases := []struct {
		v    any
		keys []string
	}{
		{server, []string{"name", "enabled", "description", "transport", "command", "args", "env", "cwd", "instances",
			"url", "headers", "headers_command", "auth", "oauth", "roles", "global", "tools", "timeouts", "source"}},
		{func() MCPServer { s := server; s.Revision = "abc"; return s }(), []string{"name", "enabled", "description", "transport", "command", "args", "env", "cwd", "instances",
			"url", "headers", "headers_command", "auth", "oauth", "roles", "global", "tools", "timeouts", "source", "revision"}},
		{MCPOAuth{Scopes: []string{}}, []string{"client_id", "client_secret", "scopes", "resource"}},
		{MCPToolFilter{Allow: []string{}, Deny: []string{}}, []string{"allow", "deny"}},
		{MCPTimeouts{}, []string{"startup", "call"}},
		{state, []string{"name", "state", "instances", "tools", "omitted", "last_error", "started_at", "auth", "offers_resources", "offers_prompts"}},
		{MCPOmittedTool{}, []string{"tool", "reason"}},
		{MCPAuthState{Scopes: []string{}}, []string{"state", "expires_at", "scopes"}},
		{MCPServersResponse{Servers: []MCPServer{}, States: []MCPServerState{}, Errors: []FileError{}}, []string{"servers", "states", "errors"}},
		{MCPTestResult{Tools: []string{}, Omitted: []MCPOmittedTool{}}, []string{"tools", "omitted", "error", "offers_resources", "offers_prompts", "millis"}},
		{MCPLogLine{}, []string{"at", "message"}},
		{MCPConnect{}, []string{"authorize_url"}},
		{MCPSettings{}, []string{"enabled", "inject"}},
		{MCPInject{}, []string{"claude", "codex"}},
		{MCPToken{}, []string{"id", "kind", "name", "client_id", "redirect_host", "resource", "scope", "created_at", "expires_at", "refresh_expires_at", "last_used_at"}},
		{MCPTokenRequest{}, []string{"name"}},
		{MCPTokenRequest{ExpiresAt: &time.Time{}}, []string{"name", "expires_at"}},
		{MCPTokenCreated{}, []string{"token", "secret"}},
		{MCPClientTarget{}, []string{"id", "cli", "account", "config_path"}},
		{clientState, []string{"target", "state", "entry", "entries", "error"}},
		{MCPClientEntry{Server: server, Notes: []string{}}, []string{"name", "server", "notes", "reason"}},
		{MCPClientPlanRequest{}, []string{"action"}},
		{MCPClientPlan{Commands: [][]string{}}, []string{"target", "action", "file", "commands", "before", "after", "message", "hash"}},
		{MCPClientApply{}, []string{"plan"}},
		{MCPClientApplied{State: clientState}, []string{"state", "backup"}},
		{MCPTakeover{Candidates: []MCPTakeoverCandidate{}, Stays: []MCPTakeoverStay{}}, []string{"candidates", "stays"}},
		{MCPTakeoverCandidate{Server: server, Sources: []MCPTakeoverSource{}, Notes: []string{}}, []string{"key", "name", "server", "sources", "notes", "needs_login", "conflict"}},
		{MCPTakeoverSource{}, []string{"target", "entry_name"}},
		{MCPTakeoverStay{}, []string{"target", "entry_name", "reason"}},
		{MCPTakeoverApply{Take: []MCPTakeoverPick{}}, []string{"take", "remove_from_cli"}},
		{MCPTakeoverPick{}, []string{"key", "name"}},
		{MCPTakeoverReport{Results: []MCPTakeoverResult{}, Backups: []string{}}, []string{"results", "backups"}},
		{MCPTakeoverResult{Removed: []MCPTakeoverRemoval{}}, []string{"name", "stored", "error", "removed", "needs_login"}},
		{MCPTakeoverRemoval{}, []string{"target", "done", "error"}},
		{OAuthConsent{}, []string{"client_name", "client_id", "client_kind", "redirect_host", "localhost_redirect", "resource"}},
		{OAuthDecision{}, []string{"query", "approve"}},
		{OAuthRedirect{}, []string{"redirect"}},
	}
	nullable := map[string]bool{"started_at": true, "expires_at": true, "refresh_expires_at": true, "last_used_at": true,
		"entry": true, "before": true, "after": true}
	for _, c := range cases {
		b, err := json.Marshal(c.v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if len(m) != len(c.keys) {
			t.Errorf("%T: keys %s, want %v", c.v, b, c.keys)
		}
		for _, k := range c.keys {
			raw, ok := m[k]
			if !ok {
				t.Errorf("%T: missing %q in %s", c.v, k, b)
			} else if string(raw) == "null" && !nullable[k] {
				t.Errorf("%T: %q is null in %s", c.v, k, b)
			}
		}
	}
	if KindMCPChanged != "mcp.changed" || KindMCPToolsChanged != "mcp.tools_changed" || SectionMCPServers != "mcpservers" {
		t.Fatal("MCP event kinds or section renamed")
	}
}

// TestMCPServerYAML keeps the server file's keys equal to the JSON names
// and leaves out what is empty; the name and revision are not in the file.
func TestMCPServerYAML(t *testing.T) {
	in := MCPServer{Name: "fs", Enabled: true, Transport: MCPTransportStdio, Command: "npx", Args: []string{"-y", "x"},
		Env: map[string]string{"A": "${B}"}, Instances: MCPInstancesPerSession, Roles: []string{"developer", "export"}, Global: true,
		Tools: MCPToolFilter{Deny: []string{"rm"}}, Timeouts: MCPTimeouts{Call: Duration(10 * time.Minute)}, Revision: "r"}
	y, err := yaml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	want := "enabled: true\ntransport: stdio\ncommand: npx\nargs:\n    - -y\n    - x\nenv:\n    A: ${B}\ninstances: per_session\nroles:\n    - developer\n    - export\nglobal: true\ntools:\n    deny:\n        - rm\ntimeouts:\n    call: 10m\n"
	if string(y) != want {
		t.Fatalf("yaml:\n%s\nwant:\n%s", y, want)
	}
	var back MCPServer
	if err := yaml.Unmarshal(y, &back); err != nil {
		t.Fatal(err)
	}
	if back.Command != "npx" || back.Timeouts.Call != Duration(10*time.Minute) || back.Name != "" || back.Revision != "" {
		t.Fatalf("round trip: %+v", back)
	}
}
