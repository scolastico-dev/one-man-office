package testport

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestAcquireRecoversOnlyFromBindCollision(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	first := busy.Addr().(*net.TCPAddr).Port
	allocations, attempts := 0, 0
	var acquired net.Listener
	t.Cleanup(func() {
		if acquired != nil {
			acquired.Close()
		}
	})
	port, err := acquire(func() (int, error) {
		allocations++
		if allocations == 1 {
			return first, nil
		}
		return candidate()
	}, func(port int) error {
		attempts++
		var err error
		acquired, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || port == first || acquired == nil {
		t.Fatalf("attempts=%d port=%d listener=%v", attempts, port, acquired)
	}
}

func TestAcquirePropagatesOtherFailuresWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"invalid options", errors.New("invalid start options")},
		{"unix listener", &net.OpError{Op: "listen", Net: "unix", Addr: &net.UnixAddr{Name: "/fixture.sock", Net: "unix"}, Err: syscall.EADDRINUSE}},
		{"other TCP port", tcpError("listen", "tcp", 43, syscall.EADDRINUSE)},
		{"other Windows TCP port", tcpError("listen", "tcp", 43, syscall.Errno(10048))},
		{"other component diagnostic", errors.New("hook failed: listen tcp 127.0.0.1:43: bind: address already in use")},
		{"other HTTP port diagnostic", errors.New("omo: listen on 127.0.0.1:43: listen tcp 127.0.0.1:43: bind: address already in use")},
		{"embedded diagnostic at expected port", errors.New("hook failed: listen on 127.0.0.1:42: listen tcp 127.0.0.1:42: bind: address already in use")},
		{"permission denied", tcpError("listen", "tcp", 42, syscall.EACCES)},
		{"dial collision", tcpError("dial", "tcp", 42, syscall.EADDRINUSE)},
		{"UDP listener", tcpError("listen", "udp", 42, syscall.EADDRINUSE)},
		{"missing address", &net.OpError{Op: "listen", Net: "tcp", Err: syscall.EADDRINUSE}},
		{"nil TCP address", &net.OpError{Op: "listen", Net: "tcp", Addr: (*net.TCPAddr)(nil), Err: syscall.EADDRINUSE}},
		{"wrong address type", &net.OpError{Op: "listen", Net: "tcp", Addr: &net.UnixAddr{Name: "/fixture.sock", Net: "unix"}, Err: syscall.EADDRINUSE}},
		{"typed error overrides diagnostic", fmt.Errorf("listen on 127.0.0.1:42: listen tcp 127.0.0.1:42: bind: address already in use\n%w", tcpError("listen", "tcp", 43, syscall.EADDRINUSE))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocations, calls := 0, 0
			port, err := acquire(func() (int, error) { allocations++; return 42, nil }, func(int) error { calls++; return tc.err })
			if err != tc.err || calls != 1 || allocations != 1 || port != 42 {
				t.Fatalf("allocations=%d calls=%d port=%d error=%v; want original error without retry", allocations, calls, port, err)
			}
		})
	}
}

func tcpError(op, network string, port int, cause error) error {
	return &net.OpError{Op: op, Net: network, Addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: port}, Err: os.NewSyscallError("bind", cause)}
}

func TestAcquireReallocatesExpectedTCPPort(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"TCP", tcpError("listen", "tcp", 42, syscall.EADDRINUSE)},
		{"Winsock TCP", tcpError("listen", "tcp", 42, syscall.Errno(10048))},
		{"wrapped TCP4", fmt.Errorf("fixture startup: %w", tcpError("listen", "tcp4", 42, syscall.EADDRINUSE))},
		{"TCP6", &net.OpError{Op: "listen", Net: "tcp6", Addr: &net.TCPAddr{IP: net.ParseIP("::1"), Port: 42}, Err: os.NewSyscallError("bind", syscall.EADDRINUSE)}},
		{"Unix diagnostic", errors.New("fixture startup: exit status 1\nomo: listen on 127.0.0.1:42: listen tcp 127.0.0.1:42: bind: address already in use\n")},
		{"Windows diagnostic", errors.New("fixture startup: exit status 1\r\nomo: listen on 127.0.0.1:42: listen tcp 127.0.0.1:42: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.\r\n")},
		{"TCP4 diagnostic", errors.New("listen on 127.0.0.1:42: listen tcp4 127.0.0.1:42: bind: address already in use")},
		{"TCP6 diagnostic", errors.New("listen on [::1]:42: listen tcp6 [::1]:42: bind: address already in use")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allocations, calls := 0, 0
			port, err := acquire(func() (int, error) { allocations++; return 41 + allocations, nil }, func(port int) error {
				calls++
				if calls == 1 {
					return tc.err
				}
				if port != 43 {
					t.Fatalf("second attempt port=%d, want 43", port)
				}
				return nil
			})
			if err != nil || calls != 2 || allocations != 2 || port != 43 {
				t.Fatalf("allocations=%d calls=%d port=%d error=%v", allocations, calls, port, err)
			}
		})
	}
}

func TestAcquireStopsAfterRepeatedBindCollisions(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	calls := 0
	_, err = acquire(func() (int, error) { return busy.Addr().(*net.TCPAddr).Port, nil }, func(port int) error {
		calls++
		l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if l != nil {
			l.Close()
		}
		return err
	})
	if err == nil || calls != 3 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}

func TestSubprocessBindDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		text string
		want bool
	}{
		{"listen on 127.0.0.1:42: listen tcp 127.0.0.1:42: bind: address already in use", true},
		{"listen on 127.0.0.1:42: listen tcp 127.0.0.1:42: bind: Only one usage of each socket address (protocol/network address/port) is normally permitted.", true},
		{"listen tcp 127.0.0.1:42: bind: permission denied", false},
		{"hook says address already in use", false},
		{"listen tcp 127.0.0.1:42: invalid address", false},
		{"listen on 127.0.0.1:42: listen tcp 127.0.0.1:43: bind: address already in use", false},
		{"listen on 127.0.0.1:43: listen tcp 127.0.0.1:42: bind: address already in use", false},
		{"listen on 127.0.0.1:142: listen tcp 127.0.0.1:142: bind: address already in use", false},
		{"listen on 127.0.0.1:42: listen unix /fixture.sock: bind: address already in use", false},
		{"hook: listen on 127.0.0.1:42: listen tcp 127.0.0.1:42: bind: address already in use", false},
		{"listen on 127.0.0.1:42: listen tcp 127.0.0.1:42: bind: address already in use by another component", false},
		{"listen on 127.0.0.1:42: listen tcp 127.0.0.1:42: bind: address already in use\ninvalid settings", true},
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := bindCollision(errors.New(tc.text), 42); got != tc.want {
				t.Fatalf("collision=%v want %v", got, tc.want)
			}
		})
	}
}
