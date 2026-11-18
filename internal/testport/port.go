// Package testport acquires ports for fixtures that must start a real CLI
// with a persisted port, which cannot use an already-open listener.
package testport

import (
	"errors"
	"net"
	"regexp"
	"syscall"
)

// Acquire calls start with an ephemeral port. start must fully stop any failed
// server before returning. Only a bind collision during fixture startup may
// allocate another port; every other error is returned unchanged.
func Acquire(start func(int) error) (int, error) { return acquire(candidate, start) }

func candidate() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	return port, l.Close()
}

func acquire(next func() (int, error), start func(int) error) (int, error) {
	for attempt := 0; ; attempt++ {
		port, err := next()
		if err != nil {
			return 0, err
		}
		err = start(port)
		if err == nil || attempt == 2 || !bindCollision(err) {
			return port, err
		}
	}
}

var bindDiagnostic = regexp.MustCompile(`(?i)listen tcp[46]? [^\r\n]+: bind: (address already in use|only one usage of each socket address)`)

func bindCollision(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "listen" && errors.Is(op.Err, syscall.EADDRINUSE) {
		return true
	}
	return bindDiagnostic.MatchString(err.Error())
}
