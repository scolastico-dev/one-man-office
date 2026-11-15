//go:build windows

package transport

import (
	"context"
	"net"

	winio "github.com/Microsoft/go-winio"
)

// Dial connects to the server at endpoint.
func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
}
