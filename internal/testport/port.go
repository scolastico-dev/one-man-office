// Package testport acquires ports for fixtures that must start a real CLI
// with a persisted port, which cannot use an already-open listener.
package testport

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"syscall"
)

// Acquire calls start with an ephemeral port. start must fully stop any failed
// server before returning. Only a TCP bind collision on that port during fixture
// startup may allocate another port; every other error is returned unchanged.
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
		if err == nil || attempt == 2 || !bindCollision(err, port) {
			return port, err
		}
	}
}

// Subprocesses lose the typed error. Accept only the complete server.Run
// diagnostic, optionally prefixed by main/service.Run, on its own output line.
var bindDiagnostic = regexp.MustCompile(`(?mi)^(?:omo: )?listen on (\S+): listen tcp[46]? (\S+): bind: (?:address already in use|only one usage of each socket address \(protocol/network address/port\) is normally permitted\.)\r?$`)

// Winsock reports WSAEADDRINUSE, distinct from syscall.EADDRINUSE on Windows.
const wsaAddressInUse = syscall.Errno(10048)

func bindCollision(err error, port int) bool {
	var op *net.OpError
	if errors.As(err, &op) {
		// Typed information is authoritative: an unrelated listener must not
		// become retryable because its wrapper also contains a bind diagnostic.
		addr, ok := op.Addr.(*net.TCPAddr)
		return op.Op == "listen" && (op.Net == "tcp" || op.Net == "tcp4" || op.Net == "tcp6") &&
			ok && addr != nil && addr.Port == port &&
			(errors.Is(op.Err, syscall.EADDRINUSE) || errors.Is(op.Err, wsaAddressInUse))
	}
	for _, match := range bindDiagnostic.FindAllStringSubmatch(err.Error(), -1) {
		_, requested, err := net.SplitHostPort(match[1])
		if err != nil || requested != strconv.Itoa(port) {
			continue
		}
		_, bound, err := net.SplitHostPort(match[2])
		if err == nil && bound == strconv.Itoa(port) {
			return true
		}
	}
	return false
}
