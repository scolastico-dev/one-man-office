// Package mcprelay runs the upstream MCP servers omo relays: it starts
// stdio servers as processes (one per agent session in that session's
// folder, or one shared process) and keeps one connection per HTTP or SSE
// server, signs in at upstream servers (static headers, a header command or
// OAuth), offers their tools under omo's names and forwards calls with
// timeouts, cancellation and progress. Credentials of the clients that
// reach omo are never forwarded: every upstream request carries only what
// omo holds for that server.
package mcprelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcpauth"
	"github.com/scolastico-dev/one-man-office/internal/mcpserver"
)

// Options are what the pool needs from the server.
type Options struct {
	// Servers returns the configured servers.
	Servers func() []api.MCPServer
	// Auth keeps the OAuth sign-ins; nil keeps them in memory.
	Auth mcpauth.UpstreamStore
	// Redirect returns the address of omo's OAuth callback for a sign-in
	// started with ctx (Connect's): the callback at the address the user's
	// browser reached omo by.
	Redirect func(ctx context.Context) string
	// LogDir is the folder of the servers' logs (mcp-<name>.log).
	LogDir string
	// Env returns the environment programs get (childenv.Inherited()).
	Env func() []string
	// Home is the working folder of processes that belong to no agent
	// session: shared servers and the shared process of a per-session
	// server for connections without a session. "" is the OS user's home.
	Home string
	// Emit publishes an event (api.KindMCPChanged, api.KindMCPToolsChanged).
	Emit func(kind string, data any)
	Log  *log.Logger
	// Version is omo's version, sent to the servers; "" is "dev".
	Version string
}

// Defaults and limits.
const (
	DefaultStartupTimeout = 30 * time.Second
	DefaultCallTimeout    = 10 * time.Minute
	MaxCallTimeout        = 24 * time.Hour
	// MaxProcesses bounds the running stdio processes of all servers.
	MaxProcesses = 64
	// MaxStarting bounds the instances that start at once.
	MaxStarting = 8
	// IdleSession stops a per-session instance after this long without a
	// call; IdleShared a shared one.
	IdleSession = 15 * time.Minute
	IdleShared  = 10 * time.Minute
	// MaxFailures within FailureWindow mark a server failed until its
	// settings change or it is restarted.
	MaxFailures   = 5
	FailureWindow = 10 * time.Minute
	// MaxTools is the number of tools omo offers of one server.
	MaxTools = 512
	// MaxMessage bounds one message read from an upstream; MaxResult one
	// tool result omo passes on.
	MaxMessage = 16 << 20
	MaxResult  = 4 << 20
)

// ErrUnknownTool is returned by Call for a name that is not a tool the
// identity may use; the endpoint answers it as JSON-RPC "unknown tool".
var ErrUnknownTool = errors.New("unknown tool")

// tunables are the limits and durations, shortened by tests.
type tunables struct {
	maxProcesses  int
	maxStarting   int
	idleSession   time.Duration
	idleShared    time.Duration
	backoff       []time.Duration
	maxFailures   int
	failureWindow time.Duration
	maxTools      int
	maxMessage    int
	maxResult     int
	janitor       time.Duration
	connectWait   time.Duration
	loginWait     time.Duration
	stopWait      time.Duration
}

func defaultTunables() tunables {
	return tunables{
		maxProcesses:  MaxProcesses,
		maxStarting:   MaxStarting,
		idleSession:   IdleSession,
		idleShared:    IdleShared,
		backoff:       []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second},
		maxFailures:   MaxFailures,
		failureWindow: FailureWindow,
		maxTools:      MaxTools,
		maxMessage:    MaxMessage,
		maxResult:     MaxResult,
		janitor:       30 * time.Second,
		connectWait:   30 * time.Second,
		loginWait:     10 * time.Minute,
		stopWait:      2 * time.Second,
	}
}

