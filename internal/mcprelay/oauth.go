package mcprelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/mcpauth"
)

// oauthHTTPTimeout bounds each request to an authorization server.
const oauthHTTPTimeout = 30 * time.Second

// authorizer runs omo's interactive sign-in at a server's authorization
// server, started from the server's 401 answer: discovery, client
// registration (a pre-registered client or dynamic registration), PKCE,
// the browser hand-off through the code fetcher and the code exchange. It
// is the only part of the package that uses the SDK's OAuth client, so it
// can be replaced; refreshing and persisting tokens is omo's own.
type authorizer interface {
	Authorize(ctx context.Context, req *http.Request, resp *http.Response) error
}

// registration is what omo keeps of a sign-in beside the tokens, so that
// it can refresh them after a restart and revoke them on Disconnect.
type registration struct {
	AuthURL       string           `json:"auth_url"`
	TokenURL      string           `json:"token_url"`
	AuthStyle     oauth2.AuthStyle `json:"auth_style"`
	RevocationURL string           `json:"revocation_url,omitempty"`
	// Registered says the client was registered by omo (dynamic
	// registration) rather than configured.
	Registered bool `json:"registered"`
	// ServerURL, Resource and ConfiguredClient are the server's settings
	// the sign-in was given for: its address, its resource override and
	// its configured client id. The sign-in is used only while they are
	// unchanged.
	ServerURL        string `json:"server_url"`
	Resource         string `json:"resource,omitempty"`
	ConfiguredClient string `json:"configured_client_id,omitempty"`
}

// boundTo reports whether the stored sign-in a was given for the settings
// of s: an OAuth server at the same address, with the same resource and
// configured client. Any other sign-in is never used for s.
func boundTo(a mcpauth.UpstreamAuth, s api.MCPServer) bool {
	if s.Auth != api.MCPAuthOAuth || s.Transport == api.MCPTransportStdio {
		return false
	}
	var reg registration
	if json.Unmarshal(a.Registration, &reg) != nil {
		return false
	}
	return reg.ServerURL != "" && reg.ServerURL == s.URL && reg.Resource == s.OAuth.Resource &&
		reg.ConfiguredClient == s.OAuth.ClientID
}

// loadBound returns the stored sign-in of s when it was given for s's
// settings; ok is false otherwise.
func (p *Pool) loadBound(ctx context.Context, s api.MCPServer) (mcpauth.UpstreamAuth, bool, error) {
	a, ok, err := p.store.Load(ctx, s.Name)
	if err != nil || !ok || !boundTo(a, s) {
		return mcpauth.UpstreamAuth{}, false, err
	}
	return a, true, nil
}

// dropStaleSignIn deletes the stored sign-in of server name when the
// server was removed or its settings no longer match it, and revokes it
// in the background.
func (p *Pool) dropStaleSignIn(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ctx := context.Background()
	a, ok, err := p.store.Load(ctx, name)
	if err != nil || !ok {
		return
	}
	s, exists := p.o.Configured(name)
	if exists && boundTo(a, s) {
		return
	}
	if err := p.store.Delete(ctx, name); err != nil {
		p.logf("mcp server %s: deleting its old sign-in failed: %v", name, err)
		return
	}
	go p.revoke(context.Background(), name, a, p.logf)
}

// logf writes a line to omo's log, if there is one.
func (p *Pool) logf(format string, args ...any) {
	if p.o.Log != nil {
		p.o.Log.Printf(format, args...)
	}
}

// login is one interactive sign-in started by Connect.
type login struct {
	server   string
	redirect string
	resource string
	urls     chan string
	result   chan callbackResult
	done     chan struct{}
	err      error
	expires  time.Time
	called   bool

	// Set by the authorizer.
	conf       *oauth2.Config
	token      *oauth2.Token
	issuer     string
	revocation string
	registered bool
}

type callbackResult struct {
	code, state, iss string
	err              error
}

func (l *login) finish(err error) {
	l.err = err
	close(l.done)
}

