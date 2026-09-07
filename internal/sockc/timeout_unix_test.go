//go:build !windows

package sockc

import (
	"bufio"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestCallTimeoutClosesUnresponsiveConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		if _, err := reader.ReadString('\n'); err != nil {
			return
		}
		if _, err := reader.ReadByte(); err != nil {
			close(closed)
		}
	}()
	start := time.Now()
	if err := CallTimeout(path, "user", "office.estop", nil, nil, 50*time.Millisecond); err == nil {
		t.Fatal("unresponsive call succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("call exceeded deadline")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("timed-out connection was not closed")
	}
}
