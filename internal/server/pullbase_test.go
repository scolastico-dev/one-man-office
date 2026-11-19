package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// The tests of plugins/pull-base through the real board and engine: the
// shipped plugin is installed from a local git repository of its folder, the
// project folder tracks a bare remote, and a second clone advances the
// remote.

// pullBase is a gate server whose project tracks a remote that moved ahead.
type pullBase struct {
	*gateServer
	project string // the project folder
	tip     string // the remote's main
	before  string // main of the project folder before the pull
}

func newPullBase(t *testing.T, startEngine bool, config map[string]any) *pullBase {
	t.Helper()
	g := newGateServer(t, startEngine)
	project := g.s.settings.Projects(false)[0].Path
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	other := filepath.Join(root, "other")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", remote)
	gitIn(t, project, "remote", "add", "origin", remote)
	gitIn(t, project, "push", "-q", "-u", "origin", "main")
	gitIn(t, root, "clone", "-q", remote, other)
	before := gitOut(t, project, "rev-parse", "main")
	for _, n := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(other, n+".txt"), []byte(n+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitIn(t, other, "add", n+".txt")
		gitIn(t, other, "commit", "-q", "-m", n)
	}
	gitIn(t, other, "push", "-q", "origin", "main")
	tip := gitOut(t, other, "rev-parse", "HEAD")

	repo := shippedPluginRepo(t, "pull-base")
	g.repos["pull-base"] = repo
	settings := api.PluginSettings{Name: "pull-base", Source: repo, Enabled: true}
	resp := asUser(g.b, g.s, "POST", "/api/plugins", community(settings))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("install: status %d: %s", resp.StatusCode, respText(t, resp))
	}
	// The install starts from the manifest's defaults; the test's settings
	// are laid over them like a user's edit.
	settings.Config = map[string]any{"mode": "release", "automatic": false, "fetch_timeout_seconds": 30}
	for k, v := range config {
		settings.Config[k] = v
	}
	resp = asUser(g.b, g.s, "PUT", "/api/plugins/pull-base", community(settings))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("configure: status %d: %s", resp.StatusCode, respText(t, resp))
	}
	return &pullBase{gateServer: g, project: project, tip: tip, before: before}
}

func (p *pullBase) main() string { return gitOut(p.t, p.project, "rev-parse", "main") }

// ticketOf creates a task, or a story, of the project in Planning.
func (p *pullBase) ticketOf(typ api.TicketType, title string) int {
	p.t.Helper()
	projects := p.s.settings.Projects(false)
	tk, err := p.s.board.Create(context.Background(), board.User, api.CreateTicket{Type: typ, Title: title, Projects: []string{projects[0].ID}})
	if err != nil {
		p.t.Fatal(err)
	}
	return tk.Number
}

const pullQuestion = "`main` of app is 2 commits behind `origin/main`. Pull before starting?"

func TestPullBaseReleaseHoldsThenPullsContinuesOrCancels(t *testing.T) {
	for _, typ := range []api.TicketType{api.TypeTask, api.TypeStory} {
		for _, c := range []struct {
			choice string
			column api.Column
			pulled bool
		}{
			{"pull", api.ColumnReady, true},
			{"continue", api.ColumnReady, false},
			{"cancel", api.ColumnPlanning, false},
		} {
			t.Run(string(typ)+" "+c.choice, func(t *testing.T) {
				p := newPullBase(t, false, map[string]any{"mode": "release"})
				n := p.ticketOf(typ, "work")
				p.ready(n)
				tk := p.wait(n, "the question", held("pull-base"))
				if tk.Column != api.ColumnPlanning {
					t.Fatalf("the held ticket is in %s", tk.Column)
				}
				g := gateOf(tk, "pull-base")
				if g.Reason != pullQuestion {
					t.Fatalf("reason %q", g.Reason)
				}
				var labels []string
				for _, ch := range g.Choices {
					labels = append(labels, ch.Label)
				}
				if strings.Join(labels, "|") != "Pull and continue|Continue without pulling|Cancel" {
					t.Fatalf("choices %v", labels)
				}
				if p.main() != p.before {
					t.Fatal("the branch moved before an answer")
				}
				p.action("pull-base", c.choice, n)
				p.wait(n, "the answer to take effect", inColumn(c.column))
				want := p.before
				if c.pulled {
					want = p.tip
				}
				if p.main() != want {
					t.Fatalf("main is at %s, want %s", p.main(), want)
				}
			})
		}
	}
}

func TestPullBaseAutomaticPullsInTheMove(t *testing.T) {
	p := newPullBase(t, false, map[string]any{"mode": "release", "automatic": true})
	n := p.ticketOf(api.TypeTask, "work")
	p.ready(n)
	p.wait(n, "Ready For Dev", inColumn(api.ColumnReady))
	if p.main() != p.tip {
		t.Fatalf("main is at %s, want %s", p.main(), p.tip)
	}
}

func TestPullBaseNeverChecksATaskOfAStory(t *testing.T) {
	p := newPullBase(t, false, map[string]any{"mode": "release"})
	story := p.ticketOf(api.TypeStory, "the story")
	projects := p.s.settings.Projects(false)
	tk, err := p.s.board.Create(context.Background(), board.User, api.CreateTicket{Type: api.TypeTask, Title: "inside", Projects: []string{projects[0].ID}, Story: &story})
	if err != nil {
		t.Fatal(err)
	}
	p.ready(tk.Number)
	p.wait(tk.Number, "Ready For Dev", inColumn(api.ColumnReady))
	if p.main() != p.before {
		t.Fatal("the branch moved for a task of a story")
	}
}

func TestPullBaseStartModeHoldsBeforeTheWorktreeAndBasesItOnThePulledBranch(t *testing.T) {
	p := newPullBase(t, true, map[string]any{"mode": "start"})
	n := p.ticketOf(api.TypeTask, "work")
	p.ready(n)
	tk := p.wait(n, "the question", held("pull-base"))
	if tk.Column != api.ColumnReady || tk.Gate.Move != api.GateMoveStart || gateOf(tk, "pull-base").Reason != pullQuestion {
		t.Fatalf("held ticket: column %s, gate %+v", tk.Column, tk.Gate)
	}
	for _, pr := range tk.Projects {
		if pr.Worktree != "" {
			if _, err := os.Stat(pr.Worktree); err == nil {
				t.Fatalf("the worktree %s exists before the answer", pr.Worktree)
			}
		}
	}
	p.action("pull-base", "pull", n)
	tk = p.wait(n, "the start", func(tk api.Ticket) bool { return tk.Column == api.ColumnInProgress })
	if p.main() != p.tip {
		t.Fatalf("main is at %s, want %s", p.main(), p.tip)
	}
	waitFor(t, "the worktree", func() bool {
		tk = p.get(n)
		return len(tk.Projects) == 1 && tk.Projects[0].Worktree != ""
	})
	if head := gitOut(t, tk.Projects[0].Worktree, "rev-parse", "HEAD"); head != p.tip {
		t.Fatalf("the worktree is based on %s, want the pulled %s", head, p.tip)
	}
}
