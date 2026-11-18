package mcprelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcpauth"
)

// headersCommandTimeout bounds the header command; maxHeadersOutput its
// output.
const (
	headersCommandTimeout = 10 * time.Second
	maxHeadersOutput      = 64 << 10
)

// httpTransport is the client transport of an HTTP or SSE server: every
// request goes through upstreamTransport, which adds omo's own credentials
// for the server and nothing else.
func (i *instance) httpTransport() (mcp.Transport, error) {
	u, err := url.Parse(i.cfg.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("the server's address is not an http or https URL")
	}
	if i.usesOAuth() {
		ts, err := i.pool.tokenSource(i)
		if err != nil {
			i.authFailed.Store(true)
			return nil, err
		}
		i.ts = ts
	}
	hc := &http.Client{Transport: &upstreamTransport{inst: i, base: i.pool.base, origin: u}, CheckRedirect: sameOriginRedirect}
	if i.cfg.Transport == api.MCPTransportSSE {
		return &mcp.SSEClientTransport{Endpoint: i.cfg.URL, HTTPClient: hc, MaxEventSize: i.pool.t.maxMessage}, nil
	}
	return &mcp.StreamableClientTransport{Endpoint: i.cfg.URL, HTTPClient: hc, MaxEventSize: i.pool.t.maxMessage}, nil
}

func (i *instance) usesOAuth() bool {
	return i.cfg.Auth == api.MCPAuthOAuth
}

// upstreamTransport sets the server's headers (static values with ${VAR}
// expanded and the header command's output) and its OAuth access token on
// every request. Whatever authorization the request carried is removed
// first, so nothing of the client that reached omo can travel upstream.
// A request to another origin than the server's address (an SSE server's
// message endpoint elsewhere) is refused, so omo's credentials never
// leave the server's host.
type upstreamTransport struct {
	inst   *instance
	base   http.RoundTripper
	origin *url.URL
}

// errOtherOrigin refuses a request the server sent to another host.
var errOtherOrigin = errors.New("the server sent omo to another host or from https to http, which omo refuses")

func (t *upstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	i := t.inst
	if !sameOrigin(t.origin, req.URL) {
		return nil, errOtherOrigin
	}
	resp, used, err := t.send(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized && len(i.cfg.HeadersCommand) > 0 && i.cfg.Auth != api.MCPAuthNone &&
		(req.Body == nil || req.GetBody != nil) {
		// The command's headers may have expired: run it again once.
		drain(resp)
		i.pool.forgetHeaders(i.st)
		retry := req.Clone(req.Context())
		if req.GetBody != nil {
			if retry.Body, err = req.GetBody(); err != nil {
				return nil, err
			}
		}
		if resp, used, err = t.send(retry); err != nil {
			return nil, err
		}
	}
	if i.usesOAuth() && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden && insufficientScope(resp)) {
		drain(resp)
		i.mu.Lock()
		i.refusedAuth = used
		i.mu.Unlock()
		i.authFailed.Store(true)
		if !i.test {
			// A Test only reports the refusal; the saved server keeps its
			// sign-in.
			go i.pool.loginLost(i, used)
		}
		return nil, errNeedsLogin
	}
	if req.Method == http.MethodPost {
		resp.Body = &limitedBody{rc: resp.Body, left: int64(i.pool.t.maxMessage)}
	}
	return resp, nil
}

func (t *upstreamTransport) send(req *http.Request) (*http.Response, mcpauth.UpstreamAuth, error) {
	i := t.inst
	var used mcpauth.UpstreamAuth
	out := req.Clone(req.Context())
	out.Header.Del("Authorization")
	out.Header.Del("Cookie")
	headers, err := i.pool.headers(req.Context(), i.cfg, i.st)
	if err != nil {
		return nil, used, err
	}
	for k, v := range headers {
		out.Header.Set(k, v)
	}
	if i.ts != nil {
		tok, err := i.ts.Token()
		if err != nil {
			i.authFailed.Store(true)
			return nil, used, errNeedsLogin
		}
		used = i.ts.snapshot()
		// Bind a refusal to the token this request actually sent, even when a
		// concurrent request has already refreshed the source.
		copyToken := *tok
		used.Token = &copyToken
		tok.SetAuthHeader(out)
	}
	resp, err := t.base.RoundTrip(out)
	return resp, used, err
}

