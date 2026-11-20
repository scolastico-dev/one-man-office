package relaytransport

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/relayproto"
)

// ServeHTTP serves real HTTP/1.1 connections inside an authenticated session.
// It owns records until every request (including hijacked handlers) has exited.
// connContext attaches server-created authority, never endpoint-supplied data.
func ServeHTTP(ctx context.Context, records relayproto.RecordIO, authorize func() error, handler http.Handler, connContext func(context.Context, net.Conn) context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := New(ctx, records, authorize)
	var mu sync.Mutex
	stopping := false
	var handlers sync.WaitGroup
	srv := &http.Server{
		BaseContext:    func(net.Listener) context.Context { return ctx },
		ConnContext:    connContext,
		ErrorLog:       log.New(io.Discard, "", 0),
		MaxHeaderBytes: 64 << 10,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			if stopping {
				mu.Unlock()
				return
			}
			handlers.Add(1)
			mu.Unlock()
			defer handlers.Done()
			if r.ProtoMajor != 1 || r.ProtoMinor != 1 {
				http.Error(w, "HTTP/1.1 required", http.StatusHTTPVersionNotSupported)
				return
			}
			handler.ServeHTTP(w, r)
		}),
	}
	srv.SetKeepAlivesEnabled(false)
	_ = srv.Serve(m)
	mu.Lock()
	stopping = true
	mu.Unlock()
	cancel()
	m.Close()
	srv.Close()
	handlers.Wait()
	return m.Wait()
}
