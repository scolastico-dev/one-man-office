package board

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

var updateDragGolden = flag.Bool("update-drag-golden", false,
	"rewrite internal/board/testdata/user_drag_targets.json from the board's current behaviour")

const dragGoldenPath = "testdata/user_drag_targets.json"

// dragTicket describes the situation a ticket is in. Busy is the summary's
// busy field: set while omo holds a claim on the ticket or, for a story, on
// one of its tasks; failed_step names an engine step that waits for a
// person. Both are left out while false or empty.
type dragTicket struct {
	Type         string `json:"type"`
	InStory      bool   `json:"in_story"`
	Column       string `json:"column"`
	ReviewStage  string `json:"review_stage"`
	Resolution   string `json:"resolution,omitempty"`
	LiveAgent    bool   `json:"live_agent"`
	Projects     int    `json:"projects"`
	BaseBranch   string `json:"base_branch"`
	ProjectState string `json:"project_state"`
	WhenApproved string `json:"when_approved"`
	Busy         bool   `json:"busy,omitempty"`
	FailedStep   string `json:"failed_step,omitempty"`
}

// dragOutcome is what happened when the user dropped the ticket on a column.
type dragOutcome struct {
	OK           bool   `json:"ok"`
	Action       string `json:"action,omitempty"`
	Column       string `json:"column,omitempty"`
	Stage        string `json:"stage,omitempty"`
	NeedsComment bool   `json:"needs_comment,omitempty"`
	Confirm      bool   `json:"confirm,omitempty"`
	Code         string `json:"code,omitempty"`
}

// dragTargets holds one outcome per column, in board order.
type dragTargets struct {
	Planning   dragOutcome `json:"planning"`
	Ready      dragOutcome `json:"ready"`
	InProgress dragOutcome `json:"in_progress"`
	Review     dragOutcome `json:"review"`
	Done       dragOutcome `json:"done"`
}

func (d *dragTargets) slot(c api.Column) *dragOutcome {
	switch c {
	case api.ColumnPlanning:
		return &d.Planning
	case api.ColumnReady:
		return &d.Ready
	case api.ColumnInProgress:
		return &d.InProgress
	case api.ColumnReview:
		return &d.Review
	}
	return &d.Done
}

type dragCase struct {
	Name    string      `json:"name"`
	Ticket  dragTicket  `json:"ticket"`
	Targets dragTargets `json:"targets"`
}

type dragGolden struct {
	Cases []dragCase `json:"cases"`
}

// dragSetup is how a case builds its ticket on a fresh board.
type dragSetup struct {
	name       string
	story      bool // the ticket is a story
	inStory    bool // the ticket is a task inside a story
	col        api.Column
	stage      api.ReviewStage
	resolution api.Resolution // Done only; default completed
	live       bool           // a live agent session works on the ticket
	liveTask   bool           // story: a live developer session works on one of its tasks
	noProject  bool           // story without a project
	noBase     bool           // the project folder had no branch when the ticket was created
	export     bool           // when_approved is export
	projState  string         // "", "missing" or "disabled"
	busy       bool           // omo has claimed the ticket (start in Ready, merge in Review)
	busyTask   bool           // story: omo has claimed one of its tasks for a start
	failed     api.FailedStep // the engine step that waits for a person
}

