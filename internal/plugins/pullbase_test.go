package plugins

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// These tests run plugins/pull-base with real temporary git repositories: a
// bare "remote", the project folder (a clone, with main tracking
// origin/main) and a second clone that pushes new commits. Nothing reaches a
// network.

// pbGit runs git in dir and returns its output; the repositories never see
// the developer's git configuration.
func pbGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

type pbRepos struct {
	t       *testing.T
	remote  string // bare repository
	project string // the project folder, on main, tracking origin/main
	other   string // another clone, for new commits on the remote
}

func newPbRepos(t *testing.T) *pbRepos {
	t.Helper()
	root := t.TempDir()
	r := &pbRepos{t: t, remote: filepath.Join(root, "remote.git"), project: filepath.Join(root, "project"), other: filepath.Join(root, "other")}
	pbGit(t, root, "init", "-q", "--bare", "-b", "main", r.remote)
	pbGit(t, root, "clone", "-q", r.remote, r.project)
	pbGit(t, r.project, "checkout", "-q", "-B", "main")
	r.commit(r.project, "a.txt", "one\n", "first")
	pbGit(t, r.project, "push", "-q", "-u", "origin", "main")
	pbGit(t, root, "clone", "-q", r.remote, r.other)
	return r
}

// commit writes content to file in repo and commits it.
func (r *pbRepos) commit(repo, file, content, msg string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(repo, file), []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
	pbGit(r.t, repo, "add", file)
	pbGit(r.t, repo, "commit", "-q", "-m", msg)
}

// advance pushes n new commits to the remote's main from the other clone.
func (r *pbRepos) advance(n int) string {
	r.t.Helper()
	pbGit(r.t, r.other, "pull", "-q", "--ff-only")
	for i := 0; i < n; i++ {
		r.commit(r.other, "remote.txt", strings.Repeat("x", i+1)+"\n", "remote "+strings.Repeat("x", i+1))
	}
	pbGit(r.t, r.other, "push", "-q", "origin", "main")
	return pbGit(r.t, r.other, "rev-parse", "HEAD")
}

// state is what must stay as it is when the plugin does not pull: the
// branch, the checked-out files, the working tree's status.
func (r *pbRepos) state() string {
	r.t.Helper()
	return pbGit(r.t, r.project, "rev-parse", "main") + "\n" + pbGit(r.t, r.project, "status", "--porcelain=v1", "--untracked-files=all")
}

func (r *pbRepos) head() string { return pbGit(r.t, r.project, "rev-parse", "main") }

// pbDesk records the go-aheads and cancels of the plugin.
type pbDesk struct {
	mu    sync.Mutex
	calls []string
}

func (d *pbDesk) GateRelease(_ context.Context, plugin string, ticket int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "release "+plugin+" "+itoa(ticket))
	return nil
}

func (d *pbDesk) GateCancel(_ context.Context, plugin string, ticket int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, "cancel "+plugin+" "+itoa(ticket))
	return nil
}

func (d *pbDesk) all() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

// pbPlugin installs the shipped plugin with config laid over its defaults.
func pbPlugin(t *testing.T, config map[string]any) (*env, *pbDesk) {
	t.Helper()
	e := newEnv(t)
	desk := &pbDesk{}
	e.m.o.Gates = desk
	m := e.shippedPlugin("pull-base")
	merged := map[string]any{}
	for k, v := range m.DefaultConfig {
		merged[k] = v
	}
	// One round of checks lasts for the whole test, so no test depends on
	// how fast the machine is.
	merged["fetch_reuse_seconds"] = 3600
	for k, v := range config {
		merged[k] = v
	}
	if errs := e.load(enabled("pull-base", merged)); errs["pull-base"] != nil {
		t.Fatal(errs["pull-base"])
	}
	return e, desk
}

// pbProject is one project entry of a gate's event data.
func pbProject(id, branch, folder string) map[string]any {
	return map[string]any{"project": id, "base_branch": branch, "folder": map[string]any{"kind": "repository", "path": folder, "available": true, "reason": ""}}
}

