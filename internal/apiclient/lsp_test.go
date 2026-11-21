package apiclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestLSPIgnoreClient(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/lsp/ignore" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get(SessionHeader) != "agent" {
			t.Errorf("request %s %s %v", r.Method, r.URL, r.Header)
		}
		var got api.LSPIgnoreRequest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got.ID != "abc" {
			t.Errorf("body %+v %v", got, err)
		}
		io.WriteString(w, `{"id":"abc","ignored":true,"worktree":"/w","file":"a.go","line":3,"column":4,"message":"m"}`)
	})
	c := NewWithTransport(HandlerTransport(h), "agent")
	res, err := c.LSPIgnore(context.Background(), "abc")
	if err != nil || !res.Ignored || res.File != "a.go" || res.Line != 3 {
		t.Fatalf("%+v %v", res, err)
	}
}
