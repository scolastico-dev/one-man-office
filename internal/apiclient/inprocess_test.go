package apiclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// The in-process client sends the same request a socket client sends and
// reads the answer the same way.
func TestInProcessClient(t *testing.T) {
	var gotSession, gotType, gotBody, gotURI string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSession, gotType, gotURI = r.Header.Get(SessionHeader), r.Header.Get("Content-Type"), r.RequestURI
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		if r.URL.Path == "/api/tickets/9" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, `{"error":{"code":"forbidden","message":"not yours"}}`)
			return
		}
		io.WriteString(w, `{"actor":{"role":"developer","name":"ada"}}`)
	})
	c := NewWithTransport(HandlerTransport(h), "sess-1")
	me, err := c.Me(context.Background())
	if err != nil || me.Actor.Name != "ada" || gotSession != "sess-1" || gotURI != "/api/me" {
		t.Fatalf("me: %+v %v %q %q", me, err, gotSession, gotURI)
	}
	if string(c.LastBody()) != `{"actor":{"role":"developer","name":"ada"}}` {
		t.Fatalf("last body %q", c.LastBody())
	}
	if _, err := c.Comment(context.Background(), 3, api.CommentRequest{Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	if gotType != "application/json" || gotBody != `{"body":"hi","question":false}` {
		t.Fatalf("post: %q %q", gotType, gotBody)
	}
	_, err = c.Ticket(context.Background(), 9)
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Code != api.CodeForbidden || apiErr.Status != http.StatusForbidden {
		t.Fatalf("refusal: %v", err)
	}
	if _, err := NewWithTransport(HandlerTransport(h), "").Me(context.Background()); err != nil || gotSession != "" {
		t.Fatalf("user: %v %q", err, gotSession)
	}
}

// Cancelling the caller's context reaches the handler.
func TestInProcessClientPassesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	})
	if _, err := NewWithTransport(HandlerTransport(h), "").Me(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}