func pbGate(t *testing.T, e *env, move api.GateMove, ticket int, inStory bool, projects ...map[string]any) GateResult {
	t.Helper()
	typ := "task"
	if len(projects) == 0 {
		t.Fatal("no projects")
	}
	list := make([]any, len(projects))
	for i, p := range projects {
		list[i] = p
	}
	res, err := e.m.RunGate(t.Context(), "pull-base", move, map[string]any{
		"move":     string(move),
		"ticket":   map[string]any{"number": ticket, "title": "t", "type": typ},
		"type":     typ,
		"in_story": inStory,
		"projects": list,
	})
	if err != nil {
		t.Fatalf("RunGate: %v", err)
	}
	return res
}

func pbAction(t *testing.T, e *env, action string, ticket int) string {
	t.Helper()
	out, err := e.m.Trigger(t.Context(), "pull-base", action, TicketPayload(api.TicketSummary{Number: ticket, Title: "t"}, ""))
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return out
}

func pbFetches(t *testing.T, e *env) int {
	t.Helper()
	tasks, err := e.m.Tasks("pull-base")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, task := range tasks {
		if strings.HasPrefix(task.Name, "fetch") {
			n++
		}
	}
	return n
}

var (
	pbAsk = []api.GateChoice{
		{Label: "Pull and continue", Action: "pull"},
		{Label: "Continue without pulling", Action: "continue"},
		{Label: "Cancel", Action: "cancel"},
	}
	pbBlocked = []api.GateChoice{
		{Label: "Continue without pulling", Action: "continue"},
		{Label: "Cancel", Action: "cancel"},
	}
)

func TestPullBaseManifest(t *testing.T) {
	e := newEnv(t)
	m := e.shippedPlugin("pull-base")
	if m.Name != "pull-base" || m.Version == "" || !strings.Contains(m.Description, "checked-out branch") {
		t.Fatalf("manifest %q %q %q", m.Name, m.Version, m.Description)
	}
	var moves []string
	for _, h := range m.Hooks {
		if h.Event == EventGate {
			moves = append(moves, h.Move)
		}
	}
	slices.Sort(moves)
	if !slices.Equal(moves, []string{"release", "start"}) {
		t.Fatalf("gates %v", moves)
	}
	keys := map[string]bool{}
	for _, f := range m.ConfigFields {
		keys[f.Key] = true
	}
	for _, k := range []string{"mode", "automatic", "fetch_timeout_seconds"} {
		if !keys[k] {
			t.Errorf("config field %s is missing: %v", k, keys)
		}
	}
}

func TestPullBaseBehindAsksAndPullAndContinueFastForwards(t *testing.T) {
	r := newPbRepos(t)
	e, desk := pbPlugin(t, nil)
	tip := r.advance(2)

	got := pbGate(t, e, api.GateMoveRelease, 7, false, pbProject("app", "main", r.project))
	if got.Kind != GateHold || got.Reason != "`main` of app is 2 commits behind `origin/main`. Pull before starting?" || !slices.Equal(got.Choices, pbAsk) {
		t.Fatalf("answer %+v", got)
	}
	if r.head() == tip {
		t.Fatal("the gate pulled without being asked")
	}
	pbAction(t, e, "pull", 7)
	if r.head() != tip {
		t.Fatalf("main is at %s, want %s", r.head(), tip)
	}
	if st := pbGit(t, r.project, "status", "--porcelain=v1"); st != "" {
		t.Fatalf("status after the pull: %q", st)
	}
	if !slices.Equal(desk.all(), []string{"release pull-base 7"}) {
		t.Fatalf("calls %v", desk.all())
	}
}

func TestPullBaseOneCommitBehindIsSingular(t *testing.T) {
	r := newPbRepos(t)
	e, _ := pbPlugin(t, nil)
	r.advance(1)
	got := pbGate(t, e, api.GateMoveRelease, 7, false, pbProject("app", "main", r.project))
	if got.Reason != "`main` of app is 1 commit behind `origin/main`. Pull before starting?" {
		t.Fatalf("reason %q", got.Reason)
	}
}

func TestPullBaseContinueWithoutPullingLeavesTheFolderAlone(t *testing.T) {
	r := newPbRepos(t)
	e, desk := pbPlugin(t, nil)
	r.advance(2)
	pbGate(t, e, api.GateMoveRelease, 7, false, pbProject("app", "main", r.project))
	before := r.state()
	pbAction(t, e, "continue", 7)
	if r.state() != before {
		t.Fatalf("the folder changed:\n%s\n%s", before, r.state())
	}
	if !slices.Equal(desk.all(), []string{"release pull-base 7"}) {
		t.Fatalf("calls %v", desk.all())
	}
}

