package mcprelay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcpserver"
)

// maxPages bounds the pages of an upstream's tool list.
const maxPages = 32

// instance is one process of a stdio server or the connection to an HTTP
// or SSE server.
type instance struct {
	pool *Pool
	cfg  api.MCPServer // the settings it was started with
	key  string        // agent session of a per-session instance; "" shared
	dir  string
	st   *serverState
	test bool

	ready chan struct{} // closed when the start ended; err says how
	err   error

	// Set by the start, read after ready.
	session         *mcp.ClientSession
	proc            *process
	startedAt       time.Time
	offersResources bool
	offersPrompts   bool
	cancelConn      context.CancelFunc

	// ts is the OAuth token source of a server with auth oauth.
	ts oauth2.TokenSource
	// authFailed is set when the upstream refused omo's credentials.
	authFailed atomic.Bool

	mu       sync.Mutex
	tools    toolSet
	stopping bool
	users    int // guarded by pool.mu
	lastUsed time.Time
	progress map[string]func(done, total float64, msg string)
	nextTok  int
}

func newInstance(p *Pool, cfg api.MCPServer, key, dir string, st *serverState) *instance {
	inst := &instance{
		pool:     p,
		cfg:      cfg,
		key:      key,
		dir:      dir,
		st:       st,
		ready:    make(chan struct{}),
		lastUsed: time.Now(),
		progress: map[string]func(float64, float64, string){},
	}
	inst.learnSecrets()
	return inst
}

// learnSecrets adds the server's values to its redactor.
func (i *instance) learnSecrets() {
	env := i.pool.o.Env()
	for _, v := range i.cfg.Env {
		i.st.redact.add(v, expand(v, env))
	}
	for _, v := range i.cfg.Headers {
		i.st.redact.add(v, expand(v, env))
		if _, after, ok := cutBearer(expand(v, env)); ok {
			i.st.redact.add(after)
		}
	}
	i.st.redact.add(i.cfg.OAuth.ClientSecret)
}

// release ends a use of the instance.
func (i *instance) release() {
	p := i.pool
	p.mu.Lock()
	i.users--
	p.mu.Unlock()
	i.mu.Lock()
	i.lastUsed = time.Now()
	i.mu.Unlock()
}

// idleSince is how long the instance has had no use; 0 while used or
// starting. Call with pool.mu held.
func (i *instance) idleSince(now time.Time) time.Duration {
	if i.users > 0 || !i.running() {
		return 0
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return now.Sub(i.lastUsed)
}

func (i *instance) running() bool {
	select {
	case <-i.ready:
		return i.err == nil
	default:
		return false
	}
}

func (i *instance) toolList() []mcpserver.RelayedTool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return slices.Clone(i.tools.tools)
}

func (i *instance) upstreamName(name string) (string, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	u, ok := i.tools.upstream[name]
	return u, ok
}

// start connects, lists the tools and records the outcome.
func (i *instance) start() {
	p := i.pool
	name := i.cfg.Name
	connCtx, cancel := context.WithCancel(context.Background())
	i.mu.Lock()
	i.cancelConn = cancel
	stopping := i.stopping
	i.mu.Unlock()
	err := errors.New("stopped")
	if !stopping {
		select {
		case p.starting <- struct{}{}:
			err = i.connect(connCtx)
			<-p.starting
		case <-connCtx.Done():
		}
	}
	p.mu.Lock()
	i.mu.Lock()
	stopping = i.stopping
	i.mu.Unlock()
	if err != nil {
		i.err = err
		auth := i.authFailed.Load()
		if !i.test && !stopping {
			p.removeInstance(i)
			if auth {
				i.st.auth = api.MCPAuthState{State: api.MCPAuthStateNeedsLogin}
				i.st.authLoaded = true
			} else {
				p.recordFailure(name, i.st, err)
			}
		}
		p.mu.Unlock()
		if !stopping {
			i.st.log.printf("start failed: %s", shortReason(err))
		}
		i.closeConn()
		close(i.ready)
		if !i.test {
			p.changed(name)
			if auth {
				p.toolsChanged()
			}
		}
		return
	}
	i.startedAt = time.Now()
	changed := false
	if !i.test {
		changed = i.publishTools()
	}
	p.mu.Unlock()
	if changed {
		// Raised before anyone can list the new tools.
		p.toolsChanged()
	}
	close(i.ready)
	i.st.log.printf("started%s with %d tools", i.where(), len(i.tools.tools))
	go i.watch()
	if !i.test {
		p.changed(name)
	}
}