// authFlows are the sign-ins waiting for their callback, by state.
type authFlows struct {
	mu      sync.Mutex
	pending map[string]*login
	cancel  map[*login]context.CancelFunc
	closed  bool
}

func (f *authFlows) init() {
	f.pending = map[string]*login{}
	f.cancel = map[*login]context.CancelFunc{}
}

func (f *authFlows) add(state string, l *login) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending[state] = l
}

// take removes and returns the sign-in of state, if it has not expired.
func (f *authFlows) take(state string) *login {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.pending[state]
	delete(f.pending, state)
	if l == nil || time.Now().After(l.expires) {
		return nil
	}
	return l
}

func (f *authFlows) start(l *login, cancel context.CancelFunc) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false
	}
	f.cancel[l] = cancel
	return true
}

func (f *authFlows) end(l *login) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cancel, l)
	for state, p := range f.pending {
		if p == l {
			delete(f.pending, state)
		}
	}
}

func (f *authFlows) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for _, cancel := range f.cancel {
		cancel()
	}
}

// fetch is the authorizer's code fetcher: it hands the authorization
// address to the waiting Connect and waits for the callback.
func (l *login) fetch(f *authFlows, wait time.Duration) auth.AuthorizationCodeFetcher {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		u, err := url.Parse(args.URL)
		if err != nil {
			return nil, errors.New("the authorization address is not a URL")
		}
		q := u.Query()
		state := q.Get("state")
		if l.resource != "" {
			q.Set("resource", l.resource)
			u.RawQuery = q.Encode()
		}
		l.expires = time.Now().Add(wait)
		f.add(state, l)
		select {
		case l.urls <- u.String():
		default:
		}
		select {
		case r := <-l.result:
			if r.err != nil {
				return nil, r.err
			}
			return &auth.AuthorizationResult{Code: r.code, State: r.state, Iss: r.iss}, nil
		case <-ctx.Done():
			return nil, errors.New("the sign-in was not completed in time")
		}
	}
}

// newAuthorizer builds the SDK's authorization code handler for s. A
// configured client id is used as a pre-registered client; otherwise a
// client omo registered before for the same callback address is reused,
// and else omo registers one.
func (p *Pool) newAuthorizer(s api.MCPServer, prior mcpauth.UpstreamAuth, hasPrior bool, l *login) (authorizer, error) {
	meta := &metaRecorder{base: guardedTransport(s.URL), l: l}
	client := &http.Client{Timeout: oauthHTTPTimeout, Transport: &resourceOverride{base: meta, resource: s.OAuth.Resource},
		CheckRedirect: sameOriginRedirect}
	cfg := &auth.AuthorizationCodeHandlerConfig{
		RedirectURL:              l.redirect,
		AuthorizationCodeFetcher: l.fetch(&p.auth, p.t.loginWait),
		Client:                   client,
		RequestRefreshToken:      true,
		AcceptUnadvertisedIss:    true,
		NewTokenSource: func(ctx context.Context, conf *oauth2.Config, tok *oauth2.Token) (oauth2.TokenSource, error) {
			l.conf, l.token = conf, tok
			// The handler may refresh at once (reading the granted
			// scopes); the token stored is the latest.
			return &latestToken{src: conf.TokenSource(ctx, tok), l: l}, nil
		},
	}
	if scopes := s.OAuth.Scopes; len(scopes) > 0 {
		cfg.ScopeFilter = func([]string) []string { return scopes }
	}
	var reg registration
	if hasPrior {
		_ = json.Unmarshal(prior.Registration, &reg)
	}
	switch {
	case s.OAuth.ClientID != "":
		cfg.PreregisteredClient = &oauthex.ClientCredentials{ClientID: s.OAuth.ClientID}
		if s.OAuth.ClientSecret != "" {
			cfg.PreregisteredClient.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: s.OAuth.ClientSecret}
		}
	case hasPrior && reg.Registered && prior.ClientID != "" && prior.RedirectURI == l.redirect:
		l.registered = true
		cfg.PreregisteredClient = &oauthex.ClientCredentials{ClientID: prior.ClientID}
		if prior.ClientSecret != "" {
			cfg.PreregisteredClient.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: prior.ClientSecret}
		}
	default:
		l.registered = true
		cfg.DynamicClientRegistrationConfig = &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			RedirectURIs:            []string{l.redirect},
			ClientName:              "omo",
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
		}}
	}
	return auth.NewAuthorizationCodeHandler(cfg)
}

