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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agents"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/engine"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homezip"
	"github.com/scolastico-dev/one-man-office/internal/images"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/serverconfig"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/usage"
	"github.com/scolastico-dev/one-man-office/internal/userterm"
	"github.com/scolastico-dev/one-man-office/internal/webterm"
)

// shutdownTimeout bounds how long Run waits for open requests on shutdown.
const shutdownTimeout = 10 * time.Second

// terminalConnections bounds the open agent terminal connections.
const terminalConnections = 16

// Options configure a server.
type Options struct {
	Layout  globalhome.Layout
	Config  serverconfig.Config
	Version string
	// PromptDefaults maps prompt names to their built-in text; it is handed
	// to the settings store. nil means omo's embedded default prompts.
	PromptDefaults map[string]string
	// Runner starts agent processes. With a runner the server runs the
	// engine, which starts and ends agents for the board; without one (nil)
	// no agent ever starts and the agent routes answer 503 unavailable.
	Runner agents.Runner
	// Hurry, when closed, cuts a graceful stop short: Run stops waiting for
	// the agents' handoff comments and ends them at once. It is meant for a
	// second stop request (a second Ctrl-C).
	Hurry <-chan struct{}
	// DetectAgents looks a program up on PATH (exec.LookPath). When it is
	// set, a first start (no settings.yaml and no profile before the
	// settings were opened) writes a profile for every agent program it finds and assigns
	// models to roles that have none (setup.go); the "detect" route uses it
	// too. nil: no detection at a first start, and the route looks at the
	// real PATH.
	DetectAgents func(string) (string, error)
	// PluginCatalog is where the plugin list of the Plugins page is read
	// from. The zero value is no list (tests); Run fills it from
	// OMO_PLUGIN_CATALOG, else the release branch of omo's repository.
	PluginCatalog pluginmanager.CatalogLocation

	// scan replaces how the project scanner reads folders and times its
	// rounds; tests only.
	scan *scanSeams
}

