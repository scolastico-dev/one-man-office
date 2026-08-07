//go:build windows

package sockd

import (
	"net"

	winio "github.com/Microsoft/go-winio"
)

func listen(path string) (net.Listener, error) { return winio.ListenPipe(path, nil) }
