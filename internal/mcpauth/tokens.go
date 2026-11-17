// Package mcpauth authorizes clients of omo's MCP endpoint that are not
// omo's own agent sessions: personal access tokens, and omo's small OAuth
// 2.1 authorization server (metadata, client registration, client ID
// metadata documents, consent through the web app, PKCE, rotating refresh
// tokens, revocation). It also keeps omo's own sign-ins at upstream MCP
// servers for the relay.
//
// A token or grant of this package never ties a connection to an agent
// session: it reaches only the relayed servers marked global, never omo's
// board tools. Inbound secrets are stored as SHA-256 hashes only; upstream
// tokens are stored as they are.
package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Prefixes of the secrets this package issues. Each is followed by 43
// characters of base64url (32 random bytes).
const (
	PATPrefix     = "omo_pat_"
	AccessPrefix  = "omo_at_"
	RefreshPrefix = "omo_rt_"
)

// Lifetimes of OAuth tokens. A refresh token's lifetime starts again at
// every refresh.
const (
	AccessLifetime  = time.Hour
	RefreshLifetime = 30 * 24 * time.Hour
)

// Scope is the only scope omo grants: the relayed servers marked global.
const Scope = "mcp"

// lastUsedEvery throttles the writes of last_used_at.
const lastUsedEvery = time.Minute

// keepEnded is how long a revoked or ended token stays in the table before
// List removes it.
const keepEnded = 30 * 24 * time.Hour

// maxNameLength bounds token and client names, in characters.
const maxNameLength = 100

// clientKeyState is the server_state key of the HMAC key that signs the
// client ids of registered clients.
const clientKeyState = "mcp_client_key"

// Grant is what a verified bearer stands for: a personal access token or an
// OAuth grant. Neither is tied to an agent session.
type Grant struct {
	// ID is the token's or grant's id, as GET /api/mcp/tokens lists it.
	ID string
	// Kind is api.MCPTokenPAT or api.MCPTokenOAuth.
	Kind string
	// Name is the token's name or the OAuth client's name.
	Name string
	// ExpiresAt is when the bearer stops being accepted; nil for a
	// personal access token without expiry.
	ExpiresAt *time.Time
}

