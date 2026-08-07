//go:build !windows

package sockd

import "net"

func listen(path string) (net.Listener, error) { return net.Listen("unix", path) }
