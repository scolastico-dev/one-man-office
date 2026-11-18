package transport

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func roundTrip(t *testing.T, runDir string) {
	t.Helper()
	actual, _, cleanup, err := Endpoint(runDir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ln, err := Listen(actual)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- "accept: " + err.Error()
			return
		}
		defer c.Close()
		b := make([]byte, 1)
		if _, err := io.ReadFull(c, b); err != nil {
			got <- "read: " + err.Error()
			return
		}
		got <- string(b)
	}()

	client, err := ClientEndpoint(runDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := Dial(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "x" {
			t.Fatalf("server got %q", s)
		}
	case <-ctx.Done():
		t.Fatal("timeout")
	}
}

func TestEndpointListenDialRoundTrip(t *testing.T) {
	roundTrip(t, t.TempDir())
}

func TestClientEndpointWithDeepRunDir(t *testing.T) {
	dir := t.TempDir()
	for len(dir) <= 120 {
		dir = filepath.Join(dir, "deeper-directory-name")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, dir)
}

func TestListenReplacesStaleSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes leave no stale file")
	}
	actual, _, cleanup, err := Endpoint(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for i := 0; i < 2; i++ {
		ln, err := Listen(actual)
		if err != nil {
			t.Fatalf("listen %d: %v", i, err)
		}
		// Leave the socket file behind like a crashed server would.
		ln.(interface{ SetUnlinkOnClose(bool) }).SetUnlinkOnClose(false)
		ln.Close()
	}
}

func TestCleanupRemovesSocketAndLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes have no files")
	}
	runDir := t.TempDir()
	actual, display, cleanup, err := Endpoint(runDir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := Listen(actual)
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	if !strings.HasPrefix(display, runDir) {
		t.Fatalf("display %q outside run dir", display)
	}
	cleanup()
	if _, err := os.Lstat(display); !os.IsNotExist(err) {
		t.Fatalf("link still present: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(actual)); !os.IsNotExist(err) {
		t.Fatalf("socket dir still present: %v", err)
	}
}
