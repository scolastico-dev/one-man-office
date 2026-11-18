package proxy

import (
	"bufio"
	"io"
	"net"
	"net/http"
)

// ReverseProxy supports half-closed upgraded streams by calling CloseWrite
// and waiting for the opposite direction's EOF. WebSockets have no TCP
// half-close phase: a disconnected peer must release the handler even if the
// other peer retains its write side. Expose only the full-close interfaces on
// both ends, so ReverseProxy closes both connections on the first EOF/error.
// Other upgrade protocols retain ReverseProxy's ordinary half-close behavior.
type websocketBody struct{ io.ReadWriteCloser }
type websocketConn struct{ net.Conn }
type websocketWriter struct{ http.ResponseWriter }

func (w websocketWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w websocketWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	return websocketConn{conn}, rw, nil
}
