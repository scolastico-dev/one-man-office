package board

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// ceoTickets creates a story, a standalone task and a task without a
// project as the CEO.
func ceoTickets(t *testing.T, b *Board, ceo Actor) []api.Ticket {
	t.Helper()
	return []api.Ticket{
		mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}}),
		mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}}),
		mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeTask, Title: "N"}),
	}
}

// TestCEOTicketsWaitForTheUser covers every way a ticket the CEO created
// could reach Ready For Dev or In Progress while the setting is on: only
// the user's approval moves it out of Planning.
func TestCEOTicketsWaitForTheUser(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	// The other settings would let the CEO approve its own tickets.
	env.setPolicy(func(p *Policy) { p.CEOMayApproveStories, p.CEOTicketsNeedUserApproval = true, true })
	ceo := agent(t, b, api.RoleCEO, 0)
	for _, tk := range ceoTickets(t, b, ceo) {
		n := int64(tk.Number)
		if tk.Column != api.ColumnPlanning {
			t.Fatalf("#%d created in %s", n, tk.Column)
		}
		// The CEO approves: refused, saying the user approves.
		_, err := b.Approve(ctx, ceo, n, api.ApproveRequest{})
		wantCode(t, err, api.CodeForbidden)
		if !strings.Contains(err.Error(), "the user approves") {
			t.Fatalf("refusal does not name the user: %v", err)
		}
		// The CEO moves it to ready or in progress.
		_, err = b.Move(ctx, ceo, n, api.MoveRequest{Column: api.ColumnReady})
		wantCode(t, err, api.CodeForbidden)
		_, err = b.Move(ctx, ceo, n, api.MoveRequest{Column: api.ColumnInProgress})
		wantCode(t, err, api.CodeInvalidTransition)
		// An update changes no column.
		if got, err := b.Update(ctx, ceo, n, api.UpdateTicket{Title: api.Some("renamed")}); err != nil || got.Column != api.ColumnPlanning {
			t.Fatalf("update: %s, %v", got.Column, err)
		}
		// Retry has nothing to retry on a ticket in Planning.
		_, err = b.Retry(ctx, ceo, n)
		wantCode(t, err, api.CodeInvalidTransition)
		// The engine (scheduler) starts only tickets in Ready.
		wantCode(t, b.BeginWork(ctx, n), api.CodeInvalidTransition)
		if got := get(t, b, tk.Number); got.Column != api.ColumnPlanning {
			t.Fatalf("#%d left planning: %s", n, got.Column)
		}

		// The user approves it the way a story is approved; the engine may
		// then start it.
		got, err := b.Approve(ctx, User, n, api.ApproveRequest{})
		if err != nil || got.Column != api.ColumnReady {
			t.Fatalf("user approval: %s, %v", got.Column, err)
		}
		// The CEO cannot take the user's approval back either.
		_, err = b.Move(ctx, ceo, n, api.MoveRequest{Column: api.ColumnPlanning})
		wantCode(t, err, api.CodeForbidden)
		if err := b.BeginWork(ctx, n); err != nil {
			t.Fatalf("begin work after approval: %v", err)
		}
	}
}

// TestCEOTicketsWithoutTheSetting: with the setting off, the CEO approves
// its stories while it may approve stories and releases its tasks while
// tasks are released by agents, as before.
func TestCEOTicketsWithoutTheSetting(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	env.setPolicy(func(p *Policy) { p.CEOMayApproveStories, p.CEOTicketsNeedUserApproval = true, false })
	ceo := agent(t, b, api.RoleCEO, 0)
	for _, tk := range ceoTickets(t, b, ceo) {
		got, err := b.Move(ctx, ceo, int64(tk.Number), api.MoveRequest{Column: api.ColumnReady})
		if err != nil || got.Column != api.ColumnReady {
			t.Fatalf("#%d: %s, %v", tk.Number, got.Column, err)
		}
	}
}

