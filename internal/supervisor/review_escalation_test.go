package supervisor

import (
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func TestRejectedReviewerRetainedAndPMCanOverride(t *testing.T) {
	o := newOffice(t, nil)
	o.Sup.Cfg.Reviews.EscalateAfter = 1

	pmJob := &queue.Job{Title: "spec", Goal: "ship", Role: "product_manager"}
	if err := o.Sup.Jobs.Create(pmJob); err != nil {
		t.Fatal(err)
	}
	for _, st := range []queue.State{queue.StateAssigned, queue.StateWorking} {
		if err := o.Sup.Jobs.Transition(pmJob.ID, st); err != nil {
			t.Fatal(err)
		}
	}
	o.Sup.Jobs.SetAssignee(pmJob.ID, "pm-ada")
	devJob := &queue.Job{Title: "patch", Goal: "fix", Role: "developer", ParentJob: pmJob.ID}
	if err := o.Sup.Jobs.Create(devJob); err != nil {
		t.Fatal(err)
	}
	for _, st := range []queue.State{queue.StateAssigned, queue.StateWorking, queue.StateReview} {
		if err := o.Sup.Jobs.Transition(devJob.ID, st); err != nil {
			t.Fatal(err)
		}
	}
	o.Sup.Jobs.SetAssignee(devJob.ID, "developer-bea")
	for _, a := range []db.Agent{
		{Name: "pm-ada", Role: "product_manager", Profile: "product_manager", JobID: pmJob.ID},
		{Name: "developer-bea", Role: "developer", Profile: "developer", JobID: devJob.ID},
		{Name: "reviewer-cora", Role: "reviewer", Profile: "reviewer", JobID: devJob.ID},
	} {
		if err := db.InsertAgent(o.DB, a); err != nil {
			t.Fatal(err)
		}
		if err := db.SetAgentState(o.DB, a.Name, "working"); err != nil {
			t.Fatal(err)
		}
	}

	reviewer, _ := db.GetAgent(o.DB, "reviewer-cora")
	j, _ := o.Sup.Jobs.Get(devJob.ID)
	if err := o.Sup.rejectVerdict(reviewer, j, "rename is outside the requested patch"); err != nil {
		t.Fatal(err)
	}
	if err := o.Sup.done(reviewer.Name, "rejected"); err != nil {
		t.Fatal(err)
	}
	if got := agentState(t, o, reviewer.Name); got != "working" {
		t.Fatalf("rejected reviewer state=%s, want retained working/waiting", got)
	}
	pmMail, _ := o.Sup.Mail.Inbox("pm-ada")
	if len(pmMail) < 2 {
		t.Fatalf("PM should receive FYI and escalation, got %+v", pmMail)
	}
	devMail, _ := o.Sup.Mail.Inbox("developer-bea")
	if len(devMail) == 0 || !strings.Contains(devMail[0].Body, "contact your PM") {
		t.Fatalf("developer was not told about escalation: %+v", devMail)
	}

	if err := o.Sup.overrideReview("pm-ada", proto.ReviewOverrideArgs{JobID: devJob.ID, Notes: "not in scope"}); err != nil {
		t.Fatal(err)
	}
	j, _ = o.Sup.Jobs.Get(devJob.ID)
	if j.State != queue.StateReview || !j.ReviewOverride {
		t.Fatalf("override state=%s flag=%v", j.State, j.ReviewOverride)
	}
	if got := o.Sup.reviewerForJob(devJob.ID); got != reviewer.Name {
		t.Fatalf("override replaced reviewer: got %q want %q", got, reviewer.Name)
	}
	reviewerMail, _ := o.Sup.Mail.Inbox(reviewer.Name)
	if len(reviewerMail) == 0 || !strings.Contains(reviewerMail[0].Body, "deliver a merge verdict") {
		t.Fatalf("reviewer did not receive merge instruction: %+v", reviewerMail)
	}
}
