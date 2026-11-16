package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

func TestRegisterRoutesWrapsUserOnly(t *testing.T) {
	s := newTestServer(t)
	sess := liveSession(t, s, api.RoleDeveloper)
	before := len(s.routeTable)
	mux := http.NewServeMux()
	var calls []string
	handler := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			calls = append(calls, name)
			w.WriteHeader(http.StatusNoContent)
		}
	}
	s.registerRoutes(mux, []apiRoute{
		{"POST /api/test/user", handler("user"), userOnly},
		{"GET /api/test/read", handler("read"), agentRead},
	})
	if len(s.routeTable) != before+2 {
		t.Fatalf("route table has %d rows, want %d", len(s.routeTable), before+2)
	}
	serve := func(method, path, session string) int {
		req := withRequestContext(newRequest(t, method, "http://omo"+path, nil), board.User, s.log)
		if session != "" {
			actor, err := s.board.ResolveSession(req.Context(), session)
			if err != nil {
				t.Fatal(err)
			}
			req = withRequestContext(req, actor, s.log)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := serve("POST", "/api/test/user", sess.ID); got != http.StatusForbidden {
		t.Fatalf("agent on a user-only route: %d, want 403", got)
	}
	if len(calls) != 0 {
		t.Fatalf("the handler of a user-only route ran for an agent: %v", calls)
	}
	if got := serve("GET", "/api/test/read", sess.ID); got != http.StatusNoContent {
		t.Fatalf("agent on an agent-readable route: %d, want 204", got)
	}
	if got := serve("POST", "/api/test/user", ""); got != http.StatusNoContent {
		t.Fatalf("user on a user-only route: %d, want 204", got)
	}
	if !slices.Equal(calls, []string{"read", "user"}) {
		t.Fatalf("calls %v", calls)
	}
}

func TestRegisterRoutesRefusesRowsWithoutRule(t *testing.T) {
	s := newTestServer(t)
	ok := func(http.ResponseWriter, *http.Request) {}
	for _, row := range []apiRoute{
		{"GET /api/test/a", ok, 0},
		{"GET /api/test/b", ok, routeAccess(99)},
		{"POST /api/test/c", ok, agentRead},
		{"GET /api/test/f", ok, agentWrite},
		{"/api/test/d", ok, userOnly},
		{"GET /api/test/e", nil, userOnly},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("row %q (%s) was registered", row.pattern, row.access)
				}
			}()
			s.registerRoutes(http.NewServeMux(), []apiRoute{row})
		}()
	}
}

// guardedPrefixes are the API paths whose routes may only be registered
// through the route table, where each row states who may call it.
var guardedPrefixes = []string{
	"/api/settings", "/api/profiles", "/api/models", "/api/roles", "/api/prompts",
	"/api/projects", "/api/folders", "/api/agent-setup",
	"/api/usage", "/api/skillsets", "/api/plugins", "/api/images",
}

// guardedTicketRoutes are the routes below /api/tickets/{n} that must be in
// the route table too.
var guardedTicketRoutes = []string{"/api/tickets/{n}/images"}

// registeredDirectly lists the routes below the guarded prefixes that are
// still registered with mux.HandleFunc and check the caller in their
// handlers. Moving one into the route table means deleting it here; the
// guard fails while a listed route is no longer registered directly, so the
// list can only shrink.
var registeredDirectly = []string{}

func guarded(pattern string) bool {
	path := pattern
	if _, p, ok := strings.Cut(pattern, " "); ok {
		path = p
	}
	for _, prefix := range guardedPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	for _, prefix := range guardedTicketRoutes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// TestNoGuardedRouteOutsideTable reads the package's source: every call of
// HandleFunc or Handle outside registerRoutes must register a literal
// pattern (or one of reservedRoutes), and none of them may lie below a
// guarded prefix unless it is listed in registeredDirectly.
func TestNoGuardedRouteOutsideTable(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	direct := map[string]bool{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") || len(call.Args) == 0 {
					return true
				}
				where := fset.Position(call.Pos()).String()
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "http" {
					t.Errorf("%s: a route on http.DefaultServeMux", where)
					return true
				}
				if fn.Name.Name == "registerRoutes" {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					if fn.Name.Name == "routes" {
						// The reserved routes, checked below.
						return true
					}
					t.Errorf("%s: a route pattern that is not a literal cannot be checked; register it through registerRoutes", where)
					return true
				}
				pattern, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				if !guarded(pattern) {
					return true
				}
				if !slices.Contains(registeredDirectly, pattern) {
					t.Errorf("%s: %q is registered outside the route table; register it through registerRoutes with its access rule", where, pattern)
				}
				direct[pattern] = true
				return true
			})
		}
	}
	for _, p := range registeredDirectly {
		if !direct[p] {
			t.Errorf("%q is no longer registered directly: remove it from registeredDirectly", p)
		}
	}
	for _, p := range reservedRoutes {
		if guarded(p) {
			t.Errorf("reserved route %q lies below a guarded prefix", p)
		}
	}
}

// TestRouteTableRowsAreServed checks that each row of the table is what the
// mux answers with, so no other registration shadows it.
func TestRouteTableRowsAreServed(t *testing.T) {
	s := newTestServer(t)
	if len(s.routeTable) == 0 {
		t.Fatal("the route table is empty")
	}
	wildcard := regexp.MustCompile(`\{[^}]+\}`)
	for _, row := range s.routeTable {
		method, path, _ := strings.Cut(row.pattern, " ")
		req := httptest.NewRequest(method, "http://omo"+wildcard.ReplaceAllString(path, "x"), nil)
		if _, got := s.mux.Handler(req); got != row.pattern {
			t.Errorf("%s is served by %q", row.pattern, got)
		}
	}
}