// Pool runs the relayed servers. It implements mcpserver.Relay.
type Pool struct {
	o     Options
	t     tunables
	store mcpauth.UpstreamStore
	// base sends the requests to HTTP servers and authorization servers.
	base http.RoundTripper

	mu        sync.Mutex
	instances map[instanceKey]*instance
	servers   map[string]*serverState
	closed    bool

	gen      atomic.Uint64
	starting chan struct{}
	stop     chan struct{}
	done     chan struct{}

	auth authFlows
}

var _ mcpserver.Relay = (*Pool)(nil)

// instanceKey names an instance: the server and, for a per-session
// instance, the agent session ("" is the server's shared instance).
type instanceKey struct {
	server string
	key    string
}

// serverState is what the pool keeps about a server across its instances.
type serverState struct {
	redact   *redactor
	log      *serverLog
	failures []time.Time
	// failed is set after too many failures; only a restart or a settings
	// change clears it.
	failed    bool
	lastError string
	// notBefore delays the next start after a failure.
	notBefore time.Time
	// tools, omitted and the offers are the last listing of any instance.
	tools           []string
	omitted         []api.MCPOmittedTool
	offersResources bool
	offersPrompts   bool
	// auth is the sign-in state of an OAuth server; loaded marks it read
	// from the store.
	auth       api.MCPAuthState
	authLoaded bool
	// headers caches the output of the header command until a 401.
	headers map[string]string
}

// New returns a pool for o and starts its idle janitor; Close stops it.
func New(o Options) *Pool {
	return newPool(o, defaultTunables())
}

