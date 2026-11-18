package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A WebSocket cannot remain useful after either TCP direction reaches EOF.
// Unlike a request/response stream, it has no half-close phase. Some servers
// retain the write side after a browser disconnects during their handshake.
func TestUpgradeDisconnectLifecycle(t *testing.T) {
	for _, protocol := range []string{"websocket", "other"} {
		for _, mode := range []Mode{PathMode, SubdomainMode} {
			for _, disconnect := range []string{"browser", "upstream"} {
				t.Run(protocol+"/"+fmt.Sprint(mode)+"/"+disconnect, func(t *testing.T) {
					release := make(chan struct{})
					defer close(release)
					upstreamConn := make(chan *net.TCPConn, 1)
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						conn, rw, err := w.(http.Hijacker).Hijack()
						if err != nil {
							return
						}
						defer conn.Close()
						fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", protocol)
						if rw.Flush() != nil {
							return
						}
						upstreamConn <- conn.(*net.TCPConn)
						<-release // Deliberately retain the other TCP direction, as a slow peer can.
					}))
					defer upstream.Close()
					transport := NewTransport()
					defer transport.CloseIdleConnections()
					handler := transport.Handler(forwardFor(t, upstream.URL), mode)
					finished := make(chan struct{})
					proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						defer close(finished)
						handler.ServeHTTP(w, r)
					}))
					defer proxy.Close()
					client, err := net.Dial("tcp", strings.TrimPrefix(proxy.URL, "http://"))
					if err != nil {
						t.Fatal(err)
					}
					defer client.Close()
					requestPath := "/proxy/abc/socket"
					if mode == SubdomainMode {
						requestPath = "/socket"
					}
					fmt.Fprintf(client, "GET %s HTTP/1.1\r\nHost: app.test\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", requestPath, protocol)
					client.SetReadDeadline(time.Now().Add(5 * time.Second))
					response, err := http.ReadResponse(bufio.NewReader(client), nil)
					if err != nil {
						t.Fatal(err)
					}
					if response.StatusCode != 101 {
						t.Fatalf("upgrade status %d", response.StatusCode)
					}
					peer := await(t, upstreamConn)
					// First prove bidirectional traffic before disconnecting either endpoint.
					if _, err := client.Write([]byte("browser")); err != nil {
						t.Fatal(err)
					}
					peer.SetReadDeadline(time.Now().Add(5 * time.Second))
					data := make([]byte, 7)
					if _, err := io.ReadFull(peer, data); err != nil || string(data) != "browser" {
						t.Fatalf("upstream read %q: %v", data, err)
					}
					if _, err := peer.Write([]byte("server")); err != nil {
						t.Fatal(err)
					}
					data = make([]byte, 6)
					if _, err := io.ReadFull(client, data); err != nil || string(data) != "server" {
						t.Fatalf("browser read %q: %v", data, err)
					}
					if disconnect == "browser" {
						if protocol == "websocket" {
							err = client.Close()
						} else {
							err = client.(*net.TCPConn).CloseWrite()
						}
					} else {
						err = peer.CloseWrite()
					}
					if err != nil {
						t.Fatal(err)
					}
					if protocol != "websocket" {
						reader, writer := net.Conn(peer), client
						if disconnect == "upstream" {
							reader, writer = client, peer
						}
						if n, err := reader.Read(make([]byte, 1)); n != 0 || err != io.EOF {
							t.Fatalf("half-close EOF: %d %v", n, err)
						}
						if _, err := reader.Write([]byte("tail")); err != nil {
							t.Fatal(err)
						}
						tail := make([]byte, 4)
						if _, err := io.ReadFull(writer, tail); err != nil || string(tail) != "tail" {
							t.Fatalf("half-close lost reverse traffic: %q %v", tail, err)
						}
						if disconnect == "browser" {
							err = peer.CloseWrite()
						} else {
							err = client.(*net.TCPConn).CloseWrite()
						}
						if err != nil {
							t.Fatal(err)
						}
					}
					select {
					case <-finished:
					case <-time.After(time.Second):
						t.Fatal("WebSocket proxy retained disconnected endpoint while waiting for the other TCP direction")
					}
				})
			}
		}
	}
}