// where describes the instance for the log, without the session id.
func (i *instance) where() string {
	if i.cfg.Transport != api.MCPTransportStdio {
		return ""
	}
	if i.proc != nil {
		return fmt.Sprintf(" (process %d)", i.proc.cmd.Process.Pid)
	}
	return ""
}

// publishTools records the instance's listing as the server's and reports
// whether the offered names changed. Call with pool.mu held.
func (i *instance) publishTools() bool {
	i.mu.Lock()
	names := i.tools.names()
	omitted := slices.Clone(i.tools.omitted)
	i.mu.Unlock()
	st := i.st
	changed := !slices.Equal(st.tools, names) || st.tools == nil
	st.tools = names
	st.omitted = omitted
	st.offersResources = i.offersResources
	st.offersPrompts = i.offersPrompts
	return changed
}

// connect starts the process or opens the connection and lists the tools,
// within the startup timeout.
func (i *instance) connect(connCtx context.Context) error {
	timeout := startupTimeout(i.cfg)
	sctx, cancel := context.WithTimeout(connCtx, timeout)
	defer cancel()
	var transport mcp.Transport
	switch i.cfg.Transport {
	case api.MCPTransportStdio:
		proc, err := i.startProcess()
		if err != nil {
			return err
		}
		i.proc = proc
		transport = &mcp.IOTransport{Reader: proc.stdout, Writer: proc.stdin, MaxLineLength: i.pool.t.maxMessage}
	case api.MCPTransportHTTP, api.MCPTransportSSE:
		t, err := i.httpTransport()
		if err != nil {
			return err
		}
		transport = t
	default:
		return fmt.Errorf("unknown transport %q", shortName(i.cfg.Transport))
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "omo", Version: i.pool.o.Version}, &mcp.ClientOptions{
		Capabilities:                &mcp.ClientCapabilities{RootsV2: &mcp.RootCapabilities{}},
		ToolListChangedHandler:      func(context.Context, *mcp.ToolListChangedRequest) { go i.relist() },
		ProgressNotificationHandler: i.onProgress,
		LoggingMessageHandler: func(_ context.Context, req *mcp.LoggingMessageRequest) {
			data, _ := json.Marshal(req.Params.Data)
			i.st.log.printf("server log %s: %s", req.Params.Level, data)
		},
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	if i.key != "" {
		client.AddRoots(&mcp.Root{URI: fileURI(i.dir), Name: filepath.Base(i.dir)})
	}
	// The connection lives on connCtx (an SSE stream is bound to the
	// context of its connect); the startup timeout bounds the wait.
	type connected struct {
		cs  *mcp.ClientSession
		err error
	}
	done := make(chan connected, 1)
	go func() {
		cs, err := client.Connect(connCtx, transport, nil)
		done <- connected{cs, err}
	}()
	var c connected
	select {
	case c = <-done:
	case <-sctx.Done():
		i.cancelConn()
		i.closeProcess()
		c = <-done
		if c.cs != nil {
			c.cs.Close()
		}
		return sctx.Err()
	}
	if c.err != nil {
		return i.processError(c.err)
	}
	i.session = c.cs
	if init := c.cs.InitializeResult(); init != nil && init.Capabilities != nil {
		i.offersResources = init.Capabilities.Resources != nil
		i.offersPrompts = init.Capabilities.Prompts != nil
	}
	set, err := i.list(sctx)
	if err != nil {
		return i.processError(err)
	}
	i.mu.Lock()
	i.tools = set
	i.mu.Unlock()
	return nil
}

// processError adds the exit of the process, when it ended, to err.
func (i *instance) processError(err error) error {
	if i.proc == nil {
		return err
	}
	select {
	case <-i.proc.exited:
		return fmt.Errorf("the process exited (%s): %w", i.proc.exitStatus(), err)
	case <-time.After(100 * time.Millisecond):
		return err
	}
}

// list reads the upstream's tool list, bounded in pages and tools.
func (i *instance) list(ctx context.Context) (toolSet, error) {
	var tools []*mcp.Tool
	var cursor string
	limit := i.pool.t.maxTools
	for page := 0; page < maxPages; page++ {
		res, err := i.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return toolSet{}, err
		}
		tools = append(tools, res.Tools...)
		if res.NextCursor == "" || len(tools) > limit {
			break
		}
		cursor = res.NextCursor
	}
	return offerTools(i.cfg.Name, i.cfg.Tools, tools, limit), nil
}