// latestToken keeps the last token its source gave in the login.
type latestToken struct {
	src oauth2.TokenSource
	l   *login
}

func (t *latestToken) Token() (*oauth2.Token, error) {
	tok, err := t.src.Token()
	if err == nil {
		t.l.token = tok
	}
	return tok, err
}

// metaRecorder notes the issuer and revocation endpoint of the
// authorization server metadata the authorizer reads.
type metaRecorder struct {
	base http.RoundTripper
	l    *login
}

func (m *metaRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := m.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	path := req.URL.Path
	if !strings.Contains(path, "/.well-known/oauth-authorization-server") && !strings.Contains(path, "/.well-known/openid-configuration") {
		return resp, nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return nil, err
	}
	var meta struct {
		Issuer     string `json:"issuer"`
		Revocation string `json:"revocation_endpoint"`
	}
	if json.Unmarshal(body, &meta) == nil && meta.Issuer != "" {
		m.l.issuer, m.l.revocation = meta.Issuer, meta.Revocation
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// resourceOverride replaces the RFC 8707 resource of token requests with
// the server's configured one.
type resourceOverride struct {
	base     http.RoundTripper
	resource string
}

func (r *resourceOverride) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.resource == "" || req.Method != http.MethodPost || req.Body == nil ||
		!strings.HasPrefix(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return r.base.RoundTrip(req)
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	form, err := url.ParseQuery(string(body))
	if err == nil && form.Has("resource") {
		form.Set("resource", r.resource)
		body = []byte(form.Encode())
	}
	out := req.Clone(req.Context())
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	return r.base.RoundTrip(out)
}

// Connect starts omo's sign-in at the OAuth server name and returns the
// address the browser opens. The sign-in completes when the provider sends
// the browser to Callback; the server's state then becomes connected.
func (p *Pool) Connect(ctx context.Context, name string) (string, error) {
	s, ok := p.config(name)
	if !ok {
		return "", fmt.Errorf("there is no MCP server %s", name)
	}
	if s.Auth != api.MCPAuthOAuth || s.Transport == api.MCPTransportStdio {
		return "", fmt.Errorf("server %s does not sign in with OAuth", name)
	}
	redirect := p.o.Redirect(ctx)
	if redirect == "" {
		return "", errors.New("omo does not know the address of its OAuth callback")
	}
	prior, hasPrior, err := p.loadBound(ctx, s)
	if err != nil {
		return "", fmt.Errorf("reading the sign-in of server %s: %w", name, err)
	}
	l := &login{
		server:   name,
		redirect: redirect,
		resource: s.OAuth.Resource,
		urls:     make(chan string, 1),
		result:   make(chan callbackResult, 1),
		done:     make(chan struct{}),
	}
	az, err := p.newAuthorizer(s, prior, hasPrior, l)
	if err != nil {
		return "", fmt.Errorf("server %s: %w", name, err)
	}
	env := p.o.Env()
	p.mu.Lock()
	learnSecrets(p.state(name).redact, s, env)
	p.mu.Unlock()
	req, resp, err := p.probe(ctx, s)
	if err != nil {
		return "", fmt.Errorf("server %s is not reachable: %s", name, p.cleanAuthError(name, err))
	}
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		drain(resp)
		return "", fmt.Errorf("server %s did not ask for a sign-in (status %d)", name, resp.StatusCode)
	}
	actx, cancel := context.WithTimeout(context.Background(), p.t.loginWait)
	if !p.auth.start(l, cancel) {
		cancel()
		drain(resp)
		return "", errClosed
	}
	go func() {
		defer cancel()
		defer p.auth.end(l)
		err := az.Authorize(actx, req, resp)
		if err == nil {
			err = p.saveLogin(s, l)
		}
		// The server is taken into use before the callback page says so.
		p.loginFinished(s.Name, l, err)
		l.finish(err)
	}()
	select {
	case u := <-l.urls:
		return u, nil
	case <-l.done:
		if l.err == nil {
			return "", nil
		}
		return "", fmt.Errorf("server %s: %s", name, p.cleanAuthError(name, l.err))
	case <-time.After(p.t.connectWait):
		return "", fmt.Errorf("server %s did not start a sign-in within %s", name, p.t.connectWait)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// cleanAuthError is err as the user may see it: one line, redacted.
func (p *Pool) cleanAuthError(name string, err error) string {
	p.mu.Lock()
	r := p.state(name).redact
	p.mu.Unlock()
	return shortReason(r, err)
}

// probe sends a request without credentials to the server so that it
// answers with the 401 that starts the discovery.
func (p *Pool) probe(ctx context.Context, s api.MCPServer) (*http.Request, *http.Response, error) {
	ctx, cancel := context.WithTimeout(ctx, oauthHTTPTimeout)
	defer cancel()
	method, body := http.MethodPost, []byte(`{"jsonrpc":"2.0","id":0,"method":"ping"}`)
	if s.Transport == api.MCPTransportSSE {
		method, body = http.MethodGet, nil
	}
	req, err := http.NewRequestWithContext(ctx, method, s.URL, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := p.base.RoundTrip(req)
	if err != nil {
		return nil, nil, err
	}
	// Keep only what Authorize reads: the status and the challenge.
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(b))
	return req, resp, nil
}

// saveLogin stores the sign-in the authorizer completed.
func (p *Pool) saveLogin(s api.MCPServer, l *login) error {
	if l.conf == nil || l.token == nil {
		return errors.New("the authorization server gave no token")
	}
	issuer := l.issuer
	if issuer == "" {
		if u, err := url.Parse(l.conf.Endpoint.AuthURL); err == nil {
			issuer = u.Scheme + "://" + u.Host
		}
	}
	reg := registration{
		AuthURL:          l.conf.Endpoint.AuthURL,
		TokenURL:         l.conf.Endpoint.TokenURL,
		AuthStyle:        l.conf.Endpoint.AuthStyle,
		RevocationURL:    l.revocation,
		Registered:       l.registered,
		ServerURL:        s.URL,
		Resource:         s.OAuth.Resource,
		ConfiguredClient: s.OAuth.ClientID,
	}
	raw, _ := json.Marshal(reg)
	a := mcpauth.UpstreamAuth{
		Server:       s.Name,
		Issuer:       issuer,
		ClientID:     l.conf.ClientID,
		ClientSecret: l.conf.ClientSecret,
		RedirectURI:  l.redirect,
		State:        api.MCPAuthStateConnected,
		Registration: raw,
		Token:        l.token,
		Scopes:       grantedScopes(l.token, l.conf.Scopes),
	}
	p.learnAuthSecrets(s.Name, a)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errClosed
	}
	return p.store.Save(context.Background(), a)
}

func grantedScopes(tok *oauth2.Token, requested []string) []string {
	if s, ok := tok.Extra("scope").(string); ok && s != "" {
		return strings.Fields(s)
	}
	return requested
}

func (p *Pool) learnAuthSecrets(name string, a mcpauth.UpstreamAuth) {
	p.mu.Lock()
	r := p.state(name).redact
	p.mu.Unlock()
	r.add(a.ClientSecret)
	if a.Token != nil {
		r.add(a.Token.AccessToken, a.Token.RefreshToken)
	}
}

// loginFinished takes a completed sign-in into use, or records why it
// failed.
func (p *Pool) loginFinished(name string, l *login, err error) {
	if err != nil {
		msg := p.cleanAuthError(name, err)
		p.mu.Lock()
		st := p.state(name)
		p.mu.Unlock()
		st.log.printf("sign-in failed: %s", msg)
		if l.called {
			p.mu.Lock()
			st.auth = api.MCPAuthState{State: api.MCPAuthStateError}
			st.authLoaded = true
			p.mu.Unlock()
			p.changed(name)
		}
		return
	}
	p.mu.Lock()
	st := p.state(name)
	p.mu.Unlock()
	st.log.printf("signed in")
	p.reset(name)
	p.changed(name)
	p.toolsChanged()
}

// Callback is omo's OAuth callback (GET /oauth/callback): the provider
// sends the browser here with the code of a sign-in Connect started. It
// answers a small page saying how the sign-in ended.
func (p *Pool) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	l := p.auth.take(q.Get("state"))
	if l == nil || q.Get("state") == "" {
		callbackPage(w, http.StatusBadRequest, "This sign-in is unknown or expired.", "")
		return
	}
	l.called = true
	if e := q.Get("error"); e != "" {
		reason := e
		if d := q.Get("error_description"); d != "" {
			reason += ": " + d
		}
		l.result <- callbackResult{err: fmt.Errorf("the provider refused the sign-in: %s", cleanLine(shortName(reason)))}
	} else {
		l.result <- callbackResult{code: q.Get("code"), state: q.Get("state"), iss: q.Get("iss")}
	}
	select {
	case <-l.done:
	case <-time.After(p.t.connectWait):
		callbackPage(w, http.StatusOK, "Signing in to "+l.server+" takes longer than expected; omo's settings show when it is done.", "")
		return
	}
	if l.err != nil {
		callbackPage(w, http.StatusBadGateway, "Signing in to "+l.server+" failed: "+p.cleanAuthError(l.server, l.err), l.server)
		return
	}
	callbackPage(w, http.StatusOK, "Connected to "+l.server+". You can close this tab.", l.server)
}

func callbackPage(w http.ResponseWriter, status int, msg, server string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.WriteHeader(status)
	link := ""
	if server != "" {
		link = `<p><a href="/settings/mcp">omo's MCP settings</a></p>`
	}
	fmt.Fprintf(w, "<!doctype html><html><head><meta charset=\"utf-8\"><title>omo</title></head><body><p>%s</p>%s</body></html>\n", html.EscapeString(msg), link)
}

// loadAuth reads the sign-in state of s from the store once; a stored
// sign-in given for other settings counts as none. Call with p.mu held.
func (p *Pool) loadAuth(s api.MCPServer, st *serverState) {
	if st.authLoaded {
		return
	}
	st.authLoaded = true
	a, ok, err := p.loadBound(context.Background(), s)
	switch {
	case err != nil:
		st.auth = api.MCPAuthState{State: api.MCPAuthStateError}
		st.lastError = "the stored sign-in cannot be read"
	case !ok || a.Token == nil || a.State == api.MCPAuthStateNeedsLogin:
		st.auth = api.MCPAuthState{State: api.MCPAuthStateNeedsLogin}
	case a.State == api.MCPAuthStateError:
		st.auth = api.MCPAuthState{State: api.MCPAuthStateError}
	default:
		st.auth = api.MCPAuthState{State: api.MCPAuthStateConnected, Scopes: a.Scopes}
		if !a.Token.Expiry.IsZero() {
			exp := a.Token.Expiry
			st.auth.ExpiresAt = &exp
		}
	}
}

// tokenSource returns the refreshing token source of the stored sign-in of
// s, when it was given for s's settings; every refreshed token is stored.
// A test's source never marks the sign-in as lost.
func (p *Pool) tokenSource(inst *instance) (*persistedSource, error) {
	s, st, test := inst.cfg, inst.st, inst.test
	a, ok, err := p.loadBound(context.Background(), s)
	if err != nil {
		return nil, fmt.Errorf("reading the sign-in: %w", err)
	}
	if !ok || a.Token == nil || a.State != api.MCPAuthStateConnected {
		return nil, errNeedsLogin
	}
	var reg registration
	if err := json.Unmarshal(a.Registration, &reg); err != nil || reg.TokenURL == "" {
		return nil, errNeedsLogin
	}
	st.redact.add(a.ClientSecret, a.Token.AccessToken, a.Token.RefreshToken)
	conf := &oauth2.Config{
		ClientID:     a.ClientID,
		ClientSecret: a.ClientSecret,
		Endpoint:     oauth2.Endpoint{AuthURL: reg.AuthURL, TokenURL: reg.TokenURL, AuthStyle: reg.AuthStyle},
		RedirectURL:  a.RedirectURI,
		Scopes:       a.Scopes,
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: oauthHTTPTimeout,
		Transport: guardedTransport(s.URL), CheckRedirect: sameOriginRedirect})
	return &persistedSource{p: p, inst: inst, auth: a, src: conf.TokenSource(ctx, a.Token), last: a.Token.AccessToken, test: test}, nil
}

