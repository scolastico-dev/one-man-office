//go:build !windows

package transport

import (
	"errors"
	"net"
	"os"
)

// Listen listens on the Unix socket at endpoint, removing a stale socket file
// first.
func Listen(endpoint string) (net.Listener, error) {
	if err := os.Remove(endpoint); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return net.Listen("unix", endpoint)
}