func newPool(o Options, t tunables) *Pool {
	if o.Servers == nil {
		o.Servers = func() []api.MCPServer { return nil }
	}
	if o.Env == nil {
		o.Env = func() []string { return nil }
	}
	if o.Redirect == nil {
		o.Redirect = func(context.Context) string { return "" }
	}
	if o.Version == "" {
		o.Version = "dev"
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	p := &Pool{
		o:         o,
		t:         t,
		store:     o.Auth,
		base:      http.DefaultTransport,
		instances: map[instanceKey]*instance{},
		servers:   map[string]*serverState{},
		starting:  make(chan struct{}, t.maxStarting),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	if p.store == nil {
		p.store = NewMemoryStore()
	}
	p.auth.init()
	go p.janitor()
	return p
}

// Generation is raised on every change of a relayed tool list.
func (p *Pool) Generation() uint64 { return p.gen.Load() }

// toolsChanged raises the generation and announces it.
func (p *Pool) toolsChanged() {
	p.gen.Add(1)
	p.emit(api.KindMCPToolsChanged, nil)
}

func (p *Pool) emit(kind string, data any) {
	if p.o.Emit != nil {
		p.o.Emit(kind, data)
	}
}

// changed announces the state of server.
func (p *Pool) changed(server string) {
	if p.o.Emit == nil {
		return
	}
	for _, st := range p.Status() {
		if st.Name == server {
			p.emit(api.KindMCPChanged, st)
			return
		}
	}
}

// state returns the state of server, created on first use. Call with p.mu
// held.
func (p *Pool) state(server string) *serverState {
	st := p.servers[server]
	if st == nil {
		st = &serverState{redact: &redactor{}}
		st.log = newServerLog(p.o.LogDir, server, st.redact)
		p.servers[server] = st
	}
	return st
}

// config returns the configured server name.
func (p *Pool) config(name string) (api.MCPServer, bool) {
	for _, s := range p.o.Servers() {
		if s.Name == name {
			return s, true
		}
	}
	return api.MCPServer{}, false
}

// roleOf is the role name a server's roles list uses for the actor of id.
func roleOf(id mcpserver.Identity) string {
	if id.Actor.Purpose == api.PurposeExport {
		return "export"
	}
	return string(id.Actor.Role)
}

// visible reports whether id may use the tools of s.
func visible(s api.MCPServer, id mcpserver.Identity) bool {
	if !s.Enabled {
		return false
	}
	switch id.Class {
	case mcpserver.ClassAgent:
		return len(s.Roles) == 0 || slices.Contains(s.Roles, roleOf(id))
	case mcpserver.ClassUser:
		return s.Global
	}
	return false
}

// placement returns the instance key and folder of s for id: a per-session
// stdio server runs one process per agent session in its folder, and one
// shared process in the home folder for every connection without a
// session; everything else is shared.
func (p *Pool) placement(s api.MCPServer, id mcpserver.Identity) (key, dir string) {
	dir = p.o.Home
	if s.Transport == api.MCPTransportStdio && s.Instances != api.MCPInstancesShared &&
		id.Class == mcpserver.ClassAgent && id.Session != "" {
		key = id.Session
		if id.Workdir != "" {
			dir = id.Workdir
		}
	}
	if s.Cwd != "" {
		dir = s.Cwd
	}
	return key, dir
}

func startupTimeout(s api.MCPServer) time.Duration {
	if d := time.Duration(s.Timeouts.Startup); d > 0 {
		return d
	}
	return DefaultStartupTimeout
}

func callTimeout(s api.MCPServer) time.Duration {
	d := time.Duration(s.Timeouts.Call)
	if d <= 0 {
		return DefaultCallTimeout
	}
	return min(d, MaxCallTimeout)
}

// Tools lists the relayed tools id may use. Servers start on demand, in
// parallel; one that is not ready within its startup timeout is left out
// and announced through the generation once it is.
func (p *Pool) Tools(ctx context.Context, id mcpserver.Identity) ([]mcpserver.RelayedTool, error) {
	var servers []api.MCPServer
	for _, s := range p.o.Servers() {
		if visible(s, id) {
			servers = append(servers, s)
		}
	}
	lists := make([][]mcpserver.RelayedTool, len(servers))
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Go(func() {
			wctx, cancel := context.WithTimeout(ctx, startupTimeout(s))
			defer cancel()
			inst, err := p.acquire(wctx, s, id)
			if err != nil {
				return
			}
			defer inst.release()
			lists[i] = inst.toolList()
		})
	}
	wg.Wait()
	var out []mcpserver.RelayedTool
	for _, l := range lists {
		out = append(out, l...)
	}
	return out, ctx.Err()
}

// Call calls the relayed tool name for id. Every failure of the server is
// a result with IsError; the error is ErrUnknownTool for a name id may not
// use, or the context's error.
func (p *Pool) Call(ctx context.Context, id mcpserver.Identity, name string, args json.RawMessage, progress func(done, total float64, msg string)) (*mcp.CallToolResult, error) {
	server, _, ok := strings.Cut(name, Separator)
	if !ok {
		return nil, ErrUnknownTool
	}
	s, ok := p.config(server)
	if !ok || !visible(s, id) {
		return nil, ErrUnknownTool
	}
	sctx, cancel := context.WithTimeout(ctx, startupTimeout(s))
	inst, err := p.acquire(sctx, s, id)
	cancel()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return p.failure(server, err), nil
	}
	defer inst.release()
	upstream, ok := inst.upstreamName(name)
	if !ok {
		return nil, ErrUnknownTool
	}
	return inst.call(ctx, upstream, args, callTimeout(s), progress)
}

// failure is the tool result for a server that could not be used.
func (p *Pool) failure(server string, err error) *mcp.CallToolResult {
	p.mu.Lock()
	r := p.state(server).redact
	p.mu.Unlock()
	return errorResult(r.redact(describeStartError(server, err)))
}

func errorResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// Errors of acquire.
var (
	errFailed     = errors.New("failed")
	errNeedsLogin = errors.New("needs a sign-in")
	errTooMany    = errors.New("too many processes")
	errClosed     = errors.New("omo is stopping")
)