// relist reads the tool list again after the upstream announced a change.
func (i *instance) relist() {
	select {
	case <-i.ready:
	case <-time.After(startupTimeout(i.cfg)):
		return
	}
	if i.err != nil || i.test {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout(i.cfg))
	defer cancel()
	set, err := i.list(ctx)
	if err != nil {
		i.st.log.printf("listing the changed tools failed: %s", shortReason(err))
		return
	}
	i.mu.Lock()
	i.tools = set
	i.mu.Unlock()
	p := i.pool
	p.mu.Lock()
	i.publishTools()
	p.mu.Unlock()
	i.st.log.printf("the server changed its tools; now %d", len(set.tools))
	p.changed(i.cfg.Name)
	// The schema of a tool may have changed under the same name.
	p.toolsChanged()
}

// watch waits for the end of the connection. An end omo did not ask for
// is a crash: the instance leaves the pool and the next use starts it
// again after the backoff.
func (i *instance) watch() {
	err := i.session.Wait()
	i.mu.Lock()
	stopping := i.stopping
	i.stopping = true
	i.mu.Unlock()
	if stopping {
		return
	}
	reason := "the connection ended"
	if err != nil {
		reason = shortReason(err)
	}
	if i.proc != nil {
		select {
		case <-i.proc.exited:
			reason = "the process exited (" + i.proc.exitStatus() + ")"
		case <-time.After(i.pool.t.stopWait):
		}
	}
	p := i.pool
	p.mu.Lock()
	p.removeInstance(i)
	p.recordFailure(i.cfg.Name, i.st, errors.New(reason))
	p.mu.Unlock()
	i.st.log.printf("ended unexpectedly: %s", reason)
	i.closeConn()
	p.changed(i.cfg.Name)
}

// stop ends the instance.
func (i *instance) stop(why string) {
	i.mu.Lock()
	already := i.stopping
	i.stopping = true
	cancel := i.cancelConn
	i.mu.Unlock()
	if cancel != nil && !i.running() {
		// A start in progress is abandoned.
		cancel()
	}
	<-i.ready
	if already && i.err != nil {
		return
	}
	if i.err == nil {
		i.st.log.printf("stopping%s: %s", i.where(), why)
	}
	i.closeConn()
}

// closeConn closes the session and ends the process: its standard input
// is closed, and after stopWait it is killed with everything it started.
func (i *instance) closeConn() {
	if i.session != nil {
		i.session.Close()
	}
	if i.cancelConn != nil {
		i.cancelConn()
	}
	i.closeProcess()
}

func (i *instance) closeProcess() {
	if i.proc == nil {
		return
	}
	i.proc.stdin.Close()
	select {
	case <-i.proc.exited:
	case <-time.After(i.pool.t.stopWait):
	}
	i.proc.group.close()
	<-i.proc.exited
	i.proc.stdout.Close()
}

// onProgress hands an upstream progress notification to the call that
// asked for it.
func (i *instance) onProgress(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
	tok, ok := req.Params.ProgressToken.(string)
	if !ok {
		return
	}
	i.mu.Lock()
	fn := i.progress[tok]
	i.mu.Unlock()
	if fn != nil {
		fn(req.Params.Progress, req.Params.Total, cleanLine(shortName(req.Params.Message)))
	}
}

