//go:build !windows

package transport

import (
	"context"
	"net"
)

// Dial connects to the server at endpoint.
func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", endpoint)
}
