package mcpauth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// Paths of the authorization server below its issuer, and of the MCP
// endpoint it protects.
const (
	PathResourceMetadata = "/.well-known/oauth-protected-resource"
	PathServerMetadata   = "/.well-known/oauth-authorization-server"
	PathAuthorize        = "/oauth/authorize"
	PathToken            = "/oauth/token"
	PathRegister         = "/oauth/register"
	PathRevoke           = "/oauth/revoke"
	PathMCP              = "/mcp"
)

// codeLifetime is how long an authorization code may be exchanged.
const codeLifetime = 60 * time.Second

// maxCodes bounds the codes waiting for their exchange.
const maxCodes = 1000

// maxFormBody bounds the bodies of the token, revocation and registration
// endpoints.
const maxFormBody = 64 << 10

// Limits of a registration.
const (
	maxRedirects      = 10
	maxRedirectLength = 512
)

// dcrPrefix starts the client id of a client registered at
// /oauth/register.
const dcrPrefix = "omo_dcr_"

// ResourceMetadata is the protected resource metadata (RFC 9728) of omo's
// MCP endpoint.
type ResourceMetadata struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	ScopesSupported        []string `json:"scopes_supported"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ResourceName           string   `json:"resource_name"`
}

// ProtectedResource returns the resource metadata for the base address
// ("http://localhost:8090", no trailing slash) a request reached omo by.
func ProtectedResource(base string) ResourceMetadata {
	return ResourceMetadata{
		Resource:               base + PathMCP,
		AuthorizationServers:   []string{base},
		ScopesSupported:        []string{Scope},
		BearerMethodsSupported: []string{"header"},
		ResourceName:           "omo",
	}
}

// ServerMetadata is the authorization server metadata (RFC 8414).
type ServerMetadata struct {
	Issuer                                     string   `json:"issuer"`
	AuthorizationEndpoint                      string   `json:"authorization_endpoint"`
	TokenEndpoint                              string   `json:"token_endpoint"`
	RegistrationEndpoint                       string   `json:"registration_endpoint"`
	RevocationEndpoint                         string   `json:"revocation_endpoint"`
	ResponseTypesSupported                     []string `json:"response_types_supported"`
	GrantTypesSupported                        []string `json:"grant_types_supported"`
	CodeChallengeMethodsSupported              []string `json:"code_challenge_methods_supported"`
	TokenEndpointAuthMethodsSupported          []string `json:"token_endpoint_auth_methods_supported"`
	RevocationEndpointAuthMethodsSupported     []string `json:"revocation_endpoint_auth_methods_supported"`
	ScopesSupported                            []string `json:"scopes_supported"`
	ClientIDMetadataDocumentSupported          bool     `json:"client_id_metadata_document_supported"`
	AuthorizationResponseIssParameterSupported bool     `json:"authorization_response_iss_parameter_supported"`
}

// AuthorizationServer returns the authorization server metadata of issuer
// (a base address without trailing slash).
func AuthorizationServer(issuer string) ServerMetadata {
	return ServerMetadata{
		Issuer:                                     issuer,
		AuthorizationEndpoint:                      issuer + PathAuthorize,
		TokenEndpoint:                              issuer + PathToken,
		RegistrationEndpoint:                       issuer + PathRegister,
		RevocationEndpoint:                         issuer + PathRevoke,
		ResponseTypesSupported:                     []string{"code"},
		GrantTypesSupported:                        []string{"authorization_code", "refresh_token"},
		CodeChallengeMethodsSupported:              []string{"S256"},
		TokenEndpointAuthMethodsSupported:          []string{"none"},
		RevocationEndpointAuthMethodsSupported:     []string{"none"},
		ScopesSupported:                            []string{Scope},
		ClientIDMetadataDocumentSupported:          true,
		AuthorizationResponseIssParameterSupported: true,
	}
}

// AS is omo's OAuth authorization server for its MCP endpoint. Codes live
// in memory only; grants are in the Store.
type AS struct {
	store *Store
	fetch *http.Client
	log   *log.Logger

	keyMu sync.Mutex
	key   []byte

	mu    sync.Mutex
	codes map[string]pendingCode
	docs  map[string]cachedDocument
}

// pendingCode is an authorization code waiting for its exchange.
type pendingCode struct {
	client    client
	redirect  string
	challenge string
	resource  string
	expires   time.Time
}

// NewAS returns the authorization server on s. fetch reads client ID
// metadata documents; nil is SafeClient(). l nil logs nothing.
func NewAS(s *Store, fetch *http.Client, l *log.Logger) *AS {
	if fetch == nil {
		fetch = SafeClient()
	}
	if l == nil {
		l = log.New(io.Discard, "", 0)
	}
	return &AS{store: s, fetch: fetch, log: l, codes: map[string]pendingCode{}, docs: map[string]cachedDocument{}}
}

func (a *AS) clientKey(ctx context.Context) ([]byte, error) {
	a.keyMu.Lock()
	defer a.keyMu.Unlock()
	if a.key == nil {
		k, err := a.store.ClientKey(ctx)
		if err != nil {
			return nil, err
		}
		a.key = k
	}
	return a.key, nil
}

// client is an OAuth client: registered at /oauth/register or described
// by its client ID metadata document.
type client struct {
	ID        string
	Name      string
	Kind      string // api.OAuthClientRegistered or api.OAuthClientCIMD
	Redirects []string
}

// registration is the signed content of a registered client's id.
type registration struct {
	Name      string   `json:"n"`
	Redirects []string `json:"r"`
	Issued    int64    `json:"t"`
}

// Register serves POST /oauth/register (RFC 7591) for public clients. It
// stores nothing: the client id carries the registration, signed.
func (a *AS) Register() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RedirectURIs            []string `json:"redirect_uris"`
			ClientName              string   `json:"client_name"`
			TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
			GrantTypes              []string `json:"grant_types"`
			ResponseTypes           []string `json:"response_types"`
			Scope                   string   `json:"scope"`
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFormBody))
		if err != nil || json.Unmarshal(body, &req) != nil {
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "the registration must be a JSON object")
			return
		}
		switch {
		case req.TokenEndpointAuthMethod != "" && req.TokenEndpointAuthMethod != "none":
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "omo registers public clients only (token_endpoint_auth_method none)")
			return
		case !subset(req.GrantTypes, "authorization_code", "refresh_token"):
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "omo grants authorization_code and refresh_token only")
			return
		case !subset(req.ResponseTypes, "code"):
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "omo answers response type code only")
			return
		case !subset(strings.Fields(req.Scope), Scope):
			oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "omo grants the scope mcp only")
			return
		case len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > maxRedirects:
			oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", fmt.Sprintf("register 1 to %d redirect URIs", maxRedirects))
			return
		}
		for _, u := range req.RedirectURIs {
			if len(u) > maxRedirectLength || !validRedirect(u) {
				oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "a redirect URI must be http://localhost, http://127.0.0.1, http://[::1] or https, without a fragment")
				return
			}
		}
		key, err := a.clientKey(r.Context())
		if err != nil {
			a.log.Printf("MCP OAuth registration: %v", err)
			oauthError(w, http.StatusInternalServerError, "server_error", "omo could not register the client")
			return
		}
		name := cleanName(req.ClientName)
		if name == "" {
			name = "unnamed client"
		}
		issued := a.store.clock()
		payload, _ := json.Marshal(registration{Name: name, Redirects: req.RedirectURIs, Issued: issued.Unix()})
		id := dcrPrefix + b64(payload) + "." + b64(sign(key, payload))
		writeOAuthJSON(w, http.StatusCreated, map[string]any{
			"client_id":                  id,
			"client_id_issued_at":        issued.Unix(),
			"client_name":                name,
			"redirect_uris":              req.RedirectURIs,
			"token_endpoint_auth_method": "none",
			"grant_types":                []string{"authorization_code", "refresh_token"},
			"response_types":             []string{"code"},
			"scope":                      Scope,
		})
	})
}

// registered reads the client id of a registered client.
func (a *AS) registered(ctx context.Context, id string) (client, error) {
	refused := api.Errorf(api.CodeBadRequest, "the client id is not one omo registered")
	payload64, mac64, ok := strings.Cut(strings.TrimPrefix(id, dcrPrefix), ".")
	if !ok {
		return client{}, refused
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(payload64)
	mac, err2 := base64.RawURLEncoding.DecodeString(mac64)
	if err1 != nil || err2 != nil {
		return client{}, refused
	}
	key, err := a.clientKey(ctx)
	if err != nil {
		return client{}, err
	}
	if !hmac.Equal(mac, sign(key, payload)) {
		return client{}, refused
	}
	var reg registration
	if err := json.Unmarshal(payload, &reg); err != nil {
		return client{}, refused
	}
	return client{ID: id, Name: reg.Name, Kind: api.OAuthClientRegistered, Redirects: reg.Redirects}, nil
}

// client resolves a client id: a registered client or a client ID
// metadata document.
func (a *AS) client(ctx context.Context, id string) (client, error) {
	switch {
	case strings.HasPrefix(id, dcrPrefix):
		return a.registered(ctx, id)
	case strings.HasPrefix(id, "https://"):
		return a.document(ctx, id)
	case id == "":
		return client{}, api.Errorf(api.CodeBadRequest, "the request names no client_id")
	}
	return client{}, api.Errorf(api.CodeBadRequest, "the client is unknown: register it, or name the https address of its client ID metadata document")
}

// authRequest is a checked authorization request.
type authRequest struct {
	client    client
	redirect  string
	state     string
	challenge string
	resource  string
}

var s256Challenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// check validates an authorization request. issuer is the base address the
// request reached omo by; resources are the addresses of omo's MCP
// endpoint a token may be issued for. A refusal is shown on the consent
// page; the client is never redirected to with it, since its redirect is
// not trusted before it is checked.
func (a *AS) check(ctx context.Context, q url.Values, issuer string, resources []string) (authRequest, error) {
	bad := func(format string, args ...any) (authRequest, error) {
		return authRequest{}, api.Errorf(api.CodeBadRequest, format, args...)
	}
	c, err := a.client(ctx, q.Get("client_id"))
	if err != nil {
		return authRequest{}, err
	}
	req := authRequest{client: c, redirect: q.Get("redirect_uri"), state: q.Get("state"), challenge: q.Get("code_challenge"), resource: q.Get("resource")}
	switch {
	case req.redirect == "":
		return bad("the request names no redirect_uri")
	case !slices.Contains(c.Redirects, req.redirect):
		return bad("the redirect_uri is not one the client registered")
	case q.Get("response_type") != "code":
		return bad("omo answers response_type code only")
	case q.Get("code_challenge_method") != "S256":
		return bad("omo requires PKCE with code_challenge_method S256")
	case !s256Challenge.MatchString(req.challenge):
		return bad("the code_challenge is not an S256 challenge")
	case !subset(strings.Fields(q.Get("scope")), Scope):
		return bad("omo grants the scope mcp only")
	}
	if req.resource == "" {
		req.resource = issuer + PathMCP
	}
	if !slices.Contains(resources, req.resource) {
		return bad("the resource is not omo's MCP endpoint at this address")
	}
	return req, nil
}

// Consent checks the query of an authorization request (GET
// /oauth/authorize) and says what the consent page shows.
func (a *AS) Consent(ctx context.Context, q url.Values, issuer string, resources []string) (api.OAuthConsent, error) {
	req, err := a.check(ctx, q, issuer, resources)
	if err != nil {
		return api.OAuthConsent{}, err
	}
	u, _ := url.Parse(req.redirect)
	return api.OAuthConsent{
		ClientName:        req.client.Name,
		ClientID:          req.client.ID,
		ClientKind:        req.client.Kind,
		RedirectHost:      u.Host,
		LocalhostRedirect: loopbackRedirect(req.redirect),
		Resource:          req.resource,
	}, nil
}

// Decide applies the user's answer to an authorization request and returns
// the client's redirect: with a code on approval, with error access_denied
// otherwise, and with state and iss either way.
func (a *AS) Decide(ctx context.Context, d api.OAuthDecision, issuer string, resources []string) (string, error) {
	q, err := url.ParseQuery(strings.TrimPrefix(d.Query, "?"))
	if err != nil {
		return "", api.Errorf(api.CodeBadRequest, "the query of the authorization request is malformed")
	}
	req, err := a.check(ctx, q, issuer, resources)
	if err != nil {
		return "", err
	}
	u, _ := url.Parse(req.redirect)
	v := u.Query()
	if d.Approve {
		code := newSecret("")
		now := a.store.clock()
		a.mu.Lock()
		for k, c := range a.codes {
			if !now.Before(c.expires) {
				delete(a.codes, k)
			}
		}
		if len(a.codes) >= maxCodes {
			a.mu.Unlock()
			return "", api.Errorf(api.CodeUnavailable, "too many sign-ins are waiting; try again in a minute")
		}
		a.codes[code] = pendingCode{client: req.client, redirect: req.redirect, challenge: req.challenge, resource: req.resource, expires: now.Add(codeLifetime)}
		a.mu.Unlock()
		v.Set("code", code)
	} else {
		v.Set("error", "access_denied")
		v.Set("error_description", "the user denied the access")
	}
	if req.state != "" {
		v.Set("state", req.state)
	}
	v.Set("iss", issuer)
	u.RawQuery = v.Encode()
	return u.String(), nil
}

// takeCode removes a code and returns it when it has not expired.
func (a *AS) takeCode(code string) (pendingCode, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.codes[code]
	delete(a.codes, code)
	return c, ok && a.store.clock().Before(c.expires)
}

var codeVerifier = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// Token serves POST /oauth/token: grant types authorization_code (with
// PKCE S256) and refresh_token (rotating; a reused refresh token revokes
// the grant).
func (a *AS) Token() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		form, ok := readForm(w, r)
		if !ok {
			return
		}
		var (
			pair tokenPair
			err  error
		)
		switch form.Get("grant_type") {
		case "authorization_code":
			c, ok := a.takeCode(form.Get("code"))
			switch {
			case !ok:
				oauthError(w, http.StatusBadRequest, "invalid_grant", "the code is unknown, used or expired")
				return
			case form.Get("client_id") != c.client.ID:
				oauthError(w, http.StatusBadRequest, "invalid_grant", "the code was issued to another client")
				return
			case form.Get("redirect_uri") != c.redirect:
				oauthError(w, http.StatusBadRequest, "invalid_grant", "the redirect_uri differs from the authorization request")
				return
			case !pkceMatches(form.Get("code_verifier"), c.challenge):
				oauthError(w, http.StatusBadRequest, "invalid_grant", "the code_verifier does not match the code_challenge")
				return
			case form.Get("resource") != "" && form.Get("resource") != c.resource:
				oauthError(w, http.StatusBadRequest, "invalid_target", "the resource differs from the authorization request")
				return
			}
			u, _ := url.Parse(c.redirect)
			pair, err = a.store.createGrant(r.Context(), oauthGrant{ClientID: c.client.ID, ClientName: c.client.Name, RedirectHost: u.Host, Resource: c.resource})
		case "refresh_token":
			if !subset(strings.Fields(form.Get("scope")), Scope) {
				oauthError(w, http.StatusBadRequest, "invalid_scope", "omo grants the scope mcp only")
				return
			}
			pair, err = a.store.refresh(r.Context(), form.Get("refresh_token"), form.Get("client_id"), form.Get("resource"))
			var reused errReused
			switch {
			case errors.As(err, &reused):
				a.log.Printf("MCP OAuth: a refresh token of grant %s was used a second time; the grant is revoked", reused.grant)
				oauthError(w, http.StatusBadRequest, "invalid_grant", reused.Error())
				return
			case errors.Is(err, errInvalidGrant):
				oauthError(w, http.StatusBadRequest, "invalid_grant", err.Error())
				return
			}
		default:
			oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "omo grants authorization_code and refresh_token only")
			return
		}
		if err != nil {
			a.log.Printf("MCP OAuth token endpoint: %v", err)
			oauthError(w, http.StatusInternalServerError, "server_error", "omo could not issue the token")
			return
		}
		writeOAuthJSON(w, http.StatusOK, map[string]any{
			"access_token":  pair.Access,
			"token_type":    "Bearer",
			"expires_in":    int(AccessLifetime / time.Second),
			"refresh_token": pair.Refresh,
			"scope":         Scope,
		})
	})
}

// Revoke serves POST /oauth/revoke (RFC 7009): an access or refresh token
// revokes its whole grant. Unknown tokens are answered as revoked.
func (a *AS) Revoke() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		form, ok := readForm(w, r)
		if !ok {
			return
		}
		token := form.Get("token")
		if token == "" {
			oauthError(w, http.StatusBadRequest, "invalid_request", "the request names no token")
			return
		}
		if err := a.store.revokeToken(r.Context(), token, form.Get("client_id")); err != nil {
			a.log.Printf("MCP OAuth revocation: %v", err)
			oauthError(w, http.StatusInternalServerError, "server_error", "omo could not revoke the token")
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

// readForm reads an application/x-www-form-urlencoded POST body.
func readForm(w http.ResponseWriter, r *http.Request) (url.Values, bool) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/x-www-form-urlencoded" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "the body must be application/x-www-form-urlencoded")
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "the body is malformed or too large")
		return nil, false
	}
	return r.PostForm, true
}

func pkceMatches(verifier, challenge string) bool {
	if !codeVerifier.MatchString(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(b64(sum[:])), []byte(challenge)) == 1
}

// validRedirect accepts http://localhost, http://127.0.0.1 and http://[::1]
// on any port, and https addresses with a host; none with a fragment or
// credentials.
func validRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || strings.Contains(raw, "#") || u.User != nil || u.Opaque != "" || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return loopbackRedirect(raw)
	}
	return false
}

// loopbackRedirect reports whether raw is an http address on this machine.
func loopbackRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" {
		return false
	}
	h := u.Hostname()
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	writeOAuthJSON(w, status, map[string]string{"error": code, "error_description": description})
}

func writeOAuthJSON(w http.ResponseWriter, status int, v any) {
	data, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	w.Write(data)
}

// subset reports whether every value is one of allowed.
func subset(values []string, allowed ...string) bool {
	for _, v := range values {
		if !slices.Contains(allowed, v) {
			return false
		}
	}
	return true
}

func sign(key, payload []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(payload)
	return m.Sum(nil)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