func insufficientScope(resp *http.Response) bool {
	for _, v := range resp.Header.Values("WWW-Authenticate") {
		if strings.Contains(v, "insufficient_scope") {
			return true
		}
	}
	return false
}

func drain(resp *http.Response) {
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
}

// limitedBody fails a body that is longer than omo reads of one answer.
type limitedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, errors.New("the answer is larger than omo reads")
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *limitedBody) Close() error { return b.rc.Close() }

// headers returns the headers of s for a request: none with auth none;
// otherwise the static headers expanded, overlaid with the output of the
// header command, which is cached until the server answers 401.
func (p *Pool) headers(ctx context.Context, s api.MCPServer, st *serverState) (map[string]string, error) {
	if s.Auth == api.MCPAuthNone {
		return nil, nil
	}
	env := p.o.Env()
	out := make(map[string]string, len(s.Headers))
	for k, v := range s.Headers {
		out[k] = expand(v, env)
	}
	if len(s.HeadersCommand) == 0 {
		return out, nil
	}
	p.mu.Lock()
	cached := st.headers
	p.mu.Unlock()
	if cached == nil {
		got, err := p.runHeadersCommand(ctx, s, st)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		st.headers = got
		p.mu.Unlock()
		cached = got
	}
	maps.Copy(out, cached)
	return out, nil
}

// forgetHeaders drops the cached output of the header command of st (the
// instance's own state, so a test never touches the saved server's).
func (p *Pool) forgetHeaders(st *serverState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st.headers = nil
}

// runHeadersCommand runs the header command of s, which prints a JSON
// object of header names and values. Its output never reaches an error or
// the log.
func (p *Pool) runHeadersCommand(ctx context.Context, s api.MCPServer, st *serverState) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(ctx, headersCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.HeadersCommand[0], s.HeadersCommand[1:]...)
	cmd.Env = childEnvironment(p.o.Env(), nil)
	cmd.Dir = p.o.Home
	group, err := newProcessGroup(cmd)
	if err != nil {
		return nil, err
	}
	defer group.close()
	var stdout bytes.Buffer
	cmd.Stdout = &capped{buf: &stdout, left: maxHeadersOutput}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := startGrouped(cmd, group); err != nil {
		return nil, fmt.Errorf("the header command cannot start: %w", err)
	}
	st.log.copyLines(stderr, "header command stderr: ")
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("the header command did not finish within %s", headersCommandTimeout)
		}
		return nil, fmt.Errorf("the header command failed: %s", err)
	}
	var values map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &values); err != nil {
		return nil, errors.New("the header command did not print a JSON object of strings")
	}
	for k, v := range values {
		st.redact.add(v)
		if _, after, ok := cutBearer(v); ok {
			st.redact.add(after)
		}
		if strings.ContainsAny(k+v, "\r\n") {
			return nil, errors.New("the header command printed a header with a line break")
		}
	}
	return values, nil
}

// cutBearer splits "Bearer <token>".
func cutBearer(v string) (string, string, bool) {
	scheme, rest, ok := strings.Cut(v, " ")
	if !ok || !strings.EqualFold(scheme, "bearer") {
		return "", "", false
	}
	return scheme, strings.TrimSpace(rest), true
}

// capped keeps at most left bytes and drops the rest.
type capped struct {
	buf  *bytes.Buffer
	left int
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > c.left {
		p = p[:c.left]
	}
	c.buf.Write(p)
	c.left -= len(p)
	return n, nil
}