func TestPullBaseCancelCancelsTheHeldMove(t *testing.T) {
	r := newPbRepos(t)
	e, desk := pbPlugin(t, nil)
	r.advance(2)
	pbGate(t, e, api.GateMoveRelease, 7, false, pbProject("app", "main", r.project))
	before := r.state()
	pbAction(t, e, "cancel", 7)
	if r.state() != before {
		t.Fatalf("the folder changed")
	}
	if !slices.Equal(desk.all(), []string{"cancel pull-base 7"}) {
		t.Fatalf("calls %v", desk.all())
	}
}

func TestPullBaseUpToDateGoesAheadWithoutAQuestion(t *testing.T) {
	r := newPbRepos(t)
	e, _ := pbPlugin(t, nil)
	if got := pbGate(t, e, api.GateMoveRelease, 7, false, pbProject("app", "main", r.project)); got.Kind != GateGoAhead {
		t.Fatalf("answer %+v", got)
	}
	// Ahead of the remote is not behind it.
	r.commit(r.project, "local.txt", "x\n", "local")
	if got := pbGate(t, e, api.GateMoveRelease, 8, false, pbProject("app", "main", r.project)); got.Kind != GateGoAhead {
		t.Fatalf("a branch that is ahead: %+v", got)
	}
}

func TestPullBaseOnlyChecksTheMoveOfItsMode(t *testing.T) {
	r := newPbRepos(t)
	r.advance(2)
	for _, c := range []struct {
		mode      string
		asked     api.GateMove
		otherMove api.GateMove
	}{
		{"release", api.GateMoveRelease, api.GateMoveStart},
		{"start", api.GateMoveStart, api.GateMoveRelease},
	} {
		e, _ := pbPlugin(t, map[string]any{"mode": c.mode})
		if got := pbGate(t, e, c.otherMove, 7, false, pbProject("app", "main", r.project)); got.Kind != GateGoAhead {
			t.Errorf("mode %s, move %s: %+v, want a go-ahead", c.mode, c.otherMove, got)
		}
		if n := pbFetches(t, e); n != 0 {
			t.Errorf("mode %s: %d fetches for the other move", c.mode, n)
		}
		if got := pbGate(t, e, c.asked, 7, false, pbProject("app", "main", r.project)); got.Kind != GateHold {
			t.Errorf("mode %s, move %s: %+v, want the question", c.mode, c.asked, got)
		}
	}
}

func TestPullBaseNeverChecksATaskOfAStory(t *testing.T) {
	r := newPbRepos(t)
	e, _ := pbPlugin(t, nil)
	r.advance(2)
	if got := pbGate(t, e, api.GateMoveRelease, 7, true, pbProject("app", "main", r.project)); got.Kind != GateGoAhead {
		t.Fatalf("answer %+v", got)
	}
	if n := pbFetches(t, e); n != 0 {
		t.Fatalf("%d fetches for a task of a story", n)
	}
}

func TestPullBaseAutomaticPullsWithoutAQuestion(t *testing.T) {
	r := newPbRepos(t)
	e, desk := pbPlugin(t, map[string]any{"automatic": true})
	tip := r.advance(3)
	if got := pbGate(t, e, api.GateMoveRelease, 7, false, pbProject("app", "main", r.project)); got.Kind != GateGoAhead {
		t.Fatalf("answer %+v", got)
	}
	if r.head() != tip {
		t.Fatalf("main is at %s, want %s", r.head(), tip)
	}
	if len(desk.all()) != 0 {
		t.Fatalf("calls %v: a go-ahead answer needs no release", desk.all())
	}
}

