package mcpserver

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

func TestProxyToolsUseSharedCommands(t *testing.T) {
	f := newFixture()
	var created api.CreateProxyForward
	removed := false
	f.opts.Dispatch = func(id Identity) *apiclient.Client {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(apiclient.SessionHeader) != "s1" {
				t.Error("missing MCP identity")
			}
			switch {
			case r.URL.Path == "/api/me":
				n := 7
				json.NewEncoder(w).Encode(api.Me{Actor: api.Actor{Role: api.RoleDeveloper}, Ticket: &n})
			case r.URL.Path == "/api/proxy/forwards" && r.Method == "POST":
				if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
					t.Error(err)
				}
				json.NewEncoder(w).Encode(api.ProxyForward{ID: "abc", Ticket: created.Ticket, Name: created.Name, Port: created.Port, BasePath: "/proxy/abc/", Address: "http://omo/proxy/abc/"})
			case r.URL.Path == "/api/proxy/forwards" && r.Method == "GET":
				if r.URL.Query().Get("ticket") != "7" {
					t.Errorf("MCP list default %s", r.URL)
				}
				json.NewEncoder(w).Encode(api.ProxyForwardList{Forwards: []api.ProxyForward{}})
			case r.URL.Path == "/api/proxy/forwards/abc" && r.Method == "DELETE":
				removed = true
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Errorf("unexpected request %s %s", r.Method, r.URL)
				w.WriteHeader(http.StatusNotFound)
			}
		})
		return apiclient.NewWithTransport(apiclient.HandlerTransport(h), id.Session)
	}
	cs := f.connect(t, agent(audiences[2]))
	names := toolNames(t, cs)
	for _, name := range []string{"proxy_add", "proxy_list", "proxy_remove"} {
		if !slices.Contains(names, name) {
			t.Fatalf("tool %s missing", name)
		}
	}
	added := call(t, cs, "proxy_add", map[string]any{"port": 5173, "name": "dev", "host": "::1", "strip_prefix": true, "verify_tls": true})
	if added.IsError || created.Ticket == nil || *created.Ticket != 7 || created.Host != "::1" || !created.StripPrefix || !created.VerifyTLS || !strings.Contains(text(added), "/proxy/abc/") {
		t.Fatalf("add: %+v %s", created, text(added))
	}
	if added.StructuredContent == nil {
		t.Fatal("missing structured record")
	}
	listed := call(t, cs, "proxy_list", map[string]any{})
	if listed.IsError || listed.StructuredContent == nil {
		t.Fatalf("list: %+v", listed)
	}
	result := call(t, cs, "proxy_remove", map[string]any{"id": "abc"})
	if result.IsError || !removed {
		t.Fatalf("remove: %s", text(result))
	}
	// The shared schema makes port and name mandatory for MCP callers.
	bad := call(t, cs, "proxy_add", map[string]any{"port": 5173})
	if !bad.IsError {
		t.Fatal("MCP accepted missing name")
	}
}