// persistedSource stores every token its source refreshes (a rotated
// refresh token must be kept, also by a test), and turns a refused refresh
// into the needs_login state unless it serves a test.
type persistedSource struct {
	p    *Pool
	inst *instance
	mu   sync.Mutex
	auth mcpauth.UpstreamAuth
	src  oauth2.TokenSource
	last string
	test bool
}

func (s *persistedSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, err := s.src.Token()
	if err != nil {
		var re *oauth2.RetrieveError
		if !s.test && errors.As(err, &re) && (re.ErrorCode == "invalid_grant" || re.Response != nil && re.Response.StatusCode == http.StatusUnauthorized) {
			expected := cloneAuth(s.auth)
			go s.p.loginLost(s.inst, expected)
		}
		return nil, err
	}
	if tok.AccessToken != s.last {
		p := s.p
		updated := cloneAuth(s.auth)
		updated.Token = tok
		updated.State = api.MCPAuthStateConnected
		p.learnAuthSecrets(s.auth.Server, updated)
		p.mu.Lock()
		current, ok, err := p.store.Load(context.Background(), s.auth.Server)
		if err != nil {
			p.mu.Unlock()
			return nil, err
		}
		// A refresh of a retired sign-in must not replace a newer credential.
		if !p.closed && ok && sameSignIn(current, s.auth) {
			s.last = tok.AccessToken
			s.auth = updated
			err = p.store.Save(context.Background(), s.auth)
		}
		st := p.servers[s.auth.Server]
		p.mu.Unlock()
		if err != nil {
			if st != nil {
				st.log.printf("storing the refreshed sign-in failed: %s", shortReason(st.redact, err))
			}
			return nil, err
		}
	}
	return tok, nil
}

