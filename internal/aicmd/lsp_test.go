package aicmd

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/apiclient"
)

func TestLSPIgnoreIsOneCommandForCLIAndMCP(t *testing.T) {
	c, ok := byTool("lsp_ignore")
	if !ok {
		t.Fatal("missing shared CLI/MCP command lsp_ignore")
	}
	if strings.Join(c.Path, " ") != "lsp ignore" || c.CommandLineOnly {
		t.Fatalf("path %v, command line only %v", c.Path, c.CommandLineOnly)
	}
	for _, a := range audiences {
		want := a.Role == api.RolePM || a.Role == api.RoleDeveloper || a.Role == api.RoleReviewer
		if c.Sees(a) != want {
			t.Errorf("%s/%s sees %v, want %v", a.Role, a.Purpose, c.Sees(a), want)
		}
	}
}

func TestLSPIgnoreConfirmsSuppressionInTheWorktree(t *testing.T) {
	var got api.LSPIgnoreRequest
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(api.LSPIgnoreResult{ID: got.ID, Ignored: true, Worktree: "/work/tree", File: "src/a.go", Line: 12, Column: 5, Message: spoofText})
	})
	client := apiclient.NewWithTransport(apiclient.HandlerTransport(h), "")
	c, _ := byTool("lsp_ignore")
	res, err := c.Run(context.Background(), client, proxyTestInput(c, `{"id":"deadbeef"}`, t))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "deadbeef" {
		t.Fatalf("sent %+v", got)
	}
	for _, want := range []string{"deadbeef", "src/a.go:12:5", "/work/tree", "no longer"} {
		contains(t, res.Text, want)
	}
	if strings.ContainsAny(res.Text, "\r\x1b‮") {
		t.Errorf("unsafe output: %q", res.Text)
	}
	if !strings.Contains(string(res.Value), `"ignored":true`) {
		t.Fatalf("raw answer %s", res.Value)
	}
}

func TestLSPConfirmIsOneCommandForEveryAgentSession(t *testing.T) {
	c, ok := byTool("lsp_confirm")
	if !ok || strings.Join(c.Path, " ") != "lsp confirm" || c.CommandLineOnly {
		t.Fatalf("lsp confirm: %v %+v", ok, c.Path)
	}
	for _, a := range audiences {
		if !c.Sees(a) {
			t.Errorf("%s/%s does not see it", a.Role, a.Purpose)
		}
	}
	var got api.LSPConfirmRequest
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		json.NewEncoder(w).Encode(api.LSPConfirmResult{Confirmed: true})
	})
	client := apiclient.NewWithTransport(apiclient.HandlerTransport(h), "")
	res, err := c.Run(context.Background(), client, proxyTestInput(c, `{"token":"abc123"}`, t))
	if err != nil || got.Token != "abc123" || !strings.Contains(res.Text, "confirmed") || !strings.Contains(string(res.Value), `"confirmed":true`) {
		t.Fatalf("%+v %v %+v", got, err, res)
	}
}