// gitVersion reports the installed git version; tests replace it.
var gitVersion = func() (major, minor int, err error) { return gitops.New().Version() }

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
	// git answers the board's and the project list's questions about
	// repositories.
	git *gitops.Git
	// branchSem bounds the projects read from git at once, server-wide;
	// readBranches is what reads one project (tests replace it).
	// beforeHandIn, when set, runs once the request of a hand-in is read
	// and before it is checked (tests close the client's side there).
	beforeHandIn func()
	branchSem    chan struct{}
	// diffSem bounds the diffs read from git at once, server-wide; diffHook,
	// when set, runs in the slot before git does (tests).
	diffSem      chan struct{}
	diffHook     func(ctx context.Context)
	readBranches func(ctx context.Context, path string) (current, def string)
	events       *broker
	// engine runs the agents; nil without Options.Runner. engineStarted is
	// set once StartEngine handed its Run to background; engineReady is
	// closed after the engine's first pass, when what an earlier run left
	// behind has been cleaned up. terminals bounds the open agent terminal
	// connections.
	engine        *engine.Engine
	engineMu      sync.Mutex
	engineStarted bool
	engineReady   chan struct{}
	readyOnce     sync.Once
	terminals     *webterm.ConnectionLimit
	// shells are the user's own shells (terminals.go).
	shells *userterm.Manager
	// feed carries settings changes to the event log (settings.go).
	feed settingsFeed
	// scan searches the workspace roots for projects (discovery.go).
	scan *scanner
	// refs keeps ticket writes and project or model removals apart
	// (refs.go).
	refs refsLock
	// setup is the state of the agent setup routes (setup.go).
	setup setupState
	// usage, skills and plugins are the extensions (usage.go, skillsets.go,
	// plugins.go).
	usage   usageFeature
	skills  skillSetFeature
	plugins pluginFeature
	mcpAuth mcpAuthFeature // tokens and OAuth server of /mcp (mcp_auth.go)
	mcpConf mcpSettingsFeature
	// imageStore is the images folder; imageStray the number of files in it
	// without a record the last sweep counted (images.go).
	imageStore *images.Store
	// terminalImages holds the images dropped or pasted into terminals.
	terminalImages *images.Store
	imageStray     atomic.Int64
	// routeTable lists every route registered through registerRoutes with
	// who may call it (access.go).
	routeTable []apiRoute
	// ceoReassignFailed is set when handing the CEO's work to the user
	// failed; the next settings change tries again.
	ceoReassignFailed atomic.Bool
	token             string // login token; empty unless auth is token
	startedAt         time.Time
	mux               *http.ServeMux
	mcp               mcpEndpoint // omo's MCP endpoint (mcp.go)

	mu      sync.RWMutex
	address string // primary access address without token, set by Run
	listen  string // host:port of the TCP listener, set by Run

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
		opts:      o,
		log:       log.New(logOut, "omo: ", log.LstdFlags),
		startedAt: time.Now().UTC(),
		ctx:       ctx,
		cancel:    cancel,
	}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	if o.Runner != nil {
		// Merges need git merge-tree --write-tree; refuse to start the
		// engine on an older git before anything is opened.
		major, minor, err := gitVersion()
		if err != nil {
			return nil, fmt.Errorf("check the git version: %w", err)
		}
		if err := gitops.CheckVersion(major, minor); err != nil {
			return nil, err
		}
	}
	if o.PromptDefaults == nil {
		o.PromptDefaults = prompts.Defaults()
		s.opts.PromptDefaults = o.PromptDefaults
	}
	if o.Config.Auth == "token" {
		if s.token, err = serverconfig.LoadOrCreateToken(o.Layout.Token); err != nil {
			return nil, fmt.Errorf("load login token: %w", err)
		}
	}
	// A database an import placed next to omo.db replaces it now, before
	// anything opens it.
	if moved, err := homezip.SwapPendingDatabase(o.Layout, time.Now()); err != nil {
		return nil, fmt.Errorf("put the imported database in place: %w", err)
	} else if moved != "" {
		s.log.Printf("the imported database replaced the old one, which is kept in %s", moved)
	}
	if s.db, err = db.Open(o.Layout.DB); err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	s.events = newBroker(s.db, s.log)
	s.imageStore = images.NewStore(o.Layout.Images)
	s.terminalImages = images.NewStore(filepath.Join(o.Layout.Images, images.TerminalFolder))
	s.scan = newScanner(s, o.scan)
	firstStart := isFirstStart(o.Layout.Root)
	s.settings, err = settings.Open(o.Layout.Root, settings.Options{
		PromptDefaults:     maps.Clone(o.PromptDefaults),
		OnChange:           s.settingsChanged,
		ReservedProjectIDs: s.reservedProjectIDs,
		ValidatePrompt:     prompts.Check,
		// Non-nil: a profile may only name a usage script that exists.
		UsageDefaults:       usage.Defaults(),
		ValidateUsageScript: usage.Check,
	})
	if err != nil {
		return nil, fmt.Errorf("open settings: %w", err)
	}
	if errs := s.settings.Errors(); len(errs) > 0 {
		for _, e := range errs {
			s.log.Printf("settings file %s ignored: %s", e.File, e.Message)
		}
	}
	s.git = gitops.New()
	// git never looks for a repository in the agents' worktrees: commands
	// there run only through worktrees resolved from the project's records.
	s.git.SetAgentFolders(o.Layout.Worktrees, o.Layout.Workdirs)
	s.branchSem = make(chan struct{}, branchWorkers)
	s.diffSem = make(chan struct{}, diffSlots)
	s.readBranches = s.branches
	s.board = board.New(s.db, boardEnv{st: s.settings, git: s.git, refs: &s.refs}, board.Options{Notify: s.boardChanged, RefsLock: &s.refs})
	s.engineReady = make(chan struct{})
	s.terminals = webterm.NewConnectionLimit(terminalConnections)
	s.startUsage(ctx)
	s.startSkillSets(ctx)
	s.startPlugins(ctx)
	if o.Runner != nil {
		// The engine gets a git client of its own: it carries the agents'
		// environment, and its long operations do not hold up the board's
		// and the project list's queries.
		s.engine = engine.New(engine.Options{
			Board: s.board, Settings: s.settings, Git: gitops.New(), Layout: o.Layout, Runner: o.Runner,
			Socket: s.socketEndpoint, Emit: s.emitEngineEvent, Log: s.log,
			Usage: s.usageView(),
		})
	}
	s.shells = s.newShells()
	s.reportStartupSettings()
	if firstStart && o.DetectAgents != nil {
		s.detectAtFirstStart()
	}
	s.mux = s.routes()
	s.background(s.settings.Run)
	s.background(s.runEvents)
	// The board exists: scan the workspace roots once now, then on the
	// timer, when the roots change and on request.
	s.scan.request(scanStart)
	s.background(s.scan.run)
	s.background(s.runImageSweep)
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
		// Agents are killed at once, without a handoff: a context that has
		// already ended skips it.
		ended, end := context.WithCancel(context.Background())
		end()
		if s.shells != nil {
			s.shells.CloseAll(shellCloseGrace)
		}
		s.Shutdown(ended)
		s.cancel()
		s.wg.Wait()
		s.stopPlugins()
		s.stopSkillSets()
		s.stopUsage()
		if s.db != nil {
			s.closeErr = s.db.Close()
		}
	})
	return s.closeErr
}