func (s *persistedSource) snapshot() mcpauth.UpstreamAuth {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneAuth(s.auth)
}

// sameSignIn identifies the credential a background operation used, including
// the binding to its server settings. Callers compare and write under pool.mu.
func sameSignIn(a, b mcpauth.UpstreamAuth) bool {
	return a.Token != nil && b.Token != nil && a.Token.AccessToken == b.Token.AccessToken &&
		a.Token.RefreshToken == b.Token.RefreshToken && a.ClientID == b.ClientID &&
		a.ClientSecret == b.ClientSecret && a.Issuer == b.Issuer && a.RedirectURI == b.RedirectURI &&
		bytes.Equal(a.Registration, b.Registration)
}

// loginLost handles an upstream that refused omo's token. Retirement cancels
// the wait; the instance and credential are checked again before invalidation.
func (p *Pool) loginLost(inst *instance, expected mcpauth.UpstreamAuth) {
	if inst.test || inst.ts == nil {
		return
	}
	select {
	case <-inst.ready:
	case <-inst.connCtx.Done():
		return
	}
	p.markNeedsLogin(expected, inst, "the server refused omo's sign-in")
}

// invalidateSignIn marks only the expected stored credential as lost. The
// caller holds pool.mu, shared with every credential write in this package.
func (p *Pool) invalidateSignIn(expected mcpauth.UpstreamAuth, why string) bool {
	ctx := context.Background()
	a, ok, err := p.store.Load(ctx, expected.Server)
	if err != nil || !ok || !sameSignIn(a, expected) {
		return false
	}
	a.State = api.MCPAuthStateNeedsLogin
	a.Error = why
	a.Token = nil
	_ = p.store.Save(ctx, a)
	return true
}

