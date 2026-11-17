package mcpauth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// UpstreamAuth is omo's sign-in at an upstream MCP server with auth oauth:
// the client omo registered or was given, and the tokens it holds. The
// values are stored as they are (the owner's rule for secrets).
type UpstreamAuth struct {
	Server       string
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// State is one of api.MCPAuthState's values; Error says why it is
	// error.
	State string
	Error string
	// Registration is the authorization server's answer to the client
	// registration, as JSON.
	Registration json.RawMessage
	// Token is nil when omo holds no token.
	Token  *oauth2.Token
	Scopes []string
}

// UpstreamStore keeps one UpstreamAuth per relayed server.
type UpstreamStore interface {
	// Load returns the sign-in of server; ok is false when there is none.
	Load(ctx context.Context, server string) (UpstreamAuth, bool, error)
	// Save stores a, replacing the server's earlier sign-in.
	Save(ctx context.Context, a UpstreamAuth) error
	// Delete removes the server's sign-in; an unknown server is no error.
	Delete(ctx context.Context, server string) error
}

// Upstream is the UpstreamStore on omo's database.
type Upstream struct {
	db  *sql.DB
	now func() time.Time
}

var _ UpstreamStore = (*Upstream)(nil)

// NewUpstream returns the upstream sign-in store on db; now nil is
// time.Now.
func NewUpstream(db *sql.DB, now func() time.Time) *Upstream {
	if now == nil {
		now = time.Now
	}
	return &Upstream{db: db, now: now}
}

// Load implements UpstreamStore.
func (u *Upstream) Load(ctx context.Context, server string) (UpstreamAuth, bool, error) {
	var (
		a                          UpstreamAuth
		registration, scopes       string
		access, refresh, tokenType string
		expiry                     sql.NullString
	)
	err := u.db.QueryRowContext(ctx, `SELECT server, issuer, client_id, client_secret, registration, redirect_uri,
access_token, refresh_token, token_type, expiry, scopes, state, error FROM mcp_upstream_auth WHERE server = ?`, server).Scan(
		&a.Server, &a.Issuer, &a.ClientID, &a.ClientSecret, &registration, &a.RedirectURI,
		&access, &refresh, &tokenType, &expiry, &scopes, &a.State, &a.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return UpstreamAuth{}, false, nil
	}
	if err != nil {
		return UpstreamAuth{}, false, fmt.Errorf("read the sign-in of MCP server %s: %w", server, err)
	}
	a.Registration = json.RawMessage(registration)
	if scopes != "" {
		a.Scopes = strings.Fields(scopes)
	}
	if access != "" || refresh != "" {
		a.Token = &oauth2.Token{AccessToken: access, RefreshToken: refresh, TokenType: tokenType}
		if e := parseTime(expiry); e != nil {
			a.Token.Expiry = *e
		}
	}
	return a, true, nil
}

// Save implements UpstreamStore.
func (u *Upstream) Save(ctx context.Context, a UpstreamAuth) error {
	if a.Server == "" {
		return errors.New("the sign-in names no server")
	}
	registration := "{}"
	if len(a.Registration) > 0 {
		if !json.Valid(a.Registration) {
			return fmt.Errorf("the registration of MCP server %s is not JSON", a.Server)
		}
		registration = string(a.Registration)
	}
	var (
		access, refresh string
		tokenType       = "Bearer"
		expiry          any
	)
	if t := a.Token; t != nil {
		access, refresh = t.AccessToken, t.RefreshToken
		if t.TokenType != "" {
			tokenType = t.TokenType
		}
		if !t.Expiry.IsZero() {
			expiry = formatTime(t.Expiry)
		}
	}
	_, err := u.db.ExecContext(ctx, `INSERT INTO mcp_upstream_auth (server, issuer, client_id, client_secret, registration,
redirect_uri, access_token, refresh_token, token_type, expiry, scopes, state, error, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (server) DO UPDATE SET issuer = excluded.issuer, client_id = excluded.client_id,
client_secret = excluded.client_secret, registration = excluded.registration, redirect_uri = excluded.redirect_uri,
access_token = excluded.access_token, refresh_token = excluded.refresh_token, token_type = excluded.token_type,
expiry = excluded.expiry, scopes = excluded.scopes, state = excluded.state, error = excluded.error,
updated_at = excluded.updated_at`,
		a.Server, a.Issuer, a.ClientID, a.ClientSecret, registration, a.RedirectURI, access, refresh, tokenType,
		expiry, strings.Join(a.Scopes, " "), a.State, a.Error, formatTime(u.now()))
	if err != nil {
		return fmt.Errorf("store the sign-in of MCP server %s: %w", a.Server, err)
	}
	return nil
}

// Delete implements UpstreamStore.
func (u *Upstream) Delete(ctx context.Context, server string) error {
	if _, err := u.db.ExecContext(ctx, `DELETE FROM mcp_upstream_auth WHERE server = ?`, server); err != nil {
		return fmt.Errorf("remove the sign-in of MCP server %s: %w", server, err)
	}
	return nil
}
