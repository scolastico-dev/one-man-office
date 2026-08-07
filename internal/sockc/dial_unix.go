//go:build !windows

package sockc

import "net"

func dial(path string) (net.Conn, error) { return net.Dial("unix", path) }
