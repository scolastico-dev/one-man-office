package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
)

func TestProtocolRequestsNotificationsAndServerRequests(t *testing.T) {
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	defer clientOut.Close()
	defer serverOut.Close()
	got := make(chan string, 1)
	p := newPeer(clientIn, clientOut, func(method string, params json.RawMessage) (any, error) {
		got <- method
		return map[string]any{"answer": 42}, nil
	})
	defer p.close()
	serverDone := make(chan error, 1)
	go func() {
		r := bufio.NewReader(serverIn)
		req, err := readMessage(r)
		if err != nil {
			serverDone <- err
			return
		}
		err = writeMessage(serverOut, message{JSONRPC: "2.0", ID: json.RawMessage(`"server"`), Method: "workspace/configuration", Params: json.RawMessage(`{}`)})
		if err != nil {
			serverDone <- err
			return
		}
		response, err := readMessage(r)
		if err == nil && string(response.Result) != `{"answer":42}` {
			err = io.ErrUnexpectedEOF
		}
		if err != nil {
			serverDone <- err
			return
		}
		serverDone <- writeMessage(serverOut, message{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"capabilities":{}}`)})
	}()
	var result struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := p.call(context.Background(), "initialize", map[string]any{}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Capabilities == nil {
		t.Fatal("response lost")
	}
	if <-got != "workspace/configuration" {
		t.Fatal("server request lost")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}
func TestProtocolRejectsInvalidFrames(t *testing.T) {
	for _, s := range []string{"Content-Length: -1\r\n\r\n", "Content-Length: 999999999\r\n\r\n", "X: x\r\n\r\n", "Content-Length: 2\r\n\r\nxx"} {
		if _, err := readMessage(bufio.NewReader(strings.NewReader(s))); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestProtocolCancellationUnblocksStalledWriter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		in, serverOut := io.Pipe()
		serverIn, out := io.Pipe()
		defer serverOut.Close()
		defer serverIn.Close()
		p := newPeer(in, out, nil)
		defer p.close()
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() { result <- p.call(ctx, "initialize", map[string]any{}, nil) }()
		synctest.Wait() // The server deliberately never reads the request.
		cancel()
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	})
}
