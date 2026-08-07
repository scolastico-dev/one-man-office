package bus

import (
	"path/filepath"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/queue"
)

func setup(t *testing.T) (*Store, *queue.Store) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	for _, a := range []db.Agent{
		{Name: "ceo-ada", Role: "ceo", Profile: "p"},
		{Name: "pm-alex", Role: "product_manager", Profile: "p", JobID: 0},
		{Name: "developer-jason", Role: "developer", Profile: "p"},
		{Name: "reviewer-sara", Role: "reviewer", Profile: "p"},
	} {
		if err := db.InsertAgent(d, a); err != nil {
			t.Fatal(err)
		}
		if err := db.SetAgentState(d, a.Name, "working"); err != nil {
			t.Fatal(err)
		}
	}
	return &Store{DB: d, Dir: DBDirectory{DB: d}}, &queue.Store{DB: d}
}

// wireLineage creates spec-job(pm) → dev-job(developer) and points the
// reviewer at the dev job, so the SQL Directory can resolve relationships.
func wireLineage(t *testing.T, s *Store, q *queue.Store) int64 {
	t.Helper()
	spec := &queue.Job{Title: "spec", Goal: "g", Role: "product_manager"}
	q.Create(spec)
	q.SetAssignee(spec.ID, "pm-alex")
	dev := &queue.Job{Title: "task", Goal: "g", Role: "developer", ParentJob: spec.ID}
	q.Create(dev)
	q.SetAssignee(dev.ID, "developer-jason")
	if _, err := s.DB.Exec(`UPDATE agents SET job_id = ? WHERE name = 'pm-alex'`, spec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE agents SET job_id = ? WHERE name IN ('developer-jason','reviewer-sara')`, dev.ID); err != nil {
		t.Fatal(err)
	}
	return dev.ID
}

func TestDBDirectoryLineage(t *testing.T) {
	s, q := setup(t)
	wireLineage(t, s, q)
	d := s.Dir
	if pm, ok := d.PMOf("developer-jason"); !ok || pm != "pm-alex" {
		t.Fatalf("PMOf = %q %v", pm, ok)
	}
	devs := d.DevelopersOf("pm-alex")
	if len(devs) != 1 || devs[0] != "developer-jason" {
		t.Fatalf("DevelopersOf = %v", devs)
	}
	dev, pm, ok := d.ReviewTargets("reviewer-sara")
	if !ok || dev != "developer-jason" || pm != "pm-alex" {
		t.Fatalf("ReviewTargets = %q %q %v", dev, pm, ok)
	}
	if ceo, ok := d.CEO(); !ok || ceo != "ceo-ada" {
		t.Fatalf("CEO = %q %v", ceo, ok)
	}
}

func TestSendInboxRead(t *testing.T) {
	s, q := setup(t)
	wireLineage(t, s, q)
	var notified []string
	s.Notify = func(rs []string) { notified = rs }
	ids, err := s.Send("developer-jason", "pm-alex", "help", "stuck on schema", PrioHigh)
	if err != nil || len(ids) != 1 {
		t.Fatalf("send: ids=%v err=%v", ids, err)
	}
	if len(notified) != 1 || notified[0] != "pm-alex" {
		t.Fatalf("notify = %v", notified)
	}
	inbox, err := s.Inbox("pm-alex")
	if err != nil || len(inbox) != 1 || inbox[0].Subject != "help" {
		t.Fatalf("inbox = %+v err %v", inbox, err)
	}
	msg, err := s.Read("pm-alex", ids[0])
	if err != nil || msg.Body != "stuck on schema" {
		t.Fatalf("read = %+v err %v", msg, err)
	}
	if inbox, _ = s.Inbox("pm-alex"); len(inbox) != 0 {
		t.Fatalf("message still unread after Read: %+v", inbox)
	}
	// Only the recipient may read.
	if _, err := s.Read("developer-jason", ids[0]); err == nil {
		t.Fatal("non-recipient read must fail")
	}
}

func TestSendEnforcesRouting(t *testing.T) {
	s, q := setup(t)
	wireLineage(t, s, q)
	if _, err := s.Send("developer-jason", "user", "hi", "x", PrioNormal); err == nil {
		t.Fatal("developer→user must be rejected")
	}
}

func TestBroadcast(t *testing.T) {
	s, q := setup(t)
	wireLineage(t, s, q)
	ids, err := s.Send("ceo-ada", "", "all hands", "stand-up", PrioNormal)
	if err != nil {
		t.Fatal(err)
	}
	// Everyone living except the sender: pm-alex, developer-jason, reviewer-sara.
	if len(ids) != 3 {
		t.Fatalf("broadcast rows = %d, want 3", len(ids))
	}
}

func TestSendToUserLandsInUserInbox(t *testing.T) {
	s, _ := setup(t)
	if _, err := s.Send("ceo-ada", "user", "status", "all green", PrioNormal); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount("user"); n != 1 {
		t.Fatalf("user unread = %d, want 1", n)
	}
}

func TestHistoryIncludesReadAndInterAgentMail(t *testing.T) {
	s, q := setup(t)
	wireLineage(t, s, q)
	ids, err := s.Send("developer-jason", "pm-alex", "question", "details", PrioNormal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read("pm-alex", ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send("ceo-ada", "user", "status", "green", PrioNormal); err != nil {
		t.Fatal(err)
	}
	history, err := s.History()
	if err != nil || len(history) != 2 {
		t.Fatalf("history=%+v err=%v", history, err)
	}
	if history[1].To != "pm-alex" || !history[1].Read {
		t.Fatalf("read inter-agent mail missing: %+v", history)
	}
}
