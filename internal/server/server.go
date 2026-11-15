// Package server serves the omo HTTP API. One http.Handler is served on two
// listeners: TCP for the browser (token or basic authentication, Host and
// Origin checks) and the local socket or named pipe for agents, whose
// identity comes from the X-Omo-Session header. Identity resolution is the
// only difference between the two; routes and rules are shared.
package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"maps"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// shutdownTimeout bounds how long Run waits for open requests on shutdown.
const shutdownTimeout = 10 * time.Second

// Options configure a server.
type Options struct {
	Layout  globalhome.Layout
	Config  serverconfig.Config
	Version string
	// PromptDefaults maps prompt names to their built-in text; it is handed
	// to the settings store.
	PromptDefaults map[string]string
}

// Server holds the opened database, settings and board and the routes on
// top of them.
type Server struct {
	// SocketEndpoint is the address the agent socket listens on; Run sets
	// it before the server is ready. Agents receive it as OMO_SOCKET.
	SocketEndpoint string

	opts     Options
	log      *log.Logger
	db       *sql.DB
	settings *settings.Store
	board    *board.Board
	events   *broker
	// machineNames are the machine's host names accepted in the Host header
	// when the server listens beyond loopback.
	machineNames []string
	token        string // login token; empty unless auth is token
	startedAt    time.Time
	mux          *http.ServeMux

	mu      sync.RWMutex
	address string // primary access address without token, set by Run

	// ctx ends when Close starts; background work runs under it.
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	bgMu      sync.Mutex // guards closing and wg.Add against wg.Wait in Close
	closing   bool       // set once Close has begun; background refuses new work
	closeOnce sync.Once
	closeErr  error
}

// New opens the database, the settings and the board of o.Layout and builds
// the routes, without opening any listener. Close releases everything.
func New(o Options) (*Server, error) {
	return newServer(o, os.Stderr)
}

func newServer(o Options, logOut io.Writer) (_ *Server, err error) {
	if logOut == nil {
		logOut = io.Discard
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		opts:         o,
		log:          log.New(logOut, "omo: ", log.LstdFlags),
		startedAt:    time.Now().UTC(),
		machineNames: machineHostNames(),
		ctx:          ctx,
		cancel:       cancel,
	}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	if o.Config.Auth == "token" {
		if s.token, err = serverconfig.LoadOrCreateToken(o.Layout.Token); err != nil {
			return nil, fmt.Errorf("load login token: %w", err)
		}
	}
	if s.db, err = db.Open(o.Layout.DB); err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	s.events = newBroker(s.db, s.log)
	s.settings, err = settings.Open(o.Layout.Root, settings.Options{
		PromptDefaults: maps.Clone(o.PromptDefaults),
		OnChange:       s.settingsChanged,
	})
	if err != nil {
		return nil, fmt.Errorf("open settings: %w", err)
	}
	if errs := s.settings.Errors(); len(errs) > 0 {
		for _, e := range errs {
			s.log.Printf("settings file %s ignored: %s", e.File, e.Message)
		}
	}
	s.board = board.New(s.db, boardEnv{st: s.settings, git: gitops.New()}, board.Options{Notify: s.boardChanged})
	s.mux = s.routes()
	s.background(s.settings.Run)
	s.background(s.runEvents)
	return s, nil
}

// background runs fn in a goroutine until Close; fn must return once ctx
// ends. Once Close has begun, fn is not run and background returns false.
func (s *Server) background(fn func(ctx context.Context)) bool {
	s.bgMu.Lock()
	defer s.bgMu.Unlock()
	if s.closing {
		return false
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		fn(s.ctx)
	}()
	return true
}

// Close stops the background work and closes the database. It is safe to
// call more than once.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.bgMu.Lock()
		s.closing = true
		s.bgMu.Unlock()
		s.cancel()
		s.wg.Wait()
		if s.db != nil {
			s.closeErr = s.db.Close()
		}
	})
	return s.closeErr
}

// Address returns the primary access address without the login token, or
// "" before Run listens.
func (s *Server) Address() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.address
}

