package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// mcpSecret is a value that must never appear in a refusal or file error.
const mcpSecret = "sk-mcp-secret-41c7"

func filesystemServer() MCPServer {
	return MCPServer{
		Name:      "filesystem",
		Enabled:   true,
		Transport: api.MCPTransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "."},
		Env:       map[string]string{"API_KEY": mcpSecret},
		Instances: api.MCPInstancesPerSession,
		Headers:   map[string]string{},
		Roles:     []string{"developer"},
	}
}

func remoteServer() MCPServer {
	return MCPServer{
		Name:      "remote",
		Enabled:   true,
		Transport: api.MCPTransportHTTP,
		URL:       "https://mcp.example.invalid/mcp",
		Headers:   map[string]string{"Authorization": "Bearer " + mcpSecret},
		Auth:      api.MCPAuthHeaders,
	}
}

func TestMCPServerValidation(t *testing.T) {
	s, home, _ := openTest(t)
	cases := map[string]struct {
		base   func() MCPServer
		mutate func(*MCPServer)
	}{
		"name":                  {filesystemServer, func(m *MCPServer) { m.Name = "Bad" }},
		"name ":                 {filesystemServer, func(m *MCPServer) { m.Name = strings.Repeat("a", 25) }},
		"name  ":                {filesystemServer, func(m *MCPServer) { m.Name = "with_underscore" }},
		"name   ":               {filesystemServer, func(m *MCPServer) { m.Name = "1abc" }},
		"transport":             {filesystemServer, func(m *MCPServer) { m.Transport = "ws" }},
		"transport ":            {filesystemServer, func(m *MCPServer) { m.Transport = "" }},
		"command":               {filesystemServer, func(m *MCPServer) { m.Command = "" }},
		"command ":              {filesystemServer, func(m *MCPServer) { m.Command = "a\nb" }},
		"cwd":                   {filesystemServer, func(m *MCPServer) { m.Cwd = "relative/folder" }},
		"auth":                  {filesystemServer, func(m *MCPServer) { m.Auth = api.MCPAuthOAuth }},
		"instances":             {filesystemServer, func(m *MCPServer) { m.Instances = "many" }},
		"args":                  {filesystemServer, func(m *MCPServer) { m.Args = make([]string, 257) }},
		"args.1":                {filesystemServer, func(m *MCPServer) { m.Args = []string{"ok", "a\u202eb"} }},
		"env":                   {filesystemServer, func(m *MCPServer) { m.Env = map[string]string{"BAD NAME": mcpSecret} }},
		"env ":                  {filesystemServer, func(m *MCPServer) { m.Env = map[string]string{"A=" + mcpSecret: "x"} }},
		"env  ":                 {filesystemServer, func(m *MCPServer) { m.Env = manyVars(65) }},
		"env.KEY":               {filesystemServer, func(m *MCPServer) { m.Env = map[string]string{"KEY": mcpSecret + "\x00"} }},
		"env.BIG":               {filesystemServer, func(m *MCPServer) { m.Env = map[string]string{"BIG": strings.Repeat(mcpSecret, 3000)} }},
		"roles.1":               {filesystemServer, func(m *MCPServer) { m.Roles = []string{"developer", "user"} }},
		"roles.2":               {filesystemServer, func(m *MCPServer) { m.Roles = []string{"developer", "pm", "developer"} }},
		"tools.allow.0":         {filesystemServer, func(m *MCPServer) { m.Tools.Allow = []string{strings.Repeat("t", 129)} }},
		"tools.deny.1":          {filesystemServer, func(m *MCPServer) { m.Tools.Deny = []string{"a", "a"} }},
		"tools.deny.0":          {filesystemServer, func(m *MCPServer) { m.Tools.Deny = []string{""} }},
		"timeouts.startup":      {filesystemServer, func(m *MCPServer) { m.Timeouts.Startup = Duration(time.Hour) }},
		"timeouts.call":         {filesystemServer, func(m *MCPServer) { m.Timeouts.Call = Duration(25 * time.Hour) }},
		"timeouts.call ":        {filesystemServer, func(m *MCPServer) { m.Timeouts.Call = Duration(time.Millisecond) }},
		"description":           {filesystemServer, func(m *MCPServer) { m.Description = "two\nlines" }},
		"description ":          {filesystemServer, func(m *MCPServer) { m.Description = strings.Repeat("d", 201) }},
		"source":                {filesystemServer, func(m *MCPServer) { m.Source = "a\u200bb" }},
		"oauth":                 {filesystemServer, func(m *MCPServer) { m.OAuth.ClientID = "id" }},
		"url":                   {remoteServer, func(m *MCPServer) { m.URL = "" }},
		"url ":                  {remoteServer, func(m *MCPServer) { m.URL = "ftp://mcp.example.invalid/x" }},
		"url  ":                 {remoteServer, func(m *MCPServer) { m.URL = "https:///mcp" }},
		"url   ":                {remoteServer, func(m *MCPServer) { m.URL = "https://me:" + mcpSecret + "@mcp.example.invalid/" }},
		"url    ":               {remoteServer, func(m *MCPServer) { m.URL = "https://mcp.example.invalid/mcp#" + mcpSecret }},
		"url     ":              {remoteServer, func(m *MCPServer) { m.URL = "mcp.example.invalid/mcp" }},
		"instances ":            {remoteServer, func(m *MCPServer) { m.Instances = api.MCPInstancesPerSession }},
		"auth ":                 {remoteServer, func(m *MCPServer) { m.Auth = "basic" }},
		"headers":               {remoteServer, func(m *MCPServer) { m.Headers = map[string]string{"X: Y": mcpSecret} }},
		"headers.Authorization": {remoteServer, func(m *MCPServer) { m.Headers = map[string]string{"Authorization": mcpSecret + "\r\nX-Evil: 1"} }},
		"headers_command.0":     {remoteServer, func(m *MCPServer) { m.HeadersCommand = []string{""} }},
		"oauth.client_secret": {remoteServer, func(m *MCPServer) {
			m.Auth = api.MCPAuthOAuth
			m.OAuth.ClientSecret = mcpSecret
		}},
		"oauth.scopes.0": {remoteServer, func(m *MCPServer) {
			m.Auth = api.MCPAuthOAuth
			m.OAuth.Scopes = []string{"a b"}
		}},
		"oauth.resource": {remoteServer, func(m *MCPServer) {
			m.Auth = api.MCPAuthOAuth
			m.OAuth.Resource = "not an address"
		}},
	}
	for name, c := range cases {
		m := c.base()
		c.mutate(&m)
		err := s.PutMCPServerIf("", m)
		wantCode(t, err, api.CodeValidation)
		if got := fieldOf(err); got != strings.TrimSpace(name) {
			t.Errorf("%q: field %q, err %v", name, got, err)
		}
		if strings.Contains(err.Error(), mcpSecret) {
			t.Errorf("%q: message quotes the value: %v", name, err)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(home, "mcpservers")); len(entries) != 0 {
		t.Fatalf("refused servers were written: %v", entries)
	}

	// Accepted: names up to 24 characters, every transport and auth kind.
	ok := []MCPServer{filesystemServer(), remoteServer()}
	long := filesystemServer()
	long.Name = "a" + strings.Repeat("-b", 11) + "c"
	ok = append(ok, long)
	sse := remoteServer()
	sse.Name, sse.Transport = "events", api.MCPTransportSSE
	ok = append(ok, sse)
	oauth := remoteServer()
	oauth.Name, oauth.Auth, oauth.Headers = "github", api.MCPAuthOAuth, nil
	oauth.OAuth = api.MCPOAuth{ClientID: "id", ClientSecret: mcpSecret, Scopes: []string{"repo", "read:org"}, Resource: "https://api.example.invalid/mcp"}
	oauth.Global = true
	ok = append(ok, oauth)
	for _, m := range ok {
		if err := s.PutMCPServerIf(NoRevision, m); err != nil {
			t.Fatalf("%s: %v", m.Name, err)
		}
	}
	if got := len(long.Name); got != 24 {
		t.Fatalf("the long name has %d characters", got)
	}

	// A hand-written file is validated the same way, and its message
	// quotes nothing.
	writeFile(t, filepath.Join(home, "mcpservers", "hand.yaml"), "transport: http\nurl: https://me:"+mcpSecret+"@h.invalid/\n")
	writeFile(t, filepath.Join(home, "mcpservers", "nocommand.yaml"), "transport: stdio\nenv:\n  KEY: "+mcpSecret+"\n")
	writeFile(t, filepath.Join(home, "mcpservers", "Bad_Name.yaml"), "transport: stdio\ncommand: x\n")
	s.Reload()
	errs := s.Errors()
	if len(errs) != 3 {
		t.Fatalf("errors = %+v", errs)
	}
	for _, e := range errs {
		if strings.Contains(e.Message, mcpSecret) {
			t.Fatalf("file error quotes a value: %+v", e)
		}
	}
	for _, name := range []string{"hand", "nocommand"} {
		if _, ok := s.MCPServer(name); ok {
			t.Fatalf("broken server %s loaded", name)
		}
	}
}

func manyVars(n int) map[string]string {
	out := map[string]string{}
	for i := range n {
		out["V"+strings.Repeat("X", i)] = "x"
	}
	return out
}

func TestMCPServerDefaultsAndFile(t *testing.T) {
	s, home, _ := openTest(t)
	writeFile(t, filepath.Join(home, "mcpservers", "fs.yaml"), "enabled: true\ntransport: stdio\ncommand: fs-server\n")
	writeFile(t, filepath.Join(home, "mcpservers", "web.yaml"), "transport: http\nurl: https://h.invalid/mcp\nheaders:\n  X-Key: v\n")
	writeFile(t, filepath.Join(home, "mcpservers", "open.yaml"), "transport: sse\nurl: https://h.invalid/sse\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 0 {
		t.Fatalf("errors = %+v", errs)
	}
	fs, _ := s.MCPServer("fs")
	if fs.Instances != api.MCPInstancesPerSession || fs.Global || fs.Args == nil || fs.Env == nil || fs.Roles == nil || fs.Tools.Allow == nil {
		t.Fatalf("fs = %+v", fs)
	}
	if web, _ := s.MCPServer("web"); web.Auth != api.MCPAuthHeaders || web.Instances != "" || web.Enabled {
		t.Fatalf("web = %+v", web)
	}
	if open, _ := s.MCPServer("open"); open.Auth != api.MCPAuthNone {
		t.Fatalf("open = %+v", open)
	}

	// A written file holds no name and no revision, keeps global off
	// unless set, and is read back as written.
	m := remoteServer()
	m.Global = true
	m.Timeouts = api.MCPTimeouts{Startup: Duration(45 * time.Second), Call: Duration(2 * time.Hour)}
	m.Tools = api.MCPToolFilter{Allow: []string{"search"}, Deny: []string{"delete"}}
	if err := s.PutMCPServerIf(NoRevision, m); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "mcpservers", "remote.yaml"))
	for _, want := range []string{"transport: http\n", "global: true\n", "startup: 45s\n", "call: 2h\n"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("file lacks %q:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), "name:") || strings.Contains(string(raw), "revision") {
		t.Fatalf("file holds name or revision:\n%s", raw)
	}
	got, _ := s.MCPServer("remote")
	got.Revision = ""
	want := normalizeMCPServer(m)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("server = %+v, want %+v", got, want)
	}
	if list := s.MCPServers(); len(list) != 4 || list[0].Name != "fs" || list[3].Name != "web" {
		t.Fatalf("list = %+v", list)
	}
}

