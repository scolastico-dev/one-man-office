package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/coder/websocket"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/proxy"
)

// Each outer socket is one HTTP/1.1 connection. Binary message boundaries are
// arbitrary; bounded messages provide backpressure without buffering streams.
const forwardTunnelMessageLimit = 64 * 1024

func (s *Server) forwardTunnel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.board.LiveProxyForward(r.Context(), board.System, id); err != nil {
		writeError(w, r, err)
		return
	}
	offered := false
	for _, p := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if strings.TrimSpace(p) == api.FeatureUIForwardTunnel {
			offered = true
		}
	}
	if !offered {
		writeError(w, r, api.Errorf(api.CodeBadRequest, "ui.forwards.tunnel.v1 subprotocol is required"))
		return
	}
	// BrowserHandler has already applied the normal outer Host/Origin checks.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{api.FeatureUIForwardTunnel}, InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stopServer := context.AfterFunc(s.ctx, cancel)
	defer stopServer()
	// net/http interrupts its background reads with deadlines. NetConn closes
	// the WebSocket on a deadline, so serve HTTP on a pipe with ordinary deadline
	// semantics and pump the binary stream separately.
	appConn, bridge := net.Pipe()
	defer appConn.Close()
	defer bridge.Close()
	stopSocket := context.AfterFunc(ctx, func() { ws.CloseNow(); appConn.Close(); bridge.Close() })
	defer stopSocket()
	conn := &forwardTunnelConn{Conn: websocket.NetConn(ctx, ws, websocket.MessageBinary), cancel: cancel}
	// NetConn disables the library's default read limit; restore a protocol bound.
	ws.SetReadLimit(forwardTunnelMessageLimit)
	defer conn.Close()
	var pumps sync.WaitGroup
	pumps.Add(2)
	// An inner HTTP close may occur as soon as its last write reaches the
	// pipe reader, before that reader has written the bytes to the WebSocket.
	// Close the pipe on inbound EOF; only the outbound pump cancels the socket,
	// after its current write and all final output have drained.
	go func() { defer pumps.Done(); defer bridge.Close(); _, _ = io.Copy(bridge, conn) }()
	go func() {
		defer pumps.Done()
		defer cancel()
		if _, err := io.Copy(conn, bridge); err == nil {
			// A completed HTTP connection is a clean byte-stream EOF, including
			// close-delimited responses. Send the close frame before cancellation.
			_ = ws.Close(websocket.StatusNormalClosure, "")
		}
	}()
	listener := &forwardTunnelListener{conn: appConn, ctx: ctx, cancel: cancel}
	inner := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, app *http.Request) {
			// Never let request targets select a destination or act as a CONNECT proxy.
			if app.Method == http.MethodConnect || !strings.HasPrefix(app.RequestURI, "/") || app.URL.IsAbs() || app.URL.Host != "" {
				http.Error(w, "tunnel requires an origin-form HTTP request", http.StatusBadRequest)
				return
			}
			f, err := s.board.LiveProxyForward(app.Context(), board.System, id)
			if err != nil {
				http.Error(w, "This Web UI forward no longer exists.", http.StatusNotFound)
				return
			}
			// Use the outer accepted address for activity/client-IP metadata. Inner Host
			// and Origin describe the virtual/local app address, never the board origin.
			app.RemoteAddr = r.RemoteAddr
			s.serveProxyForwardWithBoardURL(w, app, f, proxy.SubdomainMode, s.proxyBoardURL(r))
		}),
		BaseContext: func(net.Listener) context.Context { return ctx },
		ErrorLog:    s.log,
	}
	defer inner.Close()
	_ = inner.Serve(listener)
	cancel()
	_ = inner.Close()
	pumps.Wait()
}

// Serve needs a listener, but this tunnel has exactly one in-memory connection
// and opens no TCP port. Closing either side cancels Accept and all requests.
type forwardTunnelListener struct {
	conn     net.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	accepted bool
}

func (l *forwardTunnelListener) Accept() (net.Conn, error) {
	if !l.accepted {
		l.accepted = true
		return l.conn, nil
	}
	<-l.ctx.Done()
	return nil, net.ErrClosed
}
func (l *forwardTunnelListener) Close() error   { l.cancel(); return nil }
func (l *forwardTunnelListener) Addr() net.Addr { return l.conn.LocalAddr() }

type forwardTunnelConn struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *forwardTunnelConn) Close() error { c.cancel(); return c.Conn.Close() }
func (c *forwardTunnelConn) Write(p []byte) (int, error) {
	total := 0
	for len(p) > 0 {
		n, err := c.Conn.Write(p[:min(len(p), forwardTunnelMessageLimit)])
		total += n
		p = p[n:]
		if err != nil {
			c.cancel()
			return total, err
		}
	}
	return total, nil
}
