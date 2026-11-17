package mcpauth

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// clock is a settable time source for tests.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func newTestStore(t *testing.T) (*Store, *clock, *sql.DB) {
	t.Helper()
	d := openDB(t)
	c := newClock()
	return NewStore(d, c.now), c, d
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *api.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

var secretForm = regexp.MustCompile(`^omo_(pat|at|rt)_[A-Za-z0-9_-]{43}$`)

func TestPATCreateVerifyRevoke(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	tok, secret, err := s.CreatePAT(ctx, "  laptop  ", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !secretForm.MatchString(secret) || !strings.HasPrefix(secret, PATPrefix) {
		t.Fatalf("secret %q has the wrong form", secret)
	}
	if tok.Name != "laptop" || tok.Kind != api.MCPTokenPAT || tok.ExpiresAt != nil || len(tok.ID) != 16 {
		t.Fatalf("token = %+v", tok)
	}
	g, err := s.Verify(ctx, secret, "http://anything/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if g.ID != tok.ID || g.Kind != api.MCPTokenPAT || g.Name != "laptop" {
		t.Fatalf("grant = %+v", g)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].ID != tok.ID || list[0].LastUsedAt == nil {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := s.Revoke(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.Verify(ctx, secret, "")
	wantCode(t, err, api.CodeUnauthorized)
	wantCode(t, s.Revoke(ctx, tok.ID), api.CodeNotFound)
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Fatalf("a revoked token is listed: %+v", list)
	}
}

func TestPATExpiry(t *testing.T) {
	s, c, _ := newTestStore(t)
	ctx := context.Background()
	past := c.now().Add(-time.Second)
	if _, _, err := s.CreatePAT(ctx, "x", &past); err == nil {
		t.Fatal("a token expiring in the past was created")
	}
	exp := c.now().Add(time.Hour)
	tok, secret, err := s.CreatePAT(ctx, "ci", &exp)
	if err != nil {
		t.Fatal(err)
	}
	if tok.ExpiresAt == nil || !tok.ExpiresAt.Equal(exp) {
		t.Fatalf("expires = %v", tok.ExpiresAt)
	}
	if _, err := s.Verify(ctx, secret, ""); err != nil {
		t.Fatal(err)
	}
	c.add(time.Hour)
	_, err = s.Verify(ctx, secret, "")
	wantCode(t, err, api.CodeUnauthorized)
	// An expired token is still listed, so the user sees it ended, until
	// 30 days later.
	if list, _ := s.List(ctx); len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	c.add(keepEnded + time.Second)
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Fatalf("a long expired token is still listed: %+v", list)
	}
}

func TestPATNameRules(t *testing.T) {
	s, _, _ := newTestStore(t)
	for _, name := range []string{"", "   ", strings.Repeat("x", 101), "a\x1b[31mb", "a‮b"} {
		_, _, err := s.CreatePAT(context.Background(), name, nil)
		wantCode(t, err, api.CodeValidation)
	}
}

func TestVerifyRefusesOtherForms(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	_, secret, err := s.CreatePAT(ctx, "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{
		"", "0123456789abcdef0123456789abcdef", "omo_pat_", secret + "x",
		// The PAT's secret presented as if it were an access token.
		AccessPrefix + strings.TrimPrefix(secret, PATPrefix),
	} {
		_, err := s.Verify(ctx, tok, "")
		wantCode(t, err, api.CodeUnauthorized)
		if strings.Contains(err.Error(), tok) && tok != "" {
			t.Fatalf("the refusal names the token: %v", err)
		}
	}
}

// TestSecretsStoredHashed reads every column of the token table: no secret
// is stored, only its SHA-256.
func TestSecretsStoredHashed(t *testing.T) {
	s, _, d := newTestStore(t)
	ctx := context.Background()
	_, pat, err := s.CreatePAT(ctx, "x", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.createGrant(ctx, oauthGrant{ClientID: "c", ClientName: "n", Resource: "http://localhost:1/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	q, err := s.refresh(ctx, p.Refresh, "c", "")
	if err != nil {
		t.Fatal(err)
	}
	dump := dumpTable(t, d, "mcp_tokens")
	for _, secret := range []string{pat, p.Access, p.Refresh, q.Access, q.Refresh} {
		body := secret[strings.LastIndex(secret, "_")+1:]
		if strings.Contains(dump, body) {
			t.Fatalf("a secret is stored as it is")
		}
	}
	for _, secret := range []string{pat, q.Access, q.Refresh, p.Refresh} {
		if !strings.Contains(dump, hash(secret)) {
			t.Fatalf("the hash of a secret is not stored")
		}
	}
}

func dumpTable(t *testing.T, d *sql.DB, table string) string {
	t.Helper()
	rows, err := d.Query(`SELECT * FROM ` + table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	var b strings.Builder
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			b.WriteString(v.String + "|")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestOAuthAccessTokenBoundToResource(t *testing.T) {
	s, c, _ := newTestStore(t)
	ctx := context.Background()
	p, err := s.createGrant(ctx, oauthGrant{ClientID: "c", ClientName: "Claude", Resource: "http://localhost:8090/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.Verify(ctx, p.Access, "http://localhost:8090/mcp")
	if err != nil || g.Kind != api.MCPTokenOAuth || g.Name != "Claude" || g.ExpiresAt == nil {
		t.Fatalf("grant = %+v, %v", g, err)
	}
	_, err = s.Verify(ctx, p.Access, "http://127.0.0.1:8090/mcp")
	wantCode(t, err, api.CodeUnauthorized)
	// The refresh token is no bearer.
	_, err = s.Verify(ctx, p.Refresh, "http://localhost:8090/mcp")
	wantCode(t, err, api.CodeUnauthorized)
	c.add(AccessLifetime)
	_, err = s.Verify(ctx, p.Access, "http://localhost:8090/mcp")
	wantCode(t, err, api.CodeUnauthorized)
}

func TestRefreshRotatesAndDetectsReuse(t *testing.T) {
	s, c, _ := newTestStore(t)
	ctx := context.Background()
	res := "http://localhost:8090/mcp"
	p, err := s.createGrant(ctx, oauthGrant{ClientID: "c", ClientName: "n", Resource: res})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.refresh(ctx, p.Refresh, "other", ""); !errors.Is(err, errInvalidGrant) {
		t.Fatalf("refresh by another client: %v", err)
	}
	if _, err := s.refresh(ctx, p.Refresh, "", "http://127.0.0.1:8090/mcp"); !errors.Is(err, errInvalidGrant) {
		t.Fatalf("refresh for another resource: %v", err)
	}
	c.add(time.Minute)
	q, err := s.refresh(ctx, p.Refresh, "c", res)
	if err != nil {
		t.Fatal(err)
	}
	if q.Access == p.Access || q.Refresh == p.Refresh {
		t.Fatal("the tokens were not rotated")
	}
	if _, err := s.Verify(ctx, p.Access, res); err == nil {
		t.Fatal("the old access token is still accepted")
	}
	if _, err := s.Verify(ctx, q.Access, res); err != nil {
		t.Fatal(err)
	}
	// The old refresh token again: the grant is revoked.
	_, err = s.refresh(ctx, p.Refresh, "c", "")
	var reused errReused
	if !errors.As(err, &reused) || reused.grant == "" {
		t.Fatalf("reuse: %v", err)
	}
	if _, err := s.Verify(ctx, q.Access, res); err == nil {
		t.Fatal("the grant's access token is accepted after reuse")
	}
	if _, err := s.refresh(ctx, q.Refresh, "c", ""); !errors.Is(err, errInvalidGrant) {
		t.Fatalf("refresh of a revoked grant: %v", err)
	}
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Fatalf("a revoked grant is listed: %+v", list)
	}
}

func TestRefreshExpiresAndSlides(t *testing.T) {
	s, c, _ := newTestStore(t)
	ctx := context.Background()
	p, err := s.createGrant(ctx, oauthGrant{ClientID: "c", ClientName: "n", Resource: "r"})
	if err != nil {
		t.Fatal(err)
	}
	c.add(RefreshLifetime - time.Hour)
	q, err := s.refresh(ctx, p.Refresh, "", "")
	if err != nil {
		t.Fatalf("refresh within the lifetime: %v", err)
	}
	list, _ := s.List(ctx)
	if len(list) != 1 || list[0].RefreshExpiresAt == nil || !list[0].RefreshExpiresAt.Equal(c.now().Add(RefreshLifetime)) {
		t.Fatalf("list = %+v", list)
	}
	c.add(RefreshLifetime)
	if _, err := s.refresh(ctx, q.Refresh, "", ""); !errors.Is(err, errInvalidGrant) {
		t.Fatalf("refresh after the lifetime: %v", err)
	}
	if list, _ := s.List(ctx); len(list) != 0 {
		t.Fatalf("an ended grant is listed: %+v", list)
	}
}

func TestRevokeTokenByClient(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	p, err := s.createGrant(ctx, oauthGrant{ClientID: "c", ClientName: "n", Resource: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.revokeToken(ctx, p.Refresh, "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, p.Access, "r"); err != nil {
		t.Fatal("another client revoked the grant")
	}
	if err := s.revokeToken(ctx, p.Refresh, "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, p.Access, "r"); err == nil {
		t.Fatal("the grant survived its revocation")
	}
	if err := s.revokeToken(ctx, "omo_rt_unknown", ""); err != nil {
		t.Fatal(err)
	}
}

func TestListShowsGrantDetails(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := s.createGrant(ctx, oauthGrant{ClientID: "https://c.example/doc", ClientName: "Codex", RedirectHost: "localhost:1455", Resource: "http://localhost:8090/mcp"}); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	g := list[0]
	if g.Kind != api.MCPTokenOAuth || g.Name != "Codex" || g.ClientID != "https://c.example/doc" || g.RedirectHost != "localhost:1455" ||
		g.Resource != "http://localhost:8090/mcp" || g.Scope != Scope || g.RefreshExpiresAt == nil || g.ExpiresAt != nil {
		t.Fatalf("grant = %+v", g)
	}
}

func TestClientKeyIsStable(t *testing.T) {
	s, _, _ := newTestStore(t)
	a, err := s.ClientKey(context.Background())
	if err != nil || len(a) != 32 {
		t.Fatal(a, err)
	}
	b, err := s.ClientKey(context.Background())
	if err != nil || string(a) != string(b) {
		t.Fatal("the client key changed")
	}
}