func dragSetups() []dragSetup {
	pl, rd, ip, rv, dn := api.ColumnPlanning, api.ColumnReady, api.ColumnInProgress, api.ColumnReview, api.ColumnDone
	human, agentStage := api.ReviewStageHuman, api.ReviewStageAgent
	return []dragSetup{
		{name: "standalone task in planning", col: pl},
		{name: "standalone task in planning, base branch empty", col: pl, noBase: true},
		{name: "standalone task in planning, project missing", col: pl, projState: "missing"},
		{name: "standalone task in planning, project disabled", col: pl, projState: "disabled"},
		{name: "story in planning, no project", story: true, col: pl, noProject: true},
		{name: "story in planning, one project", story: true, col: pl},
		{name: "story in planning, base branch empty", story: true, col: pl, noBase: true},
		{name: "story in planning, project missing", story: true, col: pl, projState: "missing"},
		{name: "story in planning, project disabled", story: true, col: pl, projState: "disabled"},
		{name: "task in a story in planning", inStory: true, col: pl},
		{name: "task in a story in planning, project missing", inStory: true, col: pl, projState: "missing"},
		{name: "task in a story in planning, project disabled", inStory: true, col: pl, projState: "disabled"},

		{name: "standalone task in ready", col: rd},
		{name: "standalone task in ready, live developer session", col: rd, live: true},
		{name: "standalone task in ready, project missing", col: rd, projState: "missing"},
		{name: "story in ready", story: true, col: rd},
		{name: "story in ready, live session on the story", story: true, col: rd, live: true},
		{name: "story in ready, live session on one of its tasks", story: true, col: rd, liveTask: true},
		{name: "task in a story in ready", inStory: true, col: rd},
		{name: "task in a story in ready, live developer session", inStory: true, col: rd, live: true},
		{name: "standalone task in ready, omo is starting it", col: rd, busy: true},
		{name: "standalone task in ready, failed start", col: rd, failed: api.FailedStart},

		{name: "standalone task in progress", col: ip},
		{name: "standalone task in progress, live developer session", col: ip, live: true},
		{name: "story in progress", story: true, col: ip},
		{name: "story in progress, live session on one of its tasks", story: true, col: ip, liveTask: true},
		{name: "task in a story in progress", inStory: true, col: ip},
		{name: "task in a story in progress, live developer session", inStory: true, col: ip, live: true},
		{name: "story in progress, omo is starting one of its tasks", story: true, col: ip, busyTask: true},
		{name: "standalone task in progress, failed agent", col: ip, failed: api.FailedAgent},

		{name: "standalone task in review, agent review", col: rv, stage: agentStage},
		{name: "standalone task in review, waiting for the user", col: rv, stage: human},
		{name: "standalone task in review, waiting for the user, export", col: rv, stage: human, export: true},
		{name: "standalone task in review, merging", col: rv, stage: api.ReviewStageMerging},
		{name: "standalone task in review, merging, omo is merging it", col: rv, stage: api.ReviewStageMerging, busy: true},
		{name: "standalone task in review, exporting", col: rv, stage: api.ReviewStageExporting},
		{name: "story in review, agent review", story: true, col: rv, stage: agentStage},
		{name: "story in review, waiting for the user", story: true, col: rv, stage: human},
		{name: "story in review, merging", story: true, col: rv, stage: api.ReviewStageMerging},
		{name: "task in a story in review, agent review", inStory: true, col: rv, stage: agentStage},
		{name: "task in a story in review, waiting for the user", inStory: true, col: rv, stage: human},

		{name: "standalone task done, completed", col: dn},
		{name: "standalone task done, cancelled", col: dn, resolution: api.ResolutionCancelled},
		{name: "standalone task done, exported", col: dn, resolution: api.ResolutionExported},
		{name: "story done, completed", story: true, col: dn},
		{name: "story done, cancelled", story: true, col: dn, resolution: api.ResolutionCancelled},
		{name: "task in a story done, completed", inStory: true, col: dn},
		{name: "task in a story done, cancelled", inStory: true, col: dn, resolution: api.ResolutionCancelled},
	}
}

// build creates the situation on a fresh board and returns the board and the
// number of the ticket under test.
func (s dragSetup) build(t *testing.T) (*Board, int) {
	t.Helper()
	ctx := context.Background()
	b, env := newTestBoard(t)
	if s.noBase {
		env.setBranch("api", "")
	}
	projects := []string{"api"}
	if s.noProject {
		projects = nil
	}
	var n int
	switch {
	case s.story:
		n = mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: projects}).Number
		if s.liveTask {
			force(t, b, n, api.ColumnInProgress, "")
			task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(n)})
			if _, err := b.CreateSession(ctx, NewSession{Role: api.RoleDeveloper, Ticket: int64(task.Number), Project: "api",
				Name: "developer-agent", Model: "claude/fable"}); err != nil {
				t.Fatal(err)
			}
		}
	case s.inStory:
		story := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: projects})
		force(t, b, story.Number, api.ColumnInProgress, "")
		n = mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: projects, Story: intPtr(story.Number)}).Number
	default:
		req := api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: projects}
		if s.export {
			req.WhenApproved = api.WhenApprovedExport
		}
		n = mustCreate(t, b, User, req).Number
	}
	force(t, b, n, s.col, s.stage)
	if s.col == api.ColumnDone && s.resolution != "" {
		if _, err := b.db.Exec(`UPDATE tickets SET resolution = ? WHERE id = ?`, s.resolution, n); err != nil {
			t.Fatal(err)
		}
	}
	if s.live {
		role := api.RoleDeveloper
		if s.story {
			role = api.RolePM
		}
		if _, err := b.CreateSession(ctx, NewSession{Role: role, Ticket: int64(n), Project: "api",
			Name: string(role) + "-agent", Model: "claude/fable"}); err != nil {
			t.Fatal(err)
		}
	}
	if s.busyTask {
		task := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "B", Projects: []string{"api"}, Story: intPtr(n)})
		force(t, b, task.Number, api.ColumnReady, "")
		if err := b.ClaimStart(ctx, int64(task.Number)); err != nil {
			t.Fatal(err)
		}
	}
	if s.busy {
		claim := b.ClaimStart
		if s.col == api.ColumnReview {
			claim = b.ClaimMerge
		}
		if err := claim(ctx, int64(n)); err != nil {
			t.Fatal(err)
		}
	}
	if s.failed != "" {
		if err := b.SetFailed(ctx, int64(n), s.failed, "needs a person"); err != nil {
			t.Fatal(err)
		}
	}
	switch s.projState {
	case "missing":
		env.mu.Lock()
		p := env.projects["api"]
		p.Missing = true
		env.projects["api"] = p
		env.mu.Unlock()
	case "disabled":
		env.mu.Lock()
		p := env.projects["api"]
		p.Enabled = false
		env.projects["api"] = p
		env.mu.Unlock()
	}
	return b, n
}