// Run serves the API on the configured TCP address and on the agent socket
// until ctx ends. It prints the access lines and warnings to out, calls ready
// with the access URL once both listeners accept connections, and shuts down
// within 10 s after ctx ends. Every listener, the socket and the database are
// released when Run returns, also after a failed start.
//
// The caller must hold the home's service lock (see service.Run): Run itself
// does not guard against a second server on the same home and removes the
// leftovers of a crashed run, which must not belong to a live server.
func Run(ctx context.Context, o Options, out io.Writer, ready func(accessURL string) error) (err error) {
	if out == nil {
		out = io.Discard
	}
	if err := checkConfig(o.Config); err != nil {
		return err
	}
	s, err := newServer(o, out)
	if err != nil {
		return err
	}
	defer s.Close()

	tcp, err := net.Listen("tcp", o.Config.Listen())
	if err != nil {
		return fmt.Errorf("listen on %s: %w", o.Config.Listen(), err)
	}
	defer tcp.Close()
	sock, err := s.listenSocket()
	if err != nil {
		return err
	}
	defer sock.Close()

	primary, also := accessAddresses(tcp.Addr().(*net.TCPAddr))
	s.mu.Lock()
	s.address = primary
	s.mu.Unlock()
	fragment := ""
	if o.Config.Auth == "token" {
		fragment = "#" + s.token
	}
	fmt.Fprintf(out, "omo: %s%s\n", primary, fragment)
	if also != "" {
		fmt.Fprintf(out, "also: %s%s\n", also, fragment)
	}
	printWarnings(out, o.Config, tcp.Addr().(*net.TCPAddr))

	serveCtx, stopServing := context.WithCancel(s.ctx)
	defer stopServing()
	newHTTP := func(h http.Handler) *http.Server {
		return &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
			BaseContext:       func(net.Listener) context.Context { return serveCtx },
			ErrorLog:          s.log,
		}
	}
	browser, agents := newHTTP(s.BrowserHandler()), newHTTP(s.SocketHandler())
	served := make(chan error, 2)
	go func() { served <- browser.Serve(tcp) }()
	go func() { served <- agents.Serve(sock) }()
	defer func() {
		// Long-lived handlers (event stream) end with serveCtx; the rest
		// finish their request, so every acknowledged change is committed.
		stopServing()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		var wg sync.WaitGroup
		for _, h := range []*http.Server{browser, agents} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if h.Shutdown(shutdownCtx) != nil {
					h.Close()
				}
			}()
		}
		wg.Wait()
	}()

	if ready != nil {
		if err := ready(primary + fragment); err != nil {
			return err
		}
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-served:
		return fmt.Errorf("serve: %w", err)
	}
}

// checkConfig validates the start options. Port 0 picks a free port.
func checkConfig(c serverconfig.Config) error {
	if c.Port == 0 {
		c.Port = 1
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("invalid start options: %w", err)
	}
	return nil
}

// accessAddresses returns the printed address (omo.localhost when the
// server is reachable on loopback) and a second, IP-literal address.
func accessAddresses(addr *net.TCPAddr) (primary, also string) {
	port := strconv.Itoa(addr.Port)
	switch {
	case addr.IP.IsLoopback():
		return "http://" + net.JoinHostPort(hostOmoLocalhost, port) + "/", "http://" + net.JoinHostPort(addr.IP.String(), port) + "/"
	case addr.IP.IsUnspecified():
		return "http://" + net.JoinHostPort(hostOmoLocalhost, port) + "/", "http://" + net.JoinHostPort("127.0.0.1", port) + "/"
	default:
		return "http://" + net.JoinHostPort(addr.IP.String(), port) + "/", ""
	}
}

func printWarnings(out io.Writer, c serverconfig.Config, addr *net.TCPAddr) {
	if !addr.IP.IsLoopback() {
		fmt.Fprintln(out, "WARNING: omo is exposed beyond loopback over plain HTTP. Anyone with its access URL can run commands with your user permissions. Prefer loopback with an SSH port-forward using the same local and remote port.")
	}
	switch c.Auth {
	case "none":
		fmt.Fprintln(out, "WARNING: omo token authentication is disabled. Anyone who can reach this address can run commands with your user permissions.")
	case "basic":
		fmt.Fprintln(out, "WARNING: HTTP Basic authentication is intended only for networks you trust. It is not secure for exposing omo directly to the internet.")
	}
	if !c.OriginCheck {
		fmt.Fprintln(out, "WARNING: omo Origin verification is disabled. Use this only behind a trusted reverse proxy that enforces access control.")
	}
}