// markNeedsLogin stops and announces the server only if the failed operation
// still refers to its current credential (and, for HTTP, its current instance).
func (p *Pool) markNeedsLogin(expected mcpauth.UpstreamAuth, inst *instance, why string) {
	name := expected.Server
	p.mu.Lock()
	if p.closed || inst != nil && p.instances[instanceKey{inst.cfg.Name, inst.key}] != inst || !p.invalidateSignIn(expected, why) {
		p.mu.Unlock()
		return
	}
	st := p.state(name)
	st.auth = api.MCPAuthState{State: api.MCPAuthStateNeedsLogin}
	st.authLoaded = true
	var stop []*instance
	for _, i := range p.instances {
		if i.cfg.Name == name {
			stop = append(stop, i)
			p.removeInstance(i)
		}
	}
	p.mu.Unlock()
	st.log.printf("needs a new sign-in: %s", why)
	p.stopAll(stop, "needs a new sign-in")
	if len(stop) == 0 {
		p.changed(name)
	}
	p.toolsChanged()
}

// Disconnect forgets omo's sign-in at server name, revoking its token at
// the authorization server when that offers revocation (best effort).
func (p *Pool) Disconnect(ctx context.Context, name string) error {
	if _, ok := p.config(name); !ok {
		return fmt.Errorf("there is no MCP server %s", name)
	}
	a, ok, err := p.store.Load(ctx, name)
	if err != nil {
		return fmt.Errorf("reading the sign-in of server %s: %w", name, err)
	}
	if ok {
		p.mu.Lock()
		st := p.state(name)
		p.mu.Unlock()
		p.revoke(ctx, name, a, st.log.printf)
	}
	p.mu.Lock()
	err = p.store.Delete(ctx, name)
	p.mu.Unlock()
	if err != nil {
		return fmt.Errorf("deleting the sign-in of server %s: %w", name, err)
	}
	p.reset(name)
	p.mu.Lock()
	st := p.state(name)
	st.auth = api.MCPAuthState{State: api.MCPAuthStateNeedsLogin}
	st.authLoaded = true
	p.mu.Unlock()
	st.log.printf("disconnected")
	p.changed(name)
	p.toolsChanged()
	return nil
}

