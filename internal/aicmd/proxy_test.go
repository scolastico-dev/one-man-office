package aicmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

func TestProxyCommandsSharedDefinition(t *testing.T) {
	for _, name := range []string{"proxy_add", "proxy_list", "proxy_remove"} {
		c, ok := byTool(name)
		if !ok {
			t.Fatalf("missing shared CLI/MCP command %s", name)
		}
		if c.CommandLineOnly {
			t.Errorf("%s is not exposed over MCP", name)
		}
		for _, a := range audiences {
			if !c.Sees(a) {
				t.Errorf("%s hidden from %s", name, a.Role)
			}
		}
	}
}

func TestProxyCommandsIdentityAndOutput(t *testing.T) {
	for _, role := range []api.Role{api.RoleUser, api.RoleDeveloper} {
		t.Run(string(role), func(t *testing.T) {
			n := 7
			var req api.CreateProxyForward
			var query string
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/api/me":
					me := api.Me{Actor: api.Actor{Role: role}}
					if role != api.RoleUser {
						me.Ticket = &n
					}
					json.NewEncoder(w).Encode(me)
				case r.Method == "POST":
					json.NewDecoder(r.Body).Decode(&req)
					json.NewEncoder(w).Encode(api.ProxyForward{ID: "abc", Name: spoofText, Host: spoofText, StartPath: spoofText, Address: "http://omo/proxy/abc/", BasePath: "/proxy/abc/"})
				case r.Method == "GET":
					query = r.URL.RawQuery
					io.WriteString(w, `{"forwards":[]}`)
				case r.Method == "DELETE":
					if r.URL.Path != "/api/proxy/forwards/abc" {
						t.Errorf("delete path %s", r.URL)
					}
					w.WriteHeader(http.StatusNoContent)
				}
			})
			client := apiclient.NewWithTransport(apiclient.HandlerTransport(h), "")
			ctx := context.Background()
			add, _ := byTool("proxy_add")
			// Decode just as an MCP tool does, into the shared input type.
			input := proxyTestInput(add, `{"port":5173,"name":"dev"}`, t)
			result, err := add.Run(ctx, client, input)
			if err != nil {
				t.Fatal(err)
			}
			if req.Port != 5173 || req.Name != "dev" || (role == api.RoleUser && req.Ticket != nil) || (role == api.RoleDeveloper && (req.Ticket == nil || *req.Ticket != n)) {
				t.Fatalf("create: %+v", req)
			}
			for _, text := range []string{"abc", "http://omo/proxy/abc/", "/proxy/abc/", "not started by omo"} {
				contains(t, result.Text, text)
			}
			if strings.ContainsAny(result.Text, "\r\x1b‮ ") {
				t.Errorf("unsafe output: %q", result.Text)
			}
			list, _ := byTool("proxy_list")
			result, err = list.Run(ctx, client, proxyTestInput(list, `{}`, t))
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if role == api.RoleDeveloper {
				want = "ticket=7"
			}
			if query != want {
				t.Errorf("list query %q want %q", query, want)
			}
			if !strings.Contains(string(result.Value), `"forwards":[]`) {
				t.Fatalf("raw result %s", result.Value)
			}
			_, err = list.Run(ctx, client, proxyTestInput(list, `{"ticket":12}`, t))
			if err != nil || query != "ticket=12" {
				t.Fatalf("explicit list %s %v", query, err)
			}
			remove, _ := byTool("proxy_remove")
			if _, err := remove.Run(ctx, client, proxyTestInput(remove, `{"id":"abc"}`, t)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func proxyTestInput(c Command, raw string, t *testing.T) any {
	t.Helper()
	in := reflect.New(c.Input).Interface()
	if err := json.Unmarshal([]byte(raw), in); err != nil {
		t.Fatal(err)
	}
	return in
}
