package board

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// fakeEnv is an in-memory Environment for tests.
type fakeEnv struct {
	mu       sync.Mutex
	policy   Policy
	projects map[string]ProjectInfo
	models   map[string]ModelInfo
	branches map[string]string
	notified int
}

func (e *fakeEnv) Policy() Policy {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.policy
}

func (e *fakeEnv) Project(id string) (ProjectInfo, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.projects[id]
	return p, ok
}

func (e *fakeEnv) Model(id string) (ModelInfo, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, ok := e.models[id]
	return m, ok
}

func (e *fakeEnv) CurrentBranch(path string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.branches[path]
	if !ok {
		return "", errors.New("not a git repository: " + path)
	}
	return b, nil
}

func (e *fakeEnv) setPolicy(fn func(p *Policy)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn(&e.policy)
}

func (e *fakeEnv) setBranch(project, branch string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.branches[e.projects[project].Path] = branch
}

func (e *fakeEnv) notifications() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.notified
}

func newFakeEnv() *fakeEnv {
	return &fakeEnv{
		policy: Policy{
			CEOEnabled:        true,
			ReviewStory:       api.ReviewAgent,
			ReviewTask:        api.ReviewAgent,
			WhenApprovedStory: api.WhenApprovedMerge,
			WhenApprovedTask:  api.WhenApprovedMerge,
		},
		projects: map[string]ProjectInfo{
			"api":  {ID: "api", Path: "/src/api", Enabled: true},
			"ui":   {ID: "ui", Path: "/src/ui", Enabled: true},
			"off":  {ID: "off", Path: "/src/off", Enabled: false},
			"gone": {ID: "gone", Path: "/src/gone", Enabled: true, Missing: true},
		},
		models: map[string]ModelInfo{
			"claude/fable": {ID: "claude/fable", Selectable: true},
			"claude/opus":  {ID: "claude/opus", Selectable: false},
		},
		branches: map[string]string{
			"/src/api": "main",
			"/src/ui":  "main",
		},
	}
}

// openTestDB opens a database file in a temporary directory.
func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// newTestBoard returns a board on a fresh temporary database. The clock
// advances by one millisecond per call so timestamps are distinct.
func newTestBoard(t *testing.T) (*Board, *fakeEnv) {
	t.Helper()
	t.Setenv("OMO_HOME", t.TempDir())
	env := newFakeEnv()
	d := openTestDB(t, filepath.Join(t.TempDir(), "omo.db"))
	return New(d, env, testOptions(env)), env
}

func testOptions(env *fakeEnv) Options {
	var mu sync.Mutex
	clock := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	return Options{
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			clock = clock.Add(time.Millisecond)
			return clock
		},
		Notify: func() {
			env.mu.Lock()
			env.notified++
			env.mu.Unlock()
		},
	}
}

// agent starts a session for role on ticket (0 for the CEO) and returns the
// actor resolved from it. A reviewer is started for the ticket's current
// review round.
func agent(t *testing.T, b *Board, role api.Role, ticket int64) Actor {
	t.Helper()
	ctx := context.Background()
	project, round := "", 0
	if ticket != 0 {
		tk, err := b.Get(ctx, User, ticket)
		if err != nil {
			t.Fatal(err)
		}
		if len(tk.Projects) > 0 {
			project = tk.Projects[0].Project
		}
		if role == api.RoleReviewer {
			round = tk.ReviewRound
		}
	}
	s, err := b.CreateSession(ctx, NewSession{Role: role, Ticket: ticket, Project: project, Name: string(role) + "-agent",
		Model: "claude/fable", ReviewRound: round})
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.ResolveSession(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func mustCreate(t *testing.T, b *Board, a Actor, req api.CreateTicket) api.Ticket {
	t.Helper()
	tk, err := b.Create(context.Background(), a, req)
	if err != nil {
		t.Fatalf("create %+v: %v", req, err)
	}
	return tk
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if got := errCode(err); got != code {
		t.Fatalf("error = %v (code %q), want code %q", err, got, code)
	}
}

func intPtr(n int) *int { return &n }