// revoke asks the authorization server to revoke the tokens of a (RFC
// 7009); failures are only logged with logf.
func (p *Pool) revoke(ctx context.Context, name string, a mcpauth.UpstreamAuth, logf func(string, ...any)) {
	var reg registration
	if json.Unmarshal(a.Registration, &reg) != nil || reg.RevocationURL == "" || a.Token == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transport := guardedTransport(reg.ServerURL)
	for _, tok := range []struct{ value, hint string }{{a.Token.RefreshToken, "refresh_token"}, {a.Token.AccessToken, "access_token"}} {
		if tok.value == "" {
			continue
		}
		form := url.Values{"token": {tok.value}, "token_type_hint": {tok.hint}, "client_id": {a.ClientID}}
		if a.ClientSecret != "" {
			form.Set("client_secret", a.ClientSecret)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reg.RevocationURL, strings.NewReader(form.Encode()))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := transport.RoundTrip(req)
		if err != nil {
			r := &redactor{}
			r.add(a.ClientSecret, a.Token.AccessToken, a.Token.RefreshToken)
			logf("revoking the sign-in of %s failed: %s", name, shortReason(r, err))
			return
		}
		drain(resp)
		if resp.StatusCode != http.StatusOK {
			logf("revoking the sign-in of %s failed: status %d", name, resp.StatusCode)
		}
	}
}