func describeStartError(server string, err error) string {
	switch {
	case errors.Is(err, errTooMany):
		return fmt.Sprintf("omo runs too many MCP server processes (%d); try again later", MaxProcesses)
	case errors.Is(err, errNeedsLogin):
		return fmt.Sprintf("server %s needs a sign-in: connect it in omo's settings (MCP servers)", server)
	case errors.Is(err, errFailed):
		return fmt.Sprintf("server %s failed too often and is stopped: %s; restart it in omo's settings (MCP servers)", server, errors.Unwrap(err))
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("server %s did not start in time", server)
	case errors.Is(err, errClosed):
		return "omo is stopping"
	}
	return fmt.Sprintf("server %s is not reachable: %s", server, shortReason(err))
}

// shortReason is the first line of err, bounded.
func shortReason(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return cleanLine(shortName(msg))
}

// acquire returns a started instance of s for id, starting it when needed.
// The caller releases it.
func (p *Pool) acquire(ctx context.Context, s api.MCPServer, id mcpserver.Identity) (*instance, error) {
	key, dir := p.placement(s, id)
	ik := instanceKey{s.Name, key}
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, errClosed
		}
		st := p.state(s.Name)
		if st.failed {
			err := fmt.Errorf("%w: %s", errFailed, st.lastError)
			p.mu.Unlock()
			return nil, err
		}
		if s.Auth == api.MCPAuthOAuth && s.Transport != api.MCPTransportStdio {
			p.loadAuth(s.Name, st)
			if st.auth.State != api.MCPAuthStateConnected {
				p.mu.Unlock()
				return nil, errNeedsLogin
			}
		}
		inst := p.instances[ik]
		if inst == nil {
			if wait := time.Until(st.notBefore); wait > 0 {
				p.mu.Unlock()
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(wait):
				}
				continue
			}
			if s.Transport == api.MCPTransportStdio && p.processes() >= p.t.maxProcesses {
				p.mu.Unlock()
				return nil, errTooMany
			}
			inst = newInstance(p, s, key, dir, st)
			p.instances[ik] = inst
			go inst.start()
		}
		inst.users++
		p.mu.Unlock()
		select {
		case <-inst.ready:
		case <-ctx.Done():
			inst.release()
			return nil, ctx.Err()
		}
		if inst.err != nil {
			inst.release()
			// A failed start removed the instance; the next attempt waits
			// for the backoff.
			return nil, inst.err
		}
		return inst, nil
	}
}

// processes counts the stdio processes that run or start. Call with p.mu
// held.
func (p *Pool) processes() int {
	n := 0
	for _, inst := range p.instances {
		if inst.cfg.Transport == api.MCPTransportStdio {
			n++
		}
	}
	return n
}

// failedStart records a failure of server: a start that did not succeed
// or an instance that ended on its own. Call with p.mu held.
func (p *Pool) recordFailure(server string, st *serverState, err error) {
	now := time.Now()
	cut := now.Add(-p.t.failureWindow)
	st.failures = slices.DeleteFunc(st.failures, func(t time.Time) bool { return t.Before(cut) })
	st.failures = append(st.failures, now)
	st.lastError = st.redact.redact(shortReason(err))
	n := len(st.failures)
	if n >= p.t.maxFailures {
		st.failed = true
		st.log.printf("failed %d times within %s; stopped until it is restarted", n, p.t.failureWindow)
		if p.o.Log != nil {
			p.o.Log.Printf("mcp server %s failed %d times and is stopped: %s", server, n, st.lastError)
		}
		return
	}
	st.notBefore = now.Add(p.t.backoff[min(n, len(p.t.backoff))-1])
}

// removeInstance drops inst from the pool if it is still there. Call with
// p.mu held.
func (p *Pool) removeInstance(inst *instance) {
	ik := instanceKey{inst.cfg.Name, inst.key}
	if p.instances[ik] == inst {
		delete(p.instances, ik)
	}
}

