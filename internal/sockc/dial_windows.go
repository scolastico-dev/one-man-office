//go:build windows

package sockc

import (
	"context"
	"net"
	"time"

	winio "github.com/Microsoft/go-winio"
)

func dial(path string) (net.Conn, error) { return winio.DialPipe(path, nil) }

func dialTimeout(path string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return winio.DialPipeContext(ctx, path)
}
