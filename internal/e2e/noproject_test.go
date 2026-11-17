package e2e

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// TestTaskWithoutProjectEndToEnd runs a task without a project with real
// agent processes: the developer writes its findings into its working
// folder and hands in, the reviewer rejects round 1 and approves round 2,
// and the task is done with nothing merged. The project's repository is
// untouched, and the working folder stays until the user removes it.
func TestTaskWithoutProjectEndToEnd(t *testing.T) {
	w := newWorld(t, config{scripts: map[string]string{
		"developer": "ready\nwrite|findings-$TICKET.md|LRU wins, by $NAME\ndone|the findings are in findings-$TICKET.md\nwait-loop|10m",
		"reviewer":  "ready\nverdicts|reject,approve|round verdict\nwait-loop|10m",
	}})
	mainBefore := w.git("rev-parse", "main")
	tk, err := w.user.CreateTicket(context.Background(), api.CreateTicket{Type: api.TypeTask, Title: "Compare the caches", Review: api.ReviewAgent})
	if err != nil {
		t.Fatal(err)
	}
	n := tk.Number
	w.approve(n)

	tk = w.waitColumn(n, api.ColumnDone, "")
	if tk.Resolution != api.ResolutionCompleted || tk.ReviewRound != 2 || len(tk.Projects) != 0 {
		t.Fatalf("resolution %q round %d projects %+v", tk.Resolution, tk.ReviewRound, tk.Projects)
	}
	dir := filepath.Join(w.layout.Workdirs, itoa(n))
	if tk.Workdir != dir {
		t.Fatalf("workdir %q, want %q", tk.Workdir, dir)
	}
	body, err := os.ReadFile(filepath.Join(dir, "findings-"+itoa(n)+".md"))
	if err != nil || !strings.HasPrefix(string(body), "LRU wins, by ") {
		t.Fatalf("findings: %q %v", body, err)
	}
	var devs, rounds []int
	for _, s := range w.sessions(n) {
		switch s.Role {
		case api.RoleDeveloper:
			devs = append(devs, 1)
		case api.RoleReviewer:
			rounds = append(rounds, s.ReviewRound)
		}
		if s.Workdir != dir || s.Project != "" {
			t.Errorf("%s worked in %q (project %q)", s.Name, s.Workdir, s.Project)
		}
	}
	slices.Sort(rounds)
	if len(devs) != 1 || !slices.Equal(rounds, []int{1, 2}) {
		t.Fatalf("developers %d, reviewer rounds %v", len(devs), rounds)
	}
	if !w.hasComment(n, "round verdict") || !w.hasComment(n, "the findings are in") {
		t.Fatalf("comments: %q", w.comments(n))
	}
	if got := w.git("rev-parse", "main"); got != mainBefore {
		t.Fatal("main changed")
	}
	w.checkoutClean()
	w.settled()
	w.noCrashes(n)

	got, err := w.user.RemoveWorkdir(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	if got.Workdir != "" {
		t.Fatalf("workdir %q after the removal", got.Workdir)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the working folder is still there: %v", err)
	}
}