// TestCEOApprovalSettingLeavesOthersAlone: tasks a product manager creates
// in a story the user approved follow the task release setting, and the
// CEO still approves a story the user created while it may approve stories.
func TestCEOApprovalSettingLeavesOthersAlone(t *testing.T) {
	b, env := newTestBoard(t)
	ctx := context.Background()
	env.setPolicy(func(p *Policy) { p.CEOMayApproveStories, p.CEOTicketsNeedUserApproval = true, true })
	ceo := agent(t, b, api.RoleCEO, 0)
	story := mustCreate(t, b, ceo, api.CreateTicket{Type: api.TypeStory, Title: "S", Projects: []string{"api"}})
	if _, err := b.Approve(ctx, User, int64(story.Number), api.ApproveRequest{}); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginWork(ctx, int64(story.Number)); err != nil {
		t.Fatal(err)
	}
	pm := agent(t, b, api.RolePM, int64(story.Number))
	tk := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "T", Projects: []string{"api"}, Story: intPtr(story.Number)})
	got, err := b.Approve(ctx, pm, int64(tk.Number), api.ApproveRequest{})
	if err != nil || got.Column != api.ColumnReady {
		t.Fatalf("pm release: %s, %v", got.Column, err)
	}
	if err := b.BeginWork(ctx, int64(tk.Number)); err != nil {
		t.Fatalf("pm task start: %v", err)
	}
	// The task release setting still decides for the pm's tasks.
	env.setPolicy(func(p *Policy) { p.TasksRequireUserRelease = true })
	tk2 := mustCreate(t, b, pm, api.CreateTicket{Type: api.TypeTask, Title: "T2", Projects: []string{"api"}, Story: intPtr(story.Number)})
	_, err = b.Approve(ctx, pm, int64(tk2.Number), api.ApproveRequest{})
	wantCode(t, err, api.CodeForbidden)
	env.setPolicy(func(p *Policy) { p.TasksRequireUserRelease = false })

	// A story the user created: the CEO may approve it.
	users := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeStory, Title: "U", Projects: []string{"api"}})
	if got, err := b.Approve(ctx, ceo, int64(users.Number), api.ApproveRequest{}); err != nil || got.Column != api.ColumnReady {
		t.Fatalf("user's story: %s, %v", got.Column, err)
	}
	// A task the user created is released by the user, as before.
	userTask := mustCreate(t, b, User, api.CreateTicket{Type: api.TypeTask, Title: "UT", Projects: []string{"api"}})
	if got, err := b.Approve(ctx, User, int64(userTask.Number), api.ApproveRequest{}); err != nil || got.Column != api.ColumnReady {
		t.Fatalf("user's task: %s, %v", got.Column, err)
	}
}

// columnWriters names every function of the board that changes a ticket's
// column (calls setColumn or relocate) and why none of them takes a ticket
// out of Planning to Ready For Dev or In Progress except approve, which
// applies checkRelease (and with it the rule that the user approves the
// CEO's tickets). A new column writer fails TestColumnWritersKnown until it
// is listed here with its reason.
var columnWriters = map[string]string{
	"approve":       "Planning to Ready: the only way out of Planning; checkRelease decides who may",
	"withdraw":      "Ready to Planning",
	"stop":          "In Progress to Planning or Ready, the user only; tasks of a stopped story back to Ready",
	"Cancel":        "to Done",
	"relocate":      "helper of approve, withdraw and BeginWork",
	"BeginWork":     "Ready to In Progress (the engine), only from Ready",
	"CompleteMerge": "Review to Done",
	"MergeConflict": "Review to In Progress",
	"FinishExport":  "Review to Done",
	"markMerged":    "Review awaiting merge to Done, or legacy Done export confirmation",
	"endRound":      "Review to In Progress on a rejecting review round",
	"passReview":    "Review to Done, or In Progress to Review on hand-in",
	"Reject":        "Review to In Progress, by the user",
	"setColumn":     "the writer itself",
}

func TestColumnWritersKnown(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "setColumn" || sel.Sel.Name == "relocate") {
					if !slices.Contains(found, fn.Name.Name) {
						found = append(found, fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	for _, name := range found {
		if _, ok := columnWriters[name]; !ok {
			t.Errorf("%s changes a ticket's column: list it in columnWriters with why it cannot move a ticket the user has not approved out of Planning", name)
		}
	}
	for name := range columnWriters {
		if name != "setColumn" && !slices.Contains(found, name) {
			t.Errorf("columnWriters lists %s, which no longer changes a column", name)
		}
	}
}