// EndInstance stops the per-session instances of key, an agent session
// that ended.
func (p *Pool) EndInstance(key string) {
	if key == "" {
		return
	}
	p.stopWhere(func(inst *instance) bool { return inst.key == key }, "the agent session ended")
}

// stopWhere stops every instance match selects and announces the servers.
func (p *Pool) stopWhere(match func(*instance) bool, why string) {
	p.mu.Lock()
	var stop []*instance
	for _, inst := range p.instances {
		if match(inst) {
			stop = append(stop, inst)
			p.removeInstance(inst)
		}
	}
	p.mu.Unlock()
	p.stopAll(stop, why)
}

func (p *Pool) stopAll(stop []*instance, why string) {
	var wg sync.WaitGroup
	names := map[string]bool{}
	for _, inst := range stop {
		names[inst.cfg.Name] = true
		wg.Go(func() { inst.stop(why) })
	}
	wg.Wait()
	for _, name := range sortedKeys(names) {
		p.changed(name)
	}
}

// Restart stops every instance of the server name and clears its failures;
// the next use starts it again.
func (p *Pool) Restart(name string) error {
	if _, ok := p.config(name); !ok {
		return fmt.Errorf("there is no MCP server %s", name)
	}
	p.reset(name)
	p.changed(name)
	return nil
}

// reset stops the instances of name and forgets its failures and caches.
func (p *Pool) reset(name string) {
	p.mu.Lock()
	var stop []*instance
	for _, inst := range p.instances {
		if inst.cfg.Name == name {
			stop = append(stop, inst)
			p.removeInstance(inst)
		}
	}
	if st := p.servers[name]; st != nil {
		st.failed = false
		st.failures = nil
		st.notBefore = time.Time{}
		st.lastError = ""
		st.headers = nil
		st.authLoaded = false
	}
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, inst := range stop {
		wg.Go(func() { inst.stop("restarted") })
	}
	wg.Wait()
}

// Changed takes the settings of the servers names into use: their
// instances stop and start again with the new settings on their next use.
// A removed server is forgotten.
func (p *Pool) Changed(names []string) {
	for _, name := range names {
		p.reset(name)
		if _, ok := p.config(name); !ok {
			p.mu.Lock()
			if st := p.servers[name]; st != nil {
				st.log.close()
				delete(p.servers, name)
			}
			p.mu.Unlock()
		}
		p.changed(name)
	}
	// The tools may have changed; the caller, which changed the settings,
	// announces that.
	p.gen.Add(1)
}

// Close stops every instance and the janitor.
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	var stop []*instance
	for _, inst := range p.instances {
		stop = append(stop, inst)
	}
	clear(p.instances)
	p.mu.Unlock()
	close(p.stop)
	<-p.done
	p.auth.closeAll()
	var wg sync.WaitGroup
	for _, inst := range stop {
		wg.Go(func() { inst.stop("omo stops") })
	}
	wg.Wait()
	p.mu.Lock()
	for _, st := range p.servers {
		st.log.close()
	}
	p.mu.Unlock()
}

// janitor stops instances that were idle too long.
func (p *Pool) janitor() {
	defer close(p.done)
	tick := time.NewTicker(p.t.janitor)
	defer tick.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-tick.C:
		}
		now := time.Now()
		p.stopWhere(func(inst *instance) bool {
			idle := p.t.idleShared
			if inst.key != "" {
				idle = p.t.idleSession
			}
			return inst.idleSince(now) > idle
		}, "idle")
	}
}