// call calls the upstream tool within timeout and maps every failure to a
// tool result with IsError.
func (i *instance) call(ctx context.Context, tool string, args json.RawMessage, timeout time.Duration, progress func(done, total float64, msg string)) (*mcp.CallToolResult, error) {
	name := i.cfg.Name
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	params := &mcp.CallToolParams{Name: tool, Arguments: args}
	if progress != nil {
		i.mu.Lock()
		i.nextTok++
		tok := "omo-" + strconv.Itoa(i.nextTok)
		i.progress[tok] = progress
		i.mu.Unlock()
		defer func() {
			i.mu.Lock()
			delete(i.progress, tok)
			i.mu.Unlock()
		}()
		params.SetProgressToken(tok)
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := i.session.CallTool(cctx, params)
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(cctx.Err(), context.DeadlineExceeded):
		i.st.log.printf("call of %s did not answer within %s", shortName(tool), timeout)
		return errorResult(fmt.Sprintf("server %s did not answer within %s", name, timeout)), nil
	default:
		var rpc *jsonrpc.Error
		if errors.As(err, &rpc) {
			msg := i.st.redact.redact(cleanLine(shortName(rpc.Message)))
			return errorResult(fmt.Sprintf("server %s: %s (%d)", name, msg, rpc.Code)), nil
		}
		reason := i.st.redact.redact(shortReason(err))
		i.st.log.printf("call of %s failed: %s", shortName(tool), reason)
		return errorResult(fmt.Sprintf("server %s is not reachable: %s", name, reason)), nil
	}
	if res.NeedsInput() {
		return errorResult(fmt.Sprintf("server %s asked for input (elicitation), which omo does not relay", name)), nil
	}
	if size, ok := withinSize(res, i.pool.t.maxResult); !ok {
		i.st.log.printf("call of %s answered %d bytes, more than omo relays", shortName(tool), size)
		return errorResult(fmt.Sprintf("server %s answered with %s, more than omo relays (%s)", name, mib(size), mib(i.pool.t.maxResult))), nil
	}
	return res, nil
}

func withinSize(res *mcp.CallToolResult, limit int) (int, bool) {
	raw, err := json.Marshal(res)
	if err != nil {
		return 0, false
	}
	return len(raw), len(raw) <= limit
}

func mib(n int) string {
	return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
}

func fileURI(dir string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(dir)}).String()
}

// process is a running stdio server.
type process struct {
	cmd    *exec.Cmd
	group  *processGroup
	stdin  io.WriteCloser
	stdout io.ReadCloser
	exited chan struct{}
	err    error
}

func (pr *process) exitStatus() string {
	if pr.err == nil {
		return "status 0"
	}
	return pr.err.Error()
}

// startProcess starts the server's command in the instance's folder with
// omo's environment (without OMO_*) and the server's values.
func (i *instance) startProcess() (*process, error) {
	if i.dir != "" {
		if fi, err := os.Stat(i.dir); err != nil || !fi.IsDir() {
			return nil, fmt.Errorf("the working folder %s does not exist", i.dir)
		}
	}
	cmd := exec.Command(i.cfg.Command, i.cfg.Args...)
	cmd.Env = childEnvironment(i.pool.o.Env(), i.cfg.Env)
	cmd.Dir = i.dir
	group, err := newProcessGroup(cmd)
	if err != nil {
		return nil, err
	}
	// The process is ended by closeProcess, not by a context.
	cmd.Cancel = nil
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = inR, outW, errW
	err = cmd.Start()
	inR.Close()
	outW.Close()
	errW.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		errR.Close()
		group.close()
		return nil, fmt.Errorf("cannot start: %w", err)
	}
	if err := group.started(cmd); err != nil {
		i.st.log.printf("cannot put the process into its own group: %s", err)
	}
	pr := &process{cmd: cmd, group: group, stdin: inW, stdout: outR, exited: make(chan struct{})}
	go func() {
		i.st.log.copyLines(errR, "stderr: ")
		errR.Close()
	}()
	go func() {
		pr.err = cmd.Wait()
		close(pr.exited)
	}()
	return pr, nil
}
