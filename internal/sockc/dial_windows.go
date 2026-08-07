//go:build windows

package sockc

import (
	"net"

	winio "github.com/Microsoft/go-winio"
)

func dial(path string) (net.Conn, error) { return winio.DialPipe(path, nil) }