// describe reads the ticket's situation back from the board, so the file
// records what the board holds and not what the setup meant to build.
func (s dragSetup) describe(t *testing.T, b *Board, n int) dragTicket {
	t.Helper()
	tk := get(t, b, n)
	d := dragTicket{
		Type:         string(tk.Type),
		InStory:      tk.Story != nil,
		Column:       string(tk.Column),
		ReviewStage:  string(tk.ReviewStage),
		Resolution:   string(tk.Resolution),
		LiveAgent:    s.live || s.liveTask,
		Projects:     len(tk.Projects),
		ProjectState: "ok",
		WhenApproved: string(tk.EffectiveWhenApproved),
		Busy:         tk.Busy,
		FailedStep:   string(tk.FailedStep),
	}
	if d.Column != string(api.ColumnDone) {
		d.Resolution = ""
	}
	if s.projState != "" {
		d.ProjectState = s.projState
	}
	if len(tk.Projects) > 0 {
		d.BaseBranch = tk.Projects[0].BaseBranch
	}
	return d
}

// dropOn drags the ticket onto column to as the user and records the result.
// Review to In Progress goes through Reject, the way the app does it.
func (s dragSetup) dropOn(t *testing.T, to api.Column) dragOutcome {
	t.Helper()
	ctx := context.Background()
	b, n := s.build(t)
	last := lastEvent(t, b)
	var got api.Ticket
	var err error
	rejects := s.col == api.ColumnReview && to == api.ColumnInProgress
	if rejects {
		got, err = b.Reject(ctx, User, int64(n), api.RejectRequest{Comment: "please fix"})
	} else {
		got, err = b.Move(ctx, User, int64(n), api.MoveRequest{Column: to})
	}
	if err != nil {
		if code := errCode(err); code != "" {
			return dragOutcome{Code: code}
		}
		t.Fatal(err)
	}
	out := dragOutcome{OK: true, Action: "reorder", Column: string(got.Column), Stage: string(got.ReviewStage)}
	for _, m := range movedEvents(t, eventsAfter(t, b, last)) {
		if m.Ticket.Number == n {
			out.Action = m.Action
		}
	}
	// A rejection without a comment is refused: the app asks for one first.
	if rejects {
		b2, n2 := s.build(t)
		_, err := b2.Reject(ctx, User, int64(n2), api.RejectRequest{})
		out.NeedsComment = errCode(err) == api.CodeValidation
	}
	// Stopping work is the one drop the app confirms before it acts.
	out.Confirm = out.Action == ActionStop
	return out
}

func buildDragGolden(t *testing.T) []byte {
	t.Helper()
	var g dragGolden
	for _, s := range dragSetups() {
		t.Run(s.name, func(t *testing.T) {
			b, n := s.build(t)
			c := dragCase{Name: s.name, Ticket: s.describe(t, b, n)}
			for _, col := range allColumns {
				*c.Targets.slot(col) = s.dropOn(t, col)
			}
			g.Cases = append(g.Cases, c)
		})
	}
	raw, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

// TestUserDragTargetsGolden pins what the user's drags do on every kind of
// ticket in every state. The browser keeps its own copy of these rules and
// checks it against the same file.
func TestUserDragTargetsGolden(t *testing.T) {
	got := buildDragGolden(t)
	if t.Failed() {
		return
	}
	if *updateDragGolden {
		if err := os.MkdirAll(filepath.Dir(dragGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dragGoldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(dragGoldenPath)
	if err != nil {
		t.Fatalf("cannot read %s: %v\nregenerate it with: go test ./internal/board -run TestUserDragTargetsGolden -update-drag-golden -count=1", dragGoldenPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the board's drag rules no longer match %s.\n"+
			"Regenerate it with: go test ./internal/board -run TestUserDragTargetsGolden -update-drag-golden -count=1\n"+
			"then review the diff and update the browser's move rules to match (the TypeScript golden test fails until they do).", dragGoldenPath)
	}
}