// Address returns the address omo advertises, without the login token: the
// public_url start option when set, else the primary address it listens on
// ("" before Run listens). Everything that links to the board takes it from
// here.
func (s *Server) Address() string {
	if base := s.opts.Config.PublicBase(); base != "" {
		return base
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.address
}

// listenAddress returns the host and port omo listens on: the bound address
// once Run listens, else the configured one.
func (s *Server) listenAddress() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.listen != "" {
		return s.listen
	}
	return s.opts.Config.Listen()
}

// Run serves the API on the configured TCP address and on the agent socket
// until ctx ends. It prints the access lines and warnings to out, calls ready
// with the access URL once both listeners accept connections, and then runs
// the engine (with Options.Runner). Every listener, the socket, the agents
// and the database are released when Run returns, also after a failed
// start; nothing of the engine runs before ready succeeded.
//
// When ctx ends Run stops gracefully, in this order: the engine asks every
// working agent for a handoff comment and waits for the answers, at most
// stop.handoff_timeout of the general settings (Options.Hurry cuts the wait
// short), then kills every agent, while both listeners still serve, since
// agents write their handoff through the socket; then the listeners stop,
// waiting up to 10 s for open requests (long requests such as the event
// stream, omo ai wait and terminals end at once); then everything else is
// closed.
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
	if o.PluginCatalog == (pluginmanager.CatalogLocation{}) {
		if o.PluginCatalog, err = pluginmanager.CatalogFromEnv(os.Getenv(pluginmanager.CatalogEnv)); err != nil {
			return err
		}
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
	sock, err := s.ListenSocket()
	if err != nil {
		return err
	}
	defer sock.Close()

	listening, also := accessAddresses(tcp.Addr().(*net.TCPAddr))
	s.mu.Lock()
	s.address = listening
	s.listen = tcp.Addr().String()
	s.mu.Unlock()
	primary := s.Address()
	fragment := ""
	if o.Config.Auth == "token" {
		fragment = "#" + s.token
	}
	fmt.Fprintf(out, "omo: %s%s\n", primary, fragment)
	// With a public_url the addresses omo listens on follow as "also:".
	for _, a := range []string{listening, also} {
		if a != "" && a != primary {
			fmt.Fprintf(out, "also: %s%s\n", a, fragment)
		}
	}
	name, _ := hostName()
	printHostNameHint(out, o.Config, tcp.Addr().(*net.TCPAddr), name)
	printWarnings(out, o.Config, tcp.Addr().(*net.TCPAddr))

	serveCtx, stopServing := context.WithCancel(s.ctx)
	defer stopServing()
	// No ReadTimeout or WriteTimeout: the event stream, omo ai wait (up to
	// 24 hours) and agent terminals are long requests. The event stream
	// bounds each of its writes itself; every long request ends with
	// serveCtx.
	newHTTP := func(h http.Handler) *http.Server {
		return &http.Server{
			Handler:           h,
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       60 * time.Second,
			BaseContext:       func(net.Listener) context.Context { return serveCtx },
			ErrorLog:          s.log,
		}
	}
	browser, local := newHTTP(s.BrowserHandler()), newHTTP(s.SocketHandler())
	served := make(chan error, 2)
	go func() { served <- browser.Serve(tcp) }()
	go func() { served <- local.Serve(sock) }()
	defer func() {
		// Long-lived handlers (event stream, waits, terminals) end with
		// serveCtx; the rest finish their request, so every acknowledged
		// change is committed. Websocket connections are hijacked, so
		// Shutdown does not wait for them.
		stopServing()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		var wg sync.WaitGroup
		for _, h := range []*http.Server{browser, local} {
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
	if ctx.Err() == nil {
		s.StartEngine()
	}
	select {
	case <-ctx.Done():
	case err = <-served:
		err = fmt.Errorf("serve: %w", err)
	}
	s.handOff(o.Hurry)
	return err
}

// handOff stops the engine gracefully while the listeners still serve: it
// waits for the agents' handoff comments at most stop.handoff_timeout, less
// when hurry is closed.
func (s *Server) handOff(hurry <-chan struct{}) {
	if !s.engineRunning() {
		return
	}
	limit := time.Duration(s.settings.General().Stop.HandoffTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	stop := make(chan struct{})
	defer close(stop)
	if hurry != nil {
		go func() {
			select {
			case <-hurry:
				cancel()
			case <-stop:
			}
		}()
	}
	s.Shutdown(ctx)
}

// ListenSocket listens on a fresh agent socket endpoint of the run folder
// and records it in SocketEndpoint; agents get it as OMO_SOCKET. Closing the
// listener also removes the endpoint's files. Run calls it; a caller that
// serves the socket itself (SocketHandler) calls it before StartEngine.
func (s *Server) ListenSocket() (net.Listener, error) {
	return s.listenSocket()
}

// socketEndpoint returns the agent socket's endpoint, or "" before
// ListenSocket.
func (s *Server) socketEndpoint() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.SocketEndpoint
}

// StartEngine runs the engine in the background until Close; it does
// nothing without Options.Runner, after Close has begun, or when the engine
// already runs. Run calls it once the server is ready; a caller that serves
// the handlers itself calls it after ListenSocket.
func (s *Server) StartEngine() {
	if s.engine == nil {
		return
	}
	s.engineMu.Lock()
	defer s.engineMu.Unlock()
	if s.engineStarted {
		return
	}
	s.engineStarted = s.background(s.engine.Run)
}

// engineRunning reports whether StartEngine started the engine.
func (s *Server) engineRunning() bool {
	if s.engine == nil {
		return false
	}
	s.engineMu.Lock()
	defer s.engineMu.Unlock()
	return s.engineStarted
}

// Shutdown stops the engine gracefully: no agent starts any more, every
// working agent is asked for a handoff comment, and Shutdown waits for the
// answers until ctx ends (an ended ctx skips the handoff), then kills every
// agent and ends every session. It returns once no agent runs. A second call
// cuts the first one's wait short. It does nothing when the engine does not
// run. The listeners must still serve while it waits: agents write their
// handoff through the socket.
func (s *Server) Shutdown(ctx context.Context) {
	if s.engineRunning() {
		s.engine.Shutdown(ctx)
	}
}

// emitEngineEvent records an event of the engine (scheduler.changed and the
// like) and wakes the event stream. The first scheduler.changed marks the
// end of the engine's start.
func (s *Server) emitEngineEvent(kind string, data any) error {
	err := s.appendSystemEvent(kind, data)
	s.events.Wake()
	if kind == api.KindSchedulerChanged {
		s.readyOnce.Do(func() { close(s.engineReady) })
	}
	return err
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

// hostName returns the machine's host name; tests replace it.
var hostName = os.Hostname

// printHostNameHint tells the user how to open omo by the machine's name when
// the server listens beyond loopback: the name is refused until it is listed
// in allowed_hosts. It prints nothing in loopback mode, for an empty or
// unusable name, or when the name is already allowed.
func printHostNameHint(out io.Writer, c serverconfig.Config, addr *net.TCPAddr, name string) {
	if addr.IP.IsLoopback() {
		return
	}
	name = strings.TrimSpace(name)
	port := strconv.Itoa(addr.Port)
	if p, ok := c.Public(); ok && strings.EqualFold(p.Host, name) {
		return
	}
	if name == "" || net.ParseIP(name) != nil || serverconfig.ValidateAllowedHost(name) != nil {
		return
	}
	for _, entry := range c.AllowedHosts {
		if n, p, ok := serverconfig.SplitAllowedHost(entry); ok && strings.EqualFold(n, name) && (p == "" || p == port) {
			return
		}
	}
	list := append(append([]string{}, c.AllowedHosts...), name)
	fmt.Fprintf(out, "To open omo as http://%s, run: omo config set allowed_hosts %s\n", net.JoinHostPort(name, port), serverconfig.FormatAllowedHosts(list))
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
	if !uiBuilt() {
		fmt.Fprintln(out, "WARNING: the board is not built into this binary; the browser shows a notice page instead.")
	}
	if !c.OriginCheck {
		fmt.Fprintln(out, "WARNING: omo Origin verification is disabled. Use this only behind a trusted reverse proxy that enforces access control.")
	}
}