func TestMCPServerRevisions(t *testing.T) {
	s, home, log := openTest(t)
	m := filesystemServer()
	if err := s.PutMCPServerIf(NoRevision, m); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.PutMCPServerIf(NoRevision, m), api.CodeStaleSettings)
	got, ok := s.MCPServer("filesystem")
	rev := s.Revision(SectionMCPServers, "filesystem")
	if !ok || got.Revision != rev || rev == NoRevision {
		t.Fatalf("server = %+v, revision %q", got, rev)
	}
	if ch := log.take(); len(ch) != 1 || ch[0].Section != SectionMCPServers || ch[0].Name != "filesystem" || ch[0].FromDisk {
		t.Fatalf("changes = %+v", ch)
	}
	// A hand edit not yet polled makes the page's revision stale.
	writeFile(t, filepath.Join(home, "mcpservers", "filesystem.yaml"), "transport: stdio\ncommand: other\n")
	wantCode(t, s.PutMCPServerIf(rev, m), api.CodeStaleSettings)
	if ch := log.take(); len(ch) != 1 || !ch[0].FromDisk {
		t.Fatalf("changes = %+v", ch)
	}
	wantCode(t, s.DeleteMCPServerIf(rev, "filesystem"), api.CodeStaleSettings)
	if err := s.DeleteMCPServerIf(s.Revision(SectionMCPServers, "filesystem"), "filesystem"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.MCPServer("filesystem"); ok || fileExists(filepath.Join(home, "mcpservers", "filesystem.yaml")) {
		t.Fatal("server not deleted")
	}
	if ch := log.take(); len(ch) != 1 || !ch[0].Removed {
		t.Fatalf("changes = %+v", ch)
	}
	wantCode(t, s.DeleteMCPServerIf("", "filesystem"), api.CodeNotFound)
	wantCode(t, s.DeleteMCPServerIf("", "../x"), api.CodeNotFound)
	// A broken file can be deleted.
	writeFile(t, filepath.Join(home, "mcpservers", "broken.yaml"), "transport: [")
	s.Reload()
	if err := s.DeleteMCPServerIf("", "broken"); err != nil {
		t.Fatal(err)
	}
	if len(s.Errors()) != 0 {
		t.Fatalf("errors = %+v", s.Errors())
	}
}
