package supervisor

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func TestGeneratedBranchNameUsesConfiguredPrefix(t *testing.T) {
	o := newOffice(t, map[string]string{})
	o.Sup.Cfg.Branches.Prefix = "team/job-"
	o.Sup.Cfg.Branches.Naming = "generated"
	j := &queue.Job{ID: 42, Role: "developer"}
	got, err := o.Sup.branchNameForJob(j)
	if err != nil {
		t.Fatal(err)
	}
	if got != "team/job-42" {
		t.Fatalf("branch = %q", got)
	}
}

func TestAIBranchNameLaunchesOneShotAgent(t *testing.T) {
	o := newOffice(t, map[string]string{
		"smokealarm": "ready\nbranchname|feat/add-search-index\nsleep|10s\n",
	})
	o.Sup.Cfg.Branches.Prefix = "omo/job-"
	o.Sup.Cfg.Branches.Naming = "ai"
	j := &queue.Job{Title: "Search", Goal: "Add a customer search index", Role: "developer", Repo: "api"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	got, err := o.Sup.branchNameForJob(j)
	if err != nil {
		t.Fatal(err)
	}
	if got != "feat/add-search-index" {
		t.Fatalf("branch = %q", got)
	}
	events, _ := db.EventsSince(o.DB, 0)
	found := false
	for _, event := range events {
		found = found || event.Kind == "branch_name_generated" && event.JobID == j.ID
	}
	if !found {
		t.Fatalf("branch_name_generated event missing: %+v", events)
	}
}

func TestAIBranchNameUsesSmokeAlarmProfile(t *testing.T) {
	o := newOffice(t, map[string]string{
		"smokealarm": "ready\nbranchname|feat/add-search-index\nsleep|10s\n",
	})
	o.Sup.Cfg.Branches.Naming = "ai"
	j := &queue.Job{Title: "Search", Goal: "Add a customer search index", Role: "developer", Repo: "api"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	got, err := o.Sup.branchNameForJob(j)
	if err != nil {
		t.Fatal(err)
	}
	if got != "feat/add-search-index" {
		t.Fatalf("branch = %q", got)
	}
	events, err := db.EventsSince(o.DB, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind != "branch_name_generated" {
			continue
		}
		agent, err := db.GetAgent(o.DB, event.Agent)
		if err != nil {
			t.Fatal(err)
		}
		if agent.Profile != "smokealarm" {
			t.Fatalf("branch namer profile = %q, want smokealarm", agent.Profile)
		}
		return
	}
	t.Fatal("branch_name_generated event missing")
}

func TestAIBranchNameHandshakeRetryUsesSmokeAlarmFailover(t *testing.T) {
	oldReadyTimeout := ReadyTimeout
	ReadyTimeout = time.Second
	t.Cleanup(func() { ReadyTimeout = oldReadyTimeout })

	o := newOffice(t, map[string]string{
		"smokealarm": "sleep|10s\n",
		"developer":  "ready\nbranchname|feat/retry-namer\nsleep|10s\n",
	})
	o.Sup.Cfg.Roles["smokealarm"] = config.RoleModels{
		Models:     []string{"smokealarm", "developer"},
		Assignment: config.AssignmentFailover,
	}
	o.Sup.Cfg.Branches.Naming = "ai"
	j := &queue.Job{Title: "Retry naming", Goal: "exercise branch-namer failover", Role: "developer", Repo: "api"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}

	got, err := o.Sup.branchNameForJob(j)
	if err != nil {
		t.Fatal(err)
	}
	if got != "feat/retry-namer" {
		t.Fatalf("branch = %q", got)
	}
}

func TestBranchNamerRetrySelectionFailureWakesWaiter(t *testing.T) {
	oldReadyTimeout := ReadyTimeout
	ReadyTimeout = 50 * time.Millisecond
	t.Cleanup(func() { ReadyTimeout = oldReadyTimeout })

	o := newOffice(t, nil)
	o.Sup.Cfg.Roles["smokealarm"] = config.RoleModels{}
	j := &queue.Job{Title: "Selection failure", Goal: "wake branch naming caller", Role: "developer"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	const name = "branch-namer-selection-failure"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "branch_namer", Profile: "smokealarm", JobID: j.ID}); err != nil {
		t.Fatal(err)
	}
	waiter := make(chan branchNameResult, 1)
	o.Sup.mu.Lock()
	o.Sup.branchNameWaiters[j.ID] = waiter
	o.Sup.mu.Unlock()
	t.Cleanup(func() {
		o.Sup.mu.Lock()
		delete(o.Sup.branchNameWaiters, j.ID)
		o.Sup.mu.Unlock()
	})

	go o.Sup.watchHandshake(name, "branch_namer", "smokealarm", j.ID, o.Dir, j.Goal, 0, true, false, false)
	select {
	case result := <-waiter:
		if result.err == nil {
			t.Fatal("selection failure did not report an error")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("branch naming waiter was stranded after retry selection failure")
	}
}

func TestBranchNamerRetryLaunchFailureWakesWaiter(t *testing.T) {
	oldReadyTimeout := ReadyTimeout
	ReadyTimeout = 50 * time.Millisecond
	t.Cleanup(func() { ReadyTimeout = oldReadyTimeout })

	o := newOffice(t, nil)
	o.Sup.Cfg.Models["broken"] = config.Profile{Cmd: filepath.Join(t.TempDir(), "missing-branch-namer")}
	o.Sup.Cfg.Roles["smokealarm"] = config.RoleModels{
		Models:     []string{"smokealarm", "broken"},
		Assignment: config.AssignmentFailover,
	}
	j := &queue.Job{Title: "Launch failure", Goal: "wake branch naming caller", Role: "developer"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	const name = "branch-namer-launch-failure"
	if err := db.InsertAgent(o.DB, db.Agent{Name: name, Role: "branch_namer", Profile: "smokealarm", JobID: j.ID}); err != nil {
		t.Fatal(err)
	}
	waiter := make(chan branchNameResult, 1)
	o.Sup.mu.Lock()
	o.Sup.branchNameWaiters[j.ID] = waiter
	o.Sup.mu.Unlock()
	t.Cleanup(func() {
		o.Sup.mu.Lock()
		delete(o.Sup.branchNameWaiters, j.ID)
		o.Sup.mu.Unlock()
	})

	go o.Sup.watchHandshake(name, "branch_namer", "smokealarm", j.ID, o.Dir, j.Goal, 0, true, false, false)
	select {
	case result := <-waiter:
		if result.err == nil {
			t.Fatal("retry launch failure did not report an error")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("branch naming waiter was stranded after retry launch failure")
	}
}

func TestKillingBranchNamerWakesWaiter(t *testing.T) {
	oldReadyTimeout := ReadyTimeout
	ReadyTimeout = time.Second
	t.Cleanup(func() { ReadyTimeout = oldReadyTimeout })

	o := newOffice(t, map[string]string{
		"smokealarm": "ready\nsleep|10s\n",
	})
	o.Sup.Cfg.Branches.Naming = "ai"
	j := &queue.Job{Title: "Kill naming", Goal: "wake branch naming caller", Role: "developer"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := o.Sup.branchNameForJob(j)
		result <- err
	}()

	var agent db.Agent
	waitFor(t, time.Second, "branch namer starts", func() bool {
		agents, err := db.LivingByJobRole(o.DB, j.ID, "branch_namer")
		if err != nil || len(agents) != 1 {
			return false
		}
		agent = agents[0]
		return agent.State == "working"
	})
	if err := o.Sup.StopAgent(agent.Name, "user", "kill"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("killed branch namer returned a branch name")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("killed branch namer stranded its waiter")
	}
}

func TestBranchNamerDoneWithoutNameWakesWaiter(t *testing.T) {
	o := newOffice(t, map[string]string{
		"smokealarm": "ready\ndone|naming complete\nsleep|10s\n",
	})
	o.Sup.Cfg.Branches.Naming = "ai"
	j := &queue.Job{Title: "Done naming", Goal: "wake branch naming caller", Role: "developer"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := o.Sup.branchNameForJob(j)
		result <- err
	}()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("branch namer completed without a branch name")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("completed branch namer stranded its waiter")
	}
}

func TestBranchNamerRestartLaunchFailureWakesWaiter(t *testing.T) {
	o := newOffice(t, map[string]string{
		"smokealarm": "ready\nsleep|10s\n",
	})
	o.Sup.Cfg.Branches.Naming = "ai"
	j := &queue.Job{Title: "Restart naming", Goal: "wake branch naming caller", Role: "developer"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := o.Sup.branchNameForJob(j)
		result <- err
	}()

	var agent db.Agent
	waitFor(t, time.Second, "branch namer starts", func() bool {
		agents, err := db.LivingByJobRole(o.DB, j.ID, "branch_namer")
		if err != nil || len(agents) != 1 {
			return false
		}
		agent = agents[0]
		return agent.State == "working"
	})
	profile := o.Sup.Cfg.Models["smokealarm"]
	profile.Cmd = filepath.Join(t.TempDir(), "missing-branch-namer")
	o.Sup.Cfg.Models["smokealarm"] = profile
	if err := o.Sup.StopAgent(agent.Name, "user", "restart"); err == nil {
		t.Fatal("restart succeeded despite missing replacement command")
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("failed branch-namer restart returned a branch name")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("failed branch-namer restart stranded its waiter")
	}
}

func TestBranchNamerRestartAssignmentFailureWakesWaiter(t *testing.T) {
	o := newOffice(t, map[string]string{
		"smokealarm": "ready\nsleep|10s\n",
	})
	o.Sup.Cfg.Branches.Naming = "ai"
	j := &queue.Job{Title: "Restart assignment", Goal: "wake branch naming caller", Role: "developer"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := o.Sup.branchNameForJob(j)
		result <- err
	}()

	var agent db.Agent
	waitFor(t, time.Second, "branch namer starts", func() bool {
		agents, err := db.LivingByJobRole(o.DB, j.ID, "branch_namer")
		if err != nil || len(agents) != 1 {
			return false
		}
		agent = agents[0]
		return agent.State == "working"
	})
	if _, err := o.DB.Exec(`CREATE TRIGGER reject_branch_namer_assignment
		BEFORE UPDATE OF assignee ON jobs WHEN NEW.id = ` + fmt.Sprint(j.ID) + `
		BEGIN SELECT RAISE(FAIL, 'branch namer assignment failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.StopAgent(agent.Name, "user", "restart"); err == nil {
		t.Fatal("restart succeeded despite rejected branch-namer assignment")
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("failed replacement returned a branch name")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("failed replacement stranded its branch naming waiter")
	}
}

func TestBranchNamerRestartKeepsWaiterForReplacement(t *testing.T) {
	o := newOffice(t, map[string]string{
		"smokealarm": "ready\nsleep|10s\n",
		"developer":  "ready\nbranchname|feat/restarted-namer\nsleep|10s\n",
	})
	o.Sup.Cfg.Branches.Naming = "ai"
	j := &queue.Job{Title: "Restart naming", Goal: "keep naming caller active", Role: "developer"}
	if err := o.Sup.Jobs.Create(j); err != nil {
		t.Fatal(err)
	}
	result := make(chan branchNameResult, 1)
	go func() {
		name, err := o.Sup.branchNameForJob(j)
		result <- branchNameResult{name: name, err: err}
	}()

	var agent db.Agent
	waitFor(t, time.Second, "branch namer starts", func() bool {
		agents, err := db.LivingByJobRole(o.DB, j.ID, "branch_namer")
		if err != nil || len(agents) != 1 {
			return false
		}
		agent = agents[0]
		return agent.State == "working"
	})
	o.Sup.Cfg.Models["smokealarm"] = o.Sup.Cfg.Models["developer"]
	if err := o.Sup.StopAgent(agent.Name, "user", "restart"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-result:
		if got.err != nil || got.name != "feat/restarted-namer" {
			t.Fatalf("replacement branch result = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement branch namer did not return a name")
	}
}

func TestValidAIBranchNameRequiresGroupedConventionalType(t *testing.T) {
	for _, valid := range []string{"feat/add-auth", "fix/api-timeout", "ci/update-actions"} {
		if !validAIBranchName(valid) {
			t.Errorf("valid branch name rejected: %q", valid)
		}
	}
	for _, invalid := range []string{"add-auth", "feat-add-auth", "Feat/add-auth", "feat/-auth", "feat/add--auth", "feat/add/auth", "feat/"} {
		if validAIBranchName(invalid) {
			t.Errorf("invalid branch name accepted: %q", invalid)
		}
	}
}
