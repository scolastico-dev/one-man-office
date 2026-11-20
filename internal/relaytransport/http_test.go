package relaytransport

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

func TestHTTPRealFlushingAndOneRequestPerStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &recordPeer{in: make(chan relayproto.PlainRecord), out: make(chan relayproto.PlainRecord), done: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		released := make(chan struct{})
		handled := make(chan struct{})
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(handled)
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "first\n")
			if e := http.NewResponseController(w).Flush(); e != nil {
				t.Error(e)
			}
			select {
			case <-released:
				io.WriteString(w, "last\n")
			case <-r.Context().Done():
			}
		})
		go func() { done <- ServeHTTP(ctx, p, func() error { return nil }, h, nil) }()
		p.in <- record("stream.open", 1)
		if r := <-p.out; r.Type != "stream.opened" {
			t.Fatal(r.Type)
		}
		r := record("stream.data", 1)
		r.Data = []byte("GET / HTTP/1.1\r\nHost: omo-relay.invalid\r\n\r\n")
		p.in <- r
		var response string
		for !strings.Contains(response, "first\n") {
			r := <-p.out
			if r.Type == "stream.data" {
				response += string(r.Data)
			}
		}
		if !strings.Contains(response, "200 OK") || !strings.Contains(response, "Connection: close") {
			t.Fatal(response)
		}
		close(released)
		<-handled
		for {
			r := <-p.out
			if r.Type == "stream.fin" {
				break
			}
		}
		cancel()
		<-done
	})
}

// net/http must be able to Hijack the actual virtual connection, keeping every
// byte after the upgrade rather than converting an upgrade into a buffered body.
func TestHTTPHijackAndCancellationJoin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := &recordPeer{in: make(chan relayproto.PlainRecord), out: make(chan relayproto.PlainRecord), done: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		exited := make(chan struct{})
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer close(exited)
			c, rw, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			defer c.Close()
			rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\n")
			rw.Flush()
			io.Copy(c, rw)
		})
		go func() {
			done <- ServeHTTP(ctx, p, func() error { return nil }, h, func(ctx context.Context, _ net.Conn) context.Context { return ctx })
		}()
		p.in <- record("stream.open", 1)
		<-p.out
		r := record("stream.data", 1)
		r.Data = []byte("GET / HTTP/1.1\r\nHost: omo-relay.invalid\r\nConnection: Upgrade\r\nUpgrade: test\r\n\r\nhello")
		p.in <- r
		var data string
		for !strings.HasSuffix(data, "hello") {
			r := <-p.out
			if r.Type == "stream.data" {
				data += string(r.Data)
			}
		}
		response, e := http.ReadResponse(bufio.NewReader(strings.NewReader(data)), nil)
		if e != nil || response.StatusCode != 101 {
			t.Fatal(response, e)
		}
		cancel()
		<-done
		select {
		case <-exited:
		default:
			t.Fatal("ServeHTTP returned before hijacked handler")
		}
	})
}
