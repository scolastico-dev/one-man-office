//go:build !windows

package sockc

import (
	"net"
	"time"
)

func dial(path string) (net.Conn, error) { return net.Dial("unix", path) }

func dialTimeout(path string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", path, timeout)
}
