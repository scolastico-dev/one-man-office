package apiclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func TestProxyForwardClient(t *testing.T) {
	n := 17
	want := api.CreateProxyForward{Ticket: &n, Port: 5173, Host: "::1", Name: "dev", Scheme: "https", StartPath: "/?q=a%2Fb", StripPrefix: true, VerifyTLS: true}
	step := 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(SessionHeader) != "agent" {
			t.Error("session omitted")
		}
		switch step {
		case 0:
			if r.Method != "POST" || r.URL.Path != "/api/proxy/forwards" || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("create request: %s %s", r.Method, r.URL)
			}
			var got api.CreateProxyForward
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.Ticket == nil || *got.Ticket != n || got.Port != want.Port || got.StartPath != want.StartPath || !got.StripPrefix || !got.VerifyTLS {
				t.Errorf("create body: %+v", got)
			}
			io.WriteString(w, `{"id":"abc","host":"::1","port":5173}`)
		case 1, 2:
			if r.Method != "GET" || r.URL.Path != "/api/proxy/forwards" {
				t.Errorf("list request: %s %s", r.Method, r.URL)
			}
			if step == 1 && r.URL.RawQuery != "ticket=17" || step == 2 && r.URL.RawQuery != "" {
				t.Errorf("query %q", r.URL.RawQuery)
			}
			io.WriteString(w, `{"forwards":[]}`)
		case 3:
			if r.Method != "DELETE" || r.URL.Path != "/api/proxy/forwards/abc" {
				t.Errorf("delete request: %s %s", r.Method, r.URL)
			}
			w.WriteHeader(http.StatusNoContent)
		}
		step++
	})
	c := NewWithTransport(HandlerTransport(h), "agent")
	ctx := context.Background()
	if f, err := c.CreateProxyForward(ctx, want); err != nil || f.ID != "abc" {
		t.Fatalf("create: %+v %v", f, err)
	}
	for _, filter := range []*int{&n, nil} {
		if list, err := c.ProxyForwards(ctx, filter); err != nil || list.Forwards == nil {
			t.Fatalf("list: %+v %v", list, err)
		}
	}
	if err := c.DeleteProxyForward(ctx, "abc"); err != nil {
		t.Fatal(err)
	}
	if step != 4 {
		t.Fatalf("%d requests", step)
	}
}