// pbBlockedCases are the states in which the plugin never pulls.
var pbBlockedCases = []struct {
	name   string
	setup  func(r *pbRepos)
	reason string
}{
	{
		"diverged",
		func(r *pbRepos) {
			r.commit(r.project, "local.txt", "x\n", "local")
			r.advance(2)
		},
		"`main` of app has diverged from `origin/main` (1 commit ahead, 2 behind) and cannot be fast-forwarded.",
	},
	{
		"uncommitted changes the pull would touch",
		func(r *pbRepos) {
			r.advance(1)
			if err := os.WriteFile(filepath.Join(r.project, "remote.txt"), []byte("mine\n"), 0o600); err != nil {
				r.t.Fatal(err)
			}
		},
		"Uncommitted changes in app would be overwritten by pulling `origin/main` into `main`.",
	},
	{
		"tracked changes the pull would touch",
		func(r *pbRepos) {
			// The remote's new commit edits a.txt, which has local edits.
			pbGit(r.t, r.other, "pull", "-q", "--ff-only")
			r.commit(r.other, "a.txt", "remote edit\n", "edit a")
			pbGit(r.t, r.other, "push", "-q", "origin", "main")
			if err := os.WriteFile(filepath.Join(r.project, "a.txt"), []byte("local edit\n"), 0o600); err != nil {
				r.t.Fatal(err)
			}
		},
		"Uncommitted changes in app would be overwritten by pulling `origin/main` into `main`.",
	},
	{
		"no upstream",
		func(r *pbRepos) {
			r.advance(1)
			pbGit(r.t, r.project, "branch", "--unset-upstream", "main")
		},
		"`main` of app has no upstream branch to pull.",
	},
	{
		"remote cannot be reached",
		func(r *pbRepos) {
			if err := os.RemoveAll(r.remote); err != nil {
				r.t.Fatal(err)
			}
		},
		"`origin/main` of app could not be fetched, so it is unknown whether `main` is behind it.",
	},
}

func TestPullBaseBlockedStatesHoldWithTheirReasonAndChangeNothing(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, c := range pbBlockedCases {
			t.Run(c.name+map[bool]string{false: "", true: " (automatic)"}[automatic], func(t *testing.T) {
				r := newPbRepos(t)
				e, _ := pbPlugin(t, map[string]any{"automatic": automatic})
				c.setup(r)
				before := r.state()
				got := pbGate(t, e, api.GateMoveRelease, 7, false, pbProject("app", "main", r.project))
				if got.Kind != GateHold || got.Reason != c.reason || !slices.Equal(got.Choices, pbBlocked) {
					t.Fatalf("answer %+v, want a hold %q with %v", got, c.reason, pbBlocked)
				}
				if after := r.state(); after != before {
					t.Fatalf("the folder changed:\n%s\n--\n%s", before, after)
				}
			})
		}
	}
}

func TestPullBaseFetchesOnceForEachProjectAndBranch(t *testing.T) {
	r := newPbRepos(t)
	e, _ := pbPlugin(t, nil)
	r.advance(1)
	p := pbProject("app", "main", r.project)
	for ticket := 1; ticket <= 3; ticket++ {
		pbGate(t, e, api.GateMoveRelease, ticket, false, p)
	}
	if n := pbFetches(t, e); n != 1 {
		t.Fatalf("%d fetches for three tickets of one project and branch, want 1", n)
	}
	// Another branch of the same folder is another fetch.
	pbGit(t, r.project, "branch", "-q", "--track", "topic", "origin/main")
	pbGate(t, e, api.GateMoveRelease, 4, false, pbProject("app", "topic", r.project))
	if n := pbFetches(t, e); n != 2 {
		t.Fatalf("%d fetches after a second branch, want 2", n)
	}
}

func TestPullBaseFetchTimeoutHoldsAsUnreachable(t *testing.T) {
	// A server that accepts the connection and never answers.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback listener:", err)
	}
	var conns []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	r := newPbRepos(t)
	pbGit(t, r.project, "config", "remote.origin.url", "git://"+ln.Addr().String()+"/repo.git")
	e, _ := pbPlugin(t, map[string]any{"fetch_timeout_seconds": 1})
	before := r.state()
	got := pbGate(t, e, api.GateMoveRelease, 7, false, pbProject("app", "main", r.project))
	if got.Kind != GateHold || got.Reason != pbBlockedCases[4].reason || !slices.Equal(got.Choices, pbBlocked) {
		t.Fatalf("answer %+v", got)
	}
	if r.state() != before {
		t.Fatal("the folder changed")
	}
}