// Store keeps the personal access tokens and OAuth grants in the database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore returns the token store on db; now nil is time.Now.
func NewStore(db *sql.DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

func (s *Store) clock() time.Time { return s.now().UTC() }

// unauthorized is the refusal of every bearer Verify does not accept. It
// never names the token.
func unauthorized(reason string) error {
	return api.Errorf(api.CodeUnauthorized, "the MCP token is %s", reason)
}

// Verify checks a bearer of the MCP endpoint: a personal access token, or
// an OAuth access token whose resource equals resource (the address of the
// endpoint the request reached, "<base>/mcp"). It answers an *api.Error
// with code unauthorized for every other token.
func (s *Store) Verify(ctx context.Context, token, resource string) (Grant, error) {
	var kind string
	switch {
	case strings.HasPrefix(token, PATPrefix):
		kind = api.MCPTokenPAT
	case strings.HasPrefix(token, AccessPrefix):
		kind = api.MCPTokenOAuth
	default:
		return Grant{}, unauthorized("unknown")
	}
	var (
		g                          Grant
		stored                     string
		expires, lastUsed, revoked sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `SELECT id, kind, name, resource, expires_at, last_used_at, revoked_at
FROM mcp_tokens WHERE token_hash = ?`, hash(token)).Scan(&g.ID, &g.Kind, &g.Name, &stored, &expires, &lastUsed, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, unauthorized("unknown")
	}
	if err != nil {
		return Grant{}, fmt.Errorf("read MCP token: %w", err)
	}
	now := s.clock()
	switch {
	case g.Kind != kind:
		return Grant{}, unauthorized("unknown")
	case revoked.Valid:
		return Grant{}, unauthorized("revoked")
	}
	g.ExpiresAt = parseTime(expires)
	if g.ExpiresAt != nil && !now.Before(*g.ExpiresAt) {
		return Grant{}, unauthorized("expired")
	}
	if kind == api.MCPTokenOAuth && stored != resource {
		return Grant{}, unauthorized("issued for another address of omo")
	}
	if last := parseTime(lastUsed); last == nil || now.Sub(*last) >= lastUsedEvery {
		if _, err := s.db.ExecContext(ctx, `UPDATE mcp_tokens SET last_used_at = ? WHERE id = ?`, formatTime(now), g.ID); err != nil {
			return Grant{}, fmt.Errorf("record MCP token use: %w", err)
		}
	}
	return g, nil
}

// CreatePAT creates a personal access token and returns it with its secret,
// which is not stored and cannot be read again. expires nil never expires.
func (s *Store) CreatePAT(ctx context.Context, name string, expires *time.Time) (api.MCPToken, string, error) {
	clean, err := checkName(name)
	if err != nil {
		return api.MCPToken{}, "", err
	}
	now := s.clock()
	if expires != nil && !expires.After(now) {
		return api.MCPToken{}, "", api.Errorf(api.CodeValidation, "the expiry must lie in the future")
	}
	secret := newSecret(PATPrefix)
	id := newID()
	var exp any
	if expires != nil {
		exp = formatTime(*expires)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO mcp_tokens (id, kind, name, token_hash, created_at, expires_at)
VALUES (?, 'pat', ?, ?, ?, ?)`, id, clean, hash(secret), formatTime(now), exp); err != nil {
		return api.MCPToken{}, "", fmt.Errorf("store MCP token: %w", err)
	}
	t := api.MCPToken{ID: id, Kind: api.MCPTokenPAT, Name: clean, Scope: Scope, CreatedAt: now}
	if expires != nil {
		e := expires.UTC()
		t.ExpiresAt = &e
	}
	return t, secret, nil
}

// List returns the personal access tokens and OAuth grants that are not
// revoked, oldest first; an OAuth grant whose refresh token ran out is
// left out. Tokens revoked or ended more than 30 days ago are removed.
func (s *Store) List(ctx context.Context) ([]api.MCPToken, error) {
	now := s.clock()
	before := formatTime(now.Add(-keepEnded))
	if _, err := s.db.ExecContext(ctx, `DELETE FROM mcp_tokens WHERE revoked_at < ?
OR (kind = 'oauth' AND refresh_expires_at < ?) OR (kind = 'pat' AND expires_at < ?)`, before, before, before); err != nil {
		return nil, fmt.Errorf("remove ended MCP tokens: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, name, client_id, redirect_host, resource, scope,
created_at, expires_at, refresh_expires_at, last_used_at
FROM mcp_tokens WHERE revoked_at IS NULL AND NOT (kind = 'oauth' AND refresh_expires_at <= ?)
ORDER BY created_at, id`, formatTime(now))
	if err != nil {
		return nil, fmt.Errorf("list MCP tokens: %w", err)
	}
	defer rows.Close()
	out := []api.MCPToken{}
	for rows.Next() {
		var (
			t                                 api.MCPToken
			created                           string
			expires, refreshExpires, lastUsed sql.NullString
		)
		if err := rows.Scan(&t.ID, &t.Kind, &t.Name, &t.ClientID, &t.RedirectHost, &t.Resource, &t.Scope,
			&created, &expires, &refreshExpires, &lastUsed); err != nil {
			return nil, fmt.Errorf("list MCP tokens: %w", err)
		}
		if c := parseTime(sql.NullString{String: created, Valid: true}); c != nil {
			t.CreatedAt = *c
		}
		if t.Kind == api.MCPTokenOAuth {
			// An access token lives an hour; the grant's end is its
			// refresh token's.
			t.RefreshExpiresAt = parseTime(refreshExpires)
		} else {
			t.ExpiresAt = parseTime(expires)
		}
		t.LastUsedAt = parseTime(lastUsed)
		out = append(out, t)
	}
	return out, rows.Err()
}

// Revoke ends a personal access token or an OAuth grant (its access and
// refresh tokens) at once. An unknown or already revoked id is not_found.
func (s *Store) Revoke(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE mcp_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, formatTime(s.clock()), id)
	if err != nil {
		return fmt.Errorf("revoke MCP token: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("revoke MCP token: %w", err)
	} else if n == 0 {
		return api.Errorf(api.CodeNotFound, "no MCP token or grant with this id")
	}
	return nil
}

// ClientKey returns the key that signs the client ids of registered OAuth
// clients, creating it on first use.
func (s *Store) ClientKey(ctx context.Context) ([]byte, error) {
	fresh := make([]byte, 32)
	rand.Read(fresh)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO server_state (key, value) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`,
		clientKeyState, hex.EncodeToString(fresh)); err != nil {
		return nil, fmt.Errorf("store the MCP client key: %w", err)
	}
	var v string
	if err := s.db.QueryRowContext(ctx, `SELECT value FROM server_state WHERE key = ?`, clientKeyState).Scan(&v); err != nil {
		return nil, fmt.Errorf("read the MCP client key: %w", err)
	}
	key, err := hex.DecodeString(v)
	if err != nil || len(key) != 32 {
		return nil, errors.New("the stored MCP client key is malformed")
	}
	return key, nil
}

// oauthGrant is what an approved authorization code turns into.
type oauthGrant struct {
	ClientID, ClientName, RedirectHost, Resource string
}

// tokenPair is an access token with its refresh token.
type tokenPair struct {
	Access, Refresh string
}

// errInvalidGrant is the refusal of a refresh token that is unknown,
// revoked, expired or belongs to another client or resource.
var errInvalidGrant = errors.New("the refresh token is unknown, expired or revoked")

// errReused says that a refresh token was used again after it was rotated:
// the grant is revoked.
type errReused struct{ grant string }

func (e errReused) Error() string { return "a refresh token was used again; the grant is revoked" }

// createGrant stores a new OAuth grant and returns its first tokens.
func (s *Store) createGrant(ctx context.Context, g oauthGrant) (tokenPair, error) {
	now := s.clock()
	p := tokenPair{Access: newSecret(AccessPrefix), Refresh: newSecret(RefreshPrefix)}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO mcp_tokens
(id, kind, name, client_id, redirect_host, resource, scope, token_hash, refresh_hash, created_at, expires_at, refresh_expires_at)
VALUES (?, 'oauth', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newID(), g.ClientName, g.ClientID, g.RedirectHost, g.Resource, Scope, hash(p.Access), hash(p.Refresh),
		formatTime(now), formatTime(now.Add(AccessLifetime)), formatTime(now.Add(RefreshLifetime))); err != nil {
		return tokenPair{}, fmt.Errorf("store OAuth grant: %w", err)
	}
	return p, nil
}

// refresh rotates the tokens of the grant refresh belongs to. clientID and
// resource, when not empty, must be the grant's. A refresh token that was
// already rotated revokes the grant (errReused).
func (s *Store) refresh(ctx context.Context, refresh, clientID, resource string) (tokenPair, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return tokenPair{}, err
	}
	defer tx.Rollback()
	now := s.clock()
	h := hash(refresh)
	var (
		id, client, res string
		refreshExpires  sql.NullString
		revoked         sql.NullString
	)
	err = tx.QueryRowContext(ctx, `SELECT id, client_id, resource, refresh_expires_at, revoked_at FROM mcp_tokens
WHERE kind = 'oauth' AND refresh_hash = ?`, h).Scan(&id, &client, &res, &refreshExpires, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRowContext(ctx, `SELECT id, revoked_at FROM mcp_tokens WHERE kind = 'oauth' AND prior_refresh_hash = ?`, h).Scan(&id, &revoked)
		if errors.Is(err, sql.ErrNoRows) {
			return tokenPair{}, errInvalidGrant
		}
		if err != nil {
			return tokenPair{}, fmt.Errorf("read OAuth grant: %w", err)
		}
		if revoked.Valid {
			return tokenPair{}, errInvalidGrant
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mcp_tokens SET revoked_at = ? WHERE id = ?`, formatTime(now), id); err != nil {
			return tokenPair{}, fmt.Errorf("revoke OAuth grant: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return tokenPair{}, err
		}
		return tokenPair{}, errReused{grant: id}
	}
	if err != nil {
		return tokenPair{}, fmt.Errorf("read OAuth grant: %w", err)
	}
	exp := parseTime(refreshExpires)
	switch {
	case revoked.Valid, exp == nil || !now.Before(*exp):
		return tokenPair{}, errInvalidGrant
	case clientID != "" && clientID != client, resource != "" && resource != res:
		return tokenPair{}, errInvalidGrant
	}
	p := tokenPair{Access: newSecret(AccessPrefix), Refresh: newSecret(RefreshPrefix)}
	if _, err := tx.ExecContext(ctx, `UPDATE mcp_tokens SET token_hash = ?, refresh_hash = ?, prior_refresh_hash = ?,
expires_at = ?, refresh_expires_at = ?, last_used_at = ? WHERE id = ?`,
		hash(p.Access), hash(p.Refresh), h, formatTime(now.Add(AccessLifetime)), formatTime(now.Add(RefreshLifetime)), formatTime(now), id); err != nil {
		return tokenPair{}, fmt.Errorf("rotate OAuth grant: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return tokenPair{}, err
	}
	return p, nil
}

// revokeToken revokes the OAuth grant an access or refresh token belongs to,
// when clientID is empty or the grant's (RFC 7009). An unknown token is no
// error.
func (s *Store) revokeToken(ctx context.Context, token, clientID string) error {
	h := hash(token)
	_, err := s.db.ExecContext(ctx, `UPDATE mcp_tokens SET revoked_at = ?
WHERE kind = 'oauth' AND revoked_at IS NULL AND (token_hash = ? OR refresh_hash = ?) AND (? = '' OR client_id = ?)`,
		formatTime(s.clock()), h, h, clientID, clientID)
	if err != nil {
		return fmt.Errorf("revoke OAuth grant: %w", err)
	}
	return nil
}

// checkName cleans a token or client name: hidden characters are refused
// for a token, trimmed and at most maxNameLength characters.
func checkName(name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", api.Errorf(api.CodeValidation, "the token needs a name")
	case utf8.RuneCountInString(name) > maxNameLength:
		return "", api.Errorf(api.CodeValidation, "the name is longer than %d characters", maxNameLength)
	case strings.ContainsFunc(name, textsafe.IsHidden):
		return "", api.Errorf(api.CodeValidation, "the name contains a hidden or control character")
	}
	return name, nil
}

// cleanName makes a name a client chose fit to show: hidden characters are
// dropped, the rest is trimmed and cut to maxNameLength characters.
func cleanName(name string) string {
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if textsafe.IsHidden(r) {
			return -1
		}
		return r
	}, name))
	if utf8.RuneCountInString(name) > maxNameLength {
		name = strings.TrimSpace(string([]rune(name)[:maxNameLength]))
	}
	return name
}

// newSecret returns prefix followed by 32 random bytes in base64url.
func newSecret(prefix string) string {
	b := make([]byte, 32)
	rand.Read(b)
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// newID returns 16 random hex characters.
func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// hash is the stored form of a secret.
func hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// timeLayout has a fixed width, so stored times compare as strings in SQL.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(v sql.NullString) *time.Time {
	if !v.Valid {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, v.String)
	if err != nil {
		return nil
	}
	return &t
}
