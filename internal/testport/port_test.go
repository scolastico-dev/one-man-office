package testport

import (
	"errors"
	"fmt"
	"net"
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
	want := errors.New("invalid start options")
	calls := 0
	_, err := acquire(func() (int, error) { return 1234, nil }, func(int) error { calls++; return want })
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, err)
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
	} {
		t.Run(tc.text, func(t *testing.T) {
			if got := bindCollision(errors.New(tc.text)); got != tc.want {
				t.Fatalf("collision=%v want %v", got, tc.want)
			}
		})
	}
}
