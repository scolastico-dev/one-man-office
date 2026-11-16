package engine

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func (h *harness) setTicketNumberInBranches(on bool) {
	h.t.Helper()
	g := h.st.General()
	g.Branches.TicketNumber = on
	if err := h.st.SetGeneral(g); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) branchOf(n int64) string {
	h.t.Helper()
	tk := h.ticket(n)
	if len(tk.Projects) == 0 {
		h.t.Fatalf("#%d has no project", n)
	}
	return tk.Projects[0].Branch
}

// By default a branch is omo/<title>; tickets with one title started in the
// same pass get one name each.
func TestBranchesHaveNoTicketNumberByDefault(t *testing.T) {
	h := newHarness(t)
	a, b, c := h.standalone("Fix login"), h.standalone("Fix login!"), h.standalone("fix LOGIN")
	for _, n := range []int64{a, b, c} {
		h.approve(n)
	}
	h.steps(2)
	got := []string{h.branchOf(a), h.branchOf(b), h.branchOf(c)}
	slices.Sort(got)
	if want := []string{"omo/fix-login", "omo/fix-login-2", "omo/fix-login-3"}; !slices.Equal(got, want) {
		t.Fatalf("branches %v, want %v", got, want)
	}
	if on := h.branches(h.repo); len(on) != 3 {
		t.Fatalf("branches in the repository: %v", on)
	}
}

func TestBranchesWithTicketNumber(t *testing.T) {
	h := newHarness(t)
	h.setTicketNumberInBranches(true)
	a := h.standalone("Fix login")
	h.approve(a)
	h.steps(2)
	if got, want := h.branchOf(a), "omo/"+strconv.FormatInt(a, 10)+"-fix-login"; got != want {
		t.Fatalf("branch %q, want %q", got, want)
	}
}

// What takes a name: a local branch, a remote-tracking branch of any remote,
// the branch recorded on another ticket whose branch no longer exists.
func TestBranchNamesTakenByRepositoryAndRecords(t *testing.T) {
	h := newHarness(t)
	g := h.st.General()
	g.MaxRunningAgents = 20
	if err := h.st.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	gitRun(t, h.repo, "branch", "omo/local-one")
	gitRun(t, h.repo, "update-ref", "refs/remotes/origin/omo/remote-one", "HEAD")
	gitRun(t, h.repo, "update-ref", "refs/remotes/upstream/omo/remote-one-2", "HEAD")
	old := h.standalone("Gone")
	if err := h.b.SetBranch(h.ctx, old, h.project, "omo/gone", ""); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Local one":  "omo/local-one-2",
		"Remote one": "omo/remote-one-3",
		"Gone":       "omo/gone-2",
		"Fresh":      "omo/fresh",
		"日本語":        "omo/ticket",
		"!!!":        "omo/ticket-2",
	}
	byTitle := map[string]int64{}
	for title := range want {
		byTitle[title] = h.standalone(title)
	}
	// One title at a time keeps "ticket" and "ticket-2" in a fixed order.
	for _, title := range []string{"Local one", "Remote one", "Gone", "Fresh", "日本語", "!!!"} {
		h.approve(byTitle[title])
		h.steps(2)
	}
	for title, n := range byTitle {
		if got := h.branchOf(n); got != want[title] {
			t.Errorf("%q: branch %q, want %q", title, got, want[title])
		}
	}
}

// A story's integration branch and its tasks' branches follow the same rule.
func TestStoryAndTaskBranchesAreUnique(t *testing.T) {
	h := newHarness(t)
	s := h.create(api.CreateTicket{Type: api.TypeStory, Title: "Checkout", Review: api.ReviewSkipped})
	h.startStory(s)
	task := h.storyTask(s, h.project, "Checkout")
	h.finishTask(task, h.project, "c.txt", "c\n")
	if got := h.branchOf(s); got != "omo/checkout" {
		t.Fatalf("story branch %q", got)
	}
	if got := h.branchOf(task); got != "omo/checkout-2" {
		t.Fatalf("task branch %q", got)
	}
}

// A ticket that has a branch keeps it when the setting changes; only
// branches created afterwards follow the setting.
func TestExistingBranchesAreNeverRenamed(t *testing.T) {
	h := newHarness(t)
	h.setTicketNumberInBranches(true)
	old := h.standalone("Same title")
	h.approve(old)
	h.steps(2)
	oldBranch := h.branchOf(old)
	if !strings.HasPrefix(oldBranch, "omo/"+strconv.FormatInt(old, 10)+"-") {
		t.Fatalf("old-style branch %q", oldBranch)
	}
	h.setTicketNumberInBranches(false)
	fresh := h.standalone("Same title")
	h.approve(fresh)
	h.steps(3)
	if got := h.branchOf(old); got != oldBranch {
		t.Fatalf("the old ticket's branch became %q", got)
	}
	if got := h.branchOf(fresh); got != "omo/same-title" {
		t.Fatalf("new ticket's branch %q", got)
	}
	if !slices.Contains(h.branches(h.repo), oldBranch) {
		t.Fatalf("branch %s is gone: %v", oldBranch, h.branches(h.repo))
	}
}
