package engine

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/branchname"
)

func (h *harness) setBranchTemplate(tmpl string) {
	h.t.Helper()
	g := h.st.General()
	g.Branches.Template = tmpl
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

// allBranches lists every local branch of repo.
func (h *harness) allBranches(repo string) []string {
	h.t.Helper()
	out := gitRun(h.t, repo, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
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
	h.setBranchTemplate("omo/{number}-{slug}")
	a := h.standalone("Fix login")
	h.approve(a)
	h.steps(2)
	if got, want := h.branchOf(a), "omo/"+strconv.FormatInt(a, 10)+"-fix-login"; got != want {
		t.Fatalf("branch %q, want %q", got, want)
	}
}

// orWord is s, or word when s is empty.
func orWord(s, word string) string {
	if s == "" {
		return word
	}
	return s
}

// A template without omo/ and one with folders of its own: the names are
// what the template says, the suffix rule is the same.
func TestBranchesFollowTheTemplate(t *testing.T) {
	h := newHarness(t)
	host, user := branchname.Machine()
	h.setBranchTemplate("{user}/{type}/{slug}")
	a, b := h.standalone("Fix login"), h.standalone("Fix login")
	h.approve(a)
	h.steps(2)
	h.approve(b)
	h.steps(2)
	prefix := orWord(branchname.Slug(user), "user")
	if got, want := h.branchOf(a), prefix+"/task/fix-login"; got != want {
		t.Fatalf("branch %q, want %q", got, want)
	}
	if got, want := h.branchOf(b), prefix+"/task/fix-login-2"; got != want {
		t.Fatalf("second branch %q, want %q", got, want)
	}

	h.setBranchTemplate("feature-{host}-{project}-{number}-{slug}")
	c := h.standalone("Fix login")
	h.approve(c)
	h.steps(2)
	want := "feature-" + orWord(branchname.Slug(host), "host") + "-" + branchname.Slug(h.project) + "-" + strconv.FormatInt(c, 10) + "-fix-login"
	if got := h.branchOf(c); got != want {
		t.Fatalf("branch %q, want %q", got, want)
	}
	for _, n := range []string{h.branchOf(a), h.branchOf(b), want} {
		if !slices.Contains(h.allBranches(h.repo), n) {
			t.Fatalf("branch %s missing in %v", n, h.allBranches(h.repo))
		}
	}
}

// git cannot have both a and a/b. A branch below the name takes the name
// (the next suffix is free); a branch that is a folder of the name stops the
// start with a message naming it, since no suffix helps.
func TestBranchNamesAndFoldersOfOtherBranches(t *testing.T) {
	h := newHarness(t)
	h.setBranchTemplate("work/{slug}")
	gitRun(t, h.repo, "branch", "work/fix-login/old")
	a := h.standalone("Fix login")
	h.approve(a)
	h.steps(2)
	if got := h.branchOf(a); got != "work/fix-login-2" {
		t.Fatalf("branch %q, want work/fix-login-2", got)
	}

	h.setBranchTemplate("team/{slug}")
	gitRun(t, h.repo, "branch", "team")
	b := h.standalone("Other")
	h.approve(b)
	h.steps(2)
	tk := h.ticket(b)
	if tk.Projects[0].Branch != "" {
		t.Fatalf("#%d started on %q", b, tk.Projects[0].Branch)
	}
	if !strings.Contains(tk.Attention, "the branch team exists, so git cannot create team/other below it") {
		t.Fatalf("attention %q", tk.Attention)
	}

	// A recorded branch that is a folder of the name counts as well.
	h.setBranchTemplate("rec/{slug}")
	old := h.standalone("Gone")
	if err := h.b.SetBranch(h.ctx, old, h.project, "rec", ""); err != nil {
		t.Fatal(err)
	}
	c := h.standalone("Third")
	h.approve(c)
	h.steps(2)
	if at := h.ticket(c).Attention; !strings.Contains(at, "the branch rec exists, so git cannot create rec/third below it") {
		t.Fatalf("attention %q", at)
	}
}

// What takes a name: a local branch, a remote-tracking branch of any remote,
// the branch recorded on another ticket whose branch no longer exists.
func TestBranchNamesTakenByRepositoryAndRecords(t *testing.T) {
	h := newHarness(t)
	g := h.st.General()
	g.Limits = api.LimitSettings{Tasks: 20, Stories: 20}
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

// Whatever the template gives, omo never adopts a branch it did not create:
// the base branch, main, master, a remote's default branch and any existing
// branch are taken, and the ticket gets the next suffix; the foreign
// branches stay where they were and omo creates none of those names.
func TestBranchTemplateNeverAdoptsAForeignBranch(t *testing.T) {
	h := newHarness(t)
	g := h.st.General()
	g.Limits = api.LimitSettings{Tasks: 20, Stories: 20}
	if err := h.st.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	h.setBranchTemplate("{slug}")
	gitRun(t, h.repo, "branch", "feature")
	gitRun(t, h.repo, "update-ref", "refs/remotes/origin/trunk", "HEAD")
	gitRun(t, h.repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
	before := map[string]string{}
	for _, b := range []string{"main", "feature"} {
		before[b] = gitRun(t, h.repo, "rev-parse", b)
	}
	want := map[string]string{"Main": "main-2", "master": "master-2", "Feature": "feature-2", "trunk": "trunk-2"}
	for _, title := range []string{"Main", "master", "Feature", "trunk"} {
		n := h.standalone(title)
		h.approve(n)
		h.steps(2)
		if got := h.branchOf(n); got != want[title] {
			t.Errorf("%q: branch %q, want %q", title, got, want[title])
		}
	}
	for b, at := range before {
		if got := gitRun(t, h.repo, "rev-parse", b); got != at {
			t.Errorf("%s moved from %s to %s", b, at, got)
		}
	}
	for _, b := range []string{"master", "trunk"} {
		if slices.Contains(h.allBranches(h.repo), b) {
			t.Errorf("omo created %s", b)
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

// A ticket that has a branch keeps it when the template changes; only
// branches created afterwards follow the template.
func TestExistingBranchesAreNeverRenamed(t *testing.T) {
	h := newHarness(t)
	h.setBranchTemplate("omo/{number}-{slug}")
	old := h.standalone("Same title")
	h.approve(old)
	h.steps(2)
	oldBranch := h.branchOf(old)
	if !strings.HasPrefix(oldBranch, "omo/"+strconv.FormatInt(old, 10)+"-") {
		t.Fatalf("old-style branch %q", oldBranch)
	}
	h.setBranchTemplate(branchname.Default)
	fresh := h.standalone("Same title")
	h.approve(fresh)
	h.steps(3)
	h.setBranchTemplate("elsewhere/{slug}")
	h.steps(2)
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

// Names are compared without regard to case: on macOS and Windows git keeps
// Fix-Login and fix-login in one file, so a branch, a record or a default
// branch of another case takes the name too.
func TestBranchNamesIgnoreCase(t *testing.T) {
	h := newHarness(t)
	h.setBranchTemplate("{slug}")
	gitRun(t, h.repo, "branch", "Fix-Login")
	gitRun(t, h.repo, "update-ref", "refs/remotes/origin/Develop", "HEAD")
	gitRun(t, h.repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/Develop")
	other := h.standalone("Other")
	if err := h.b.SetBranch(h.ctx, other, h.project, "Ship-It", ""); err != nil {
		t.Fatal(err)
	}
	a, b, c := h.standalone("Fix login"), h.standalone("develop"), h.standalone("ship it")
	for _, n := range []int64{a, b, c} {
		h.approve(n)
	}
	h.steps(2)
	got := []string{h.branchOf(a), h.branchOf(b), h.branchOf(c)}
	if want := []string{"fix-login-2", "develop-2", "ship-it-2"}; !slices.Equal(got, want) {
		t.Fatalf("branches %v, want %v", got, want)
	}
}

// omo hands git the full ref of a ticket's branch: a tag, a ref below refs/
// or a file in the git folder of the same name (each of which git prefers
// to the branch for a bare name) never stands in for it when omo merges,
// exports or cleans up.
func TestBranchWorkIgnoresRefsOfTheSameName(t *testing.T) {
	h := gitHarness(t)
	h.setBranchTemplate("{slug}")
	base := gitRun(t, h.repo, "rev-parse", "main")
	shadow := func(name string) {
		gitRun(t, h.repo, "tag", name, base)
		gitRun(t, h.repo, "update-ref", "refs/"+name, base)
	}

	n := h.skippedTask("Shadow")
	h.startTask(n, h.project, "b.txt", "b\n")
	if h.branchOf(n) != "shadow" {
		t.Fatalf("branch %q", h.branchOf(n))
	}
	shadow("shadow")
	h.handIn(n)
	h.steps(4)
	if tk := h.ticket(n); tk.Column != api.ColumnDone {
		t.Fatalf("ticket is %s, attention %q", tk.Column, tk.Attention)
	}
	if got := gitRun(t, h.repo, "show", "refs/heads/main:b.txt"); got != "b" {
		t.Fatalf("main has %q", got)
	}
	if slices.Contains(h.allBranches(h.repo), "shadow") {
		t.Fatal("the merged branch stayed")
	}
	if gitRun(t, h.repo, "rev-parse", "refs/tags/shadow") != base || gitRun(t, h.repo, "rev-parse", "refs/shadow") != base {
		t.Fatal("a ref of the same name changed")
	}

	bare := h.origin(h.project)
	x := h.create(api.CreateTicket{Type: api.TypeTask, Title: "Export shadow", Review: api.ReviewSkipped, WhenApproved: api.WhenApprovedExport})
	h.startTask(x, h.project, "c.txt", "c\n")
	shadow("export-shadow")
	tip := gitRun(t, h.repo, "rev-parse", "refs/heads/export-shadow")
	h.handIn(x)
	h.step()
	if got := remoteHead(t, bare, "export-shadow"); got != tip {
		t.Fatalf("pushed %q, want the branch at %s", got, tip)
	}
}
