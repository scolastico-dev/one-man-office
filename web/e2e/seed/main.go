// Command seed puts the omo board into states that the API alone cannot reach
// (tickets in progress or in review) for the end-to-end tests. It reads a
// scenario from standard input, writes it through the same packages the
// server uses, and prints the ticket number of every ref.
//
// The seeded states are board records only: a ticket's branch is recorded
// but not created in its repository, and the agent session of a ticket in
// progress is one that omo ends when it starts (no process backs it). An
// approved review therefore ends in a failed merge step once omo runs.
//
// It works only on a temporary home: OMO_HOME must be set to an absolute
// path below the system's temporary folder, no omo may run on that home, and
// the home must not hold tickets or projects yet.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/service"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// scenario is the JSON read from standard input.
type scenario struct {
	Paused   *bool            `json:"paused"`
	Projects []scenarioRepo   `json:"projects"`
	Tickets  []scenarioTicket `json:"tickets"`
}

type scenarioRepo struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

type scenarioTicket struct {
	Ref          string   `json:"ref"`
	Type         string   `json:"type"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Projects     []string `json:"projects"`
	Story        string   `json:"story"`
	Review       string   `json:"review"`
	WhenApproved string   `json:"when_approved"`
	To           string   `json:"to"`
	Branch       string   `json:"branch"`
}

// result is what the tool prints.
type result struct {
	Refs map[string]int64 `json:"refs"`
}

// stages in the order the walk reaches them.
var stages = []string{"planning", "ready", "in_progress", "review", "done"}

func stageIndex(to string) int {
	for i, s := range stages {
		if s == to {
			return i
		}
	}
	return -1
}

func main() {
	if err := run(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

// boardEnv is the board's view of settings and git, as the server builds it.
type boardEnv struct {
	st  *settings.Store
	git *gitops.Git
}

func (e boardEnv) Policy() board.Policy {
	g := e.st.General()
	return board.Policy{
		CEOEnabled:              g.CEO.Enabled,
		CEOMayApproveStories:    g.CEO.MayApproveStories,
		TasksRequireUserRelease: g.Tasks.RequireUserRelease,
		ReviewStory:             g.ReviewDefaults.Story,
		ReviewTask:              g.ReviewDefaults.Task,
		WhenApprovedStory:       g.WhenApprovedDefaults.Story,
		WhenApprovedTask:        g.WhenApprovedDefaults.Task,
	}
}

func (e boardEnv) Project(id string) (board.ProjectInfo, bool) {
	p, ok := e.st.Project(id)
	if !ok {
		return board.ProjectInfo{}, false
	}
	return board.ProjectInfo{ID: p.ID, Path: p.Path, Enabled: p.Enabled, Missing: p.Missing}, true
}

func (e boardEnv) Model(id string) (board.ModelInfo, bool) {
	m, ok := e.st.Model(id)
	if !ok {
		return board.ModelInfo{}, false
	}
	return board.ModelInfo{ID: m.ID, Selectable: m.Selectable}, true
}

func (e boardEnv) CurrentBranch(path string) (string, error) {
	return e.git.CurrentBranch(path)
}

// checkHome refuses every home that is not a temporary one.
func checkHome() error {
	if os.Getenv("OMO_HOME") == "" {
		return errors.New("OMO_HOME is not set; the seeding tool only runs on an explicit temporary home")
	}
	home, err := globalhome.Dir()
	if err != nil {
		return err
	}
	real := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	// A home that does not exist yet is resolved through its parent.
	resolved := filepath.Join(real(filepath.Dir(home)), filepath.Base(home))
	tmp := real(os.TempDir())
	rel, err := filepath.Rel(tmp, resolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("OMO_HOME %s is not below the temporary folder %s; refusing to seed it", home, tmp)
	}
	if user, err := os.UserHomeDir(); err == nil && real(filepath.Join(user, ".local", "omo")) == resolved {
		return fmt.Errorf("OMO_HOME %s is the default omo home; refusing to seed it", home)
	}
	return nil
}

func validate(sc scenario) error {
	repos := map[string]bool{}
	for _, p := range sc.Projects {
		if !filepath.IsAbs(p.Path) {
			return fmt.Errorf("project path %q must be absolute", p.Path)
		}
		key := p.Name
		if key == "" {
			key = filepath.Base(p.Path)
		}
		repos[key] = true
	}
	refs := map[string]scenarioTicket{}
	for _, t := range sc.Tickets {
		if t.Ref == "" {
			return errors.New("every ticket needs a ref")
		}
		if _, dup := refs[t.Ref]; dup {
			return fmt.Errorf("ref %q is used twice", t.Ref)
		}
		if t.Type != "story" && t.Type != "task" {
			return fmt.Errorf("ticket %q: type must be story or task, not %q", t.Ref, t.Type)
		}
		to := t.To
		if to == "" {
			to = "planning"
		}
		idx := stageIndex(to)
		if idx < 0 {
			return fmt.Errorf("ticket %q: unknown column %q (planning, ready, in_progress, review or done)", t.Ref, t.To)
		}
		if t.Type == "story" && idx > stageIndex("in_progress") {
			return fmt.Errorf("ticket %q: a story cannot be seeded beyond in_progress", t.Ref)
		}
		if t.Story != "" {
			s, ok := refs[t.Story]
			if !ok {
				return fmt.Errorf("ticket %q: story %q is not an earlier ref", t.Ref, t.Story)
			}
			if s.Type != "story" {
				return fmt.Errorf("ticket %q: %q is not a story", t.Ref, t.Story)
			}
		}
		if len(t.Projects) == 0 {
			return fmt.Errorf("ticket %q needs a project", t.Ref)
		}
		for _, p := range t.Projects {
			if !repos[p] {
				return fmt.Errorf("ticket %q: project %q is not in the scenario", t.Ref, p)
			}
		}
		refs[t.Ref] = t
	}
	return nil
}

func run(ctx context.Context, in io.Reader, out io.Writer) error {
	if err := checkHome(); err != nil {
		return err
	}
	var sc scenario
	dec := json.NewDecoder(io.LimitReader(in, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sc); err != nil {
		return fmt.Errorf("read scenario: %w", err)
	}
	if err := validate(sc); err != nil {
		return err
	}
	if url, running, err := service.Running(ctx); err != nil {
		return fmt.Errorf("check for a running omo: %w", err)
	} else if running {
		return fmt.Errorf("an omo is running on this home (%s); stop it before seeding", url)
	}
	layout, err := globalhome.Open()
	if err != nil {
		return err
	}
	d, err := db.Open(layout.DB)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer d.Close()
	st, err := settings.Open(layout.Root, settings.Options{})
	if err != nil {
		return fmt.Errorf("open settings: %w", err)
	}
	var existing int
	if err := d.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 || len(st.Projects(true)) > 0 {
		return errors.New("the home already has tickets or projects; the seeding tool only fills a fresh home")
	}
	b := board.New(d, boardEnv{st: st, git: gitops.New()}, board.Options{})
	s := seeder{b: b, layout: layout, ids: map[string]string{}, refs: map[string]int64{}}

	for _, p := range sc.Projects {
		added, err := st.AddProject(p.Path, p.Name)
		if err != nil {
			return fmt.Errorf("add project %s: %w", p.Path, err)
		}
		key := p.Name
		if key == "" {
			key = filepath.Base(added.Path)
		}
		s.ids[key] = added.ID
	}
	for _, t := range sc.Tickets {
		if err := s.seed(ctx, t); err != nil {
			return fmt.Errorf("ticket %q: %w", t.Ref, err)
		}
	}
	paused := sc.Paused == nil || *sc.Paused
	if err := b.SetPaused(ctx, paused); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(result{Refs: s.refs})
}

type seeder struct {
	b      *board.Board
	layout globalhome.Layout
	ids    map[string]string // project name in the scenario -> project id
	refs   map[string]int64
}

func (s *seeder) seed(ctx context.Context, t scenarioTicket) error {
	to := t.To
	if to == "" {
		to = "planning"
	}
	idx := stageIndex(to)
	req := api.CreateTicket{
		Type:         api.TicketType(t.Type),
		Title:        t.Title,
		Description:  t.Description,
		Review:       api.Review(t.Review),
		WhenApproved: api.WhenApproved(t.WhenApproved),
	}
	for _, p := range t.Projects {
		req.Projects = append(req.Projects, s.ids[p])
	}
	if t.Story != "" {
		n := int(s.refs[t.Story])
		req.Story = &n
	}
	created, err := s.b.Create(ctx, board.User, req)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	n := int64(created.Number)
	s.refs[t.Ref] = n
	branch := func() error {
		if t.Branch == "" {
			return nil
		}
		wt := filepath.Join(s.layout.Worktrees, filepath.FromSlash(t.Branch))
		return s.b.SetBranch(ctx, n, req.Projects[0], t.Branch, wt)
	}
	if idx == 0 {
		return branch()
	}
	if _, err := s.b.Approve(ctx, board.User, n, api.ApproveRequest{}); err != nil {
		return fmt.Errorf("approve: %w", err)
	}
	if idx == 1 {
		return branch()
	}
	role := api.RoleDeveloper
	if t.Type == "story" {
		role = api.RolePM
	}
	sess, err := s.b.CreateSession(ctx, board.NewSession{Role: role, Ticket: n, Name: fmt.Sprintf("seed-%d", n), Model: "seed"})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	if err := s.b.BeginWork(ctx, n); err != nil {
		return fmt.Errorf("begin work: %w", err)
	}
	if err := branch(); err != nil {
		return fmt.Errorf("set branch: %w", err)
	}
	if idx == 2 {
		return s.b.SetSessionState(ctx, sess.ID, api.SessionRunning)
	}
	actor, err := s.b.ResolveSession(ctx, sess.ID)
	if err != nil {
		return err
	}
	if _, err := s.b.Done(ctx, actor, api.DoneRequest{}); err != nil {
		return fmt.Errorf("hand in: %w", err)
	}
	if err := s.b.EndSession(ctx, sess.ID, "handed in"); err != nil {
		return err
	}
	if idx == 3 {
		return nil
	}
	return s.finish(ctx, n, req.Projects)
}

// finish walks a ticket in Review to Done the way its review settings ask.
func (s *seeder) finish(ctx context.Context, n int64, projects []string) error {
	for range 4 {
		tk, err := s.b.Get(ctx, board.User, n)
		if err != nil {
			return err
		}
		if tk.Column == api.ColumnDone {
			return nil
		}
		switch tk.ReviewStage {
		case api.ReviewStageAgent:
			for _, p := range projects {
				rs, err := s.b.CreateSession(ctx, board.NewSession{
					Role: api.RoleReviewer, Ticket: n, Project: p, Name: fmt.Sprintf("seed-review-%d", n), Model: "seed",
					ReviewRound: tk.ReviewRound})
				if err != nil {
					return fmt.Errorf("create reviewer session: %w", err)
				}
				actor, err := s.b.ResolveSession(ctx, rs.ID)
				if err != nil {
					return err
				}
				if _, err := s.b.Done(ctx, actor, api.DoneRequest{Verdict: board.VerdictApprove}); err != nil {
					return fmt.Errorf("review verdict: %w", err)
				}
				if err := s.b.EndSession(ctx, rs.ID, "reviewed"); err != nil {
					return err
				}
			}
		case api.ReviewStageHuman:
			if _, err := s.b.Approve(ctx, board.User, n, api.ApproveRequest{}); err != nil {
				return fmt.Errorf("approve review: %w", err)
			}
		case api.ReviewStageExporting:
			if err := s.b.CompleteExport(ctx, n, fmt.Sprintf("https://example.invalid/pull/%d", n)); err != nil {
				return fmt.Errorf("complete export: %w", err)
			}
		case api.ReviewStageMerging:
			if err := s.b.CompleteMerge(ctx, n); err != nil {
				return fmt.Errorf("complete merge: %w", err)
			}
		default:
			return fmt.Errorf("unexpected review stage %q", tk.ReviewStage)
		}
	}
	return fmt.Errorf("#%d did not reach done", n)
}
