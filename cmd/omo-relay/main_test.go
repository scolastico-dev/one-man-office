package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestCommandOptions(t *testing.T) {
	for _, args := range [][]string{{"-unknown"}, {"unexpected"}, {"-password-file", "missing-secret-file"}} {
		if err := run(context.Background(), args, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"-version"}, &out); err != nil || !strings.Contains(out.String(), "omo-relay ") {
		t.Fatalf("version: %q %v", out.String(), err)
	}
}

func TestExecutableServeAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	address := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- serve(ctx, "127.0.0.1:0", "", func(addr string) { address <- addr })
	}()
	t.Cleanup(func() {
		cancel()
		for range done {
		}
	})
	addr := <-address
	// The running executable path serves the same routes with default admission off.
	r, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+addr+"/notifications/office/"+strings.Repeat("a", 96), nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Omo-Notification-Request", "YQ")
	r.Header.Set("Omo-Notification-MAC", strings.Repeat("A", 64))
	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Fatal(resp.StatusCode)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