// Status is the state of every configured server.
func (p *Pool) Status() []api.MCPServerState {
	servers := p.o.Servers()
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]api.MCPServerState, 0, len(servers))
	for _, s := range servers {
		st := p.state(s.Name)
		state := api.MCPServerState{
			Name:            s.Name,
			State:           api.MCPStateStopped,
			Tools:           slices.Clone(st.tools),
			Omitted:         slices.Clone(st.omitted),
			LastError:       st.lastError,
			OffersResources: st.offersResources,
			OffersPrompts:   st.offersPrompts,
			Auth:            api.MCPAuthState{State: api.MCPAuthStateNone},
		}
		if state.Tools == nil {
			state.Tools = []string{}
		}
		if state.Omitted == nil {
			state.Omitted = []api.MCPOmittedTool{}
		}
		starting := false
		for _, inst := range p.instances {
			if inst.cfg.Name != s.Name {
				continue
			}
			if inst.running() {
				state.Instances++
				if at := inst.startedAt; state.StartedAt == nil || at.Before(*state.StartedAt) {
					state.StartedAt = &at
				}
			} else {
				starting = true
			}
		}
		if s.Auth == api.MCPAuthOAuth && s.Transport != api.MCPTransportStdio {
			p.loadAuth(s.Name, st)
			state.Auth = st.auth
			state.Auth.Scopes = slices.Clone(st.auth.Scopes)
			if state.Auth.Scopes == nil {
				state.Auth.Scopes = []string{}
			}
		}
		switch {
		case !s.Enabled:
			state.State = api.MCPStateDisabled
		case st.failed:
			state.State = api.MCPStateFailed
		case state.Auth.State == api.MCPAuthStateNeedsLogin:
			state.State = api.MCPStateNeedsLogin
		case state.Instances > 0:
			state.State = api.MCPStateRunning
		case starting:
			state.State = api.MCPStateStarting
		}
		out = append(out, state)
	}
	return out
}

// Log returns the last max lines of the log of server name.
func (p *Pool) Log(name string, max int) ([]api.MCPLogLine, error) {
	if p.o.LogDir == "" {
		return []api.MCPLogLine{}, nil
	}
	if !validServerName(name) {
		return nil, fmt.Errorf("there is no MCP server %s", name)
	}
	return readLog(newServerLog(p.o.LogDir, name, nil).path, max)
}

// validServerName is the rule of server names; it also keeps a name from
// leaving the log folder.
func validServerName(name string) bool {
	if name == "" || len(name) > 24 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// Test starts the draft server s apart from the pool, lists its tools and
// stops it; nothing is kept. An OAuth server uses the sign-in stored under
// its name.
func (p *Pool) Test(ctx context.Context, s api.MCPServer) (api.MCPTestResult, error) {
	began := time.Now()
	res := api.MCPTestResult{Tools: []string{}, Omitted: []api.MCPOmittedTool{}}
	st := &serverState{redact: &redactor{}}
	st.log = newServerLog("", s.Name, st.redact)
	if s.Auth == api.MCPAuthOAuth && s.Transport != api.MCPTransportStdio {
		p.loadAuth(s.Name, st)
		if st.auth.State != api.MCPAuthStateConnected {
			res.Error = describeStartError(s.Name, errNeedsLogin)
			res.Millis = time.Since(began).Milliseconds()
			return res, nil
		}
	}
	_, dir := p.placement(s, mcpserver.Identity{})
	inst := newInstance(p, s, "test", dir, st)
	inst.test = true
	sctx, cancel := context.WithTimeout(ctx, startupTimeout(s))
	defer cancel()
	go inst.start()
	select {
	case <-inst.ready:
	case <-sctx.Done():
		inst.stop("test ended")
		res.Error = describeStartError(s.Name, sctx.Err())
		res.Millis = time.Since(began).Milliseconds()
		return res, nil
	}
	defer inst.stop("test ended")
	if inst.err != nil {
		res.Error = st.redact.redact(describeStartError(s.Name, inst.err))
	} else {
		res.Tools = inst.tools.names()
		res.Omitted = append(res.Omitted, inst.tools.omitted...)
		res.OffersResources = inst.offersResources
		res.OffersPrompts = inst.offersPrompts
	}
	res.Millis = time.Since(began).Milliseconds()
	return res, nil
}
