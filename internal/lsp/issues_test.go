package lsp

import (
	"testing"
	"testing/synctest"
	"time"
)

// Losing the previous diagnostic set would replay unchanged issues; failing
// to recheck pending IDs would report issues already cleared or ignored.
func TestIssuesBaselineDebounceClearIgnoreAndAttach(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var reports []Report
		s := newIssues("/managed/a", time.Second, func(r Report) { reports = append(reports, r) })
		defer s.close()
		s.attach("first")
		a := diagnostic{Range: Range{Start: Position{Line: 2, Character: 3}}, Message: "old", Severity: 1}
		b := diagnostic{Message: "new", Severity: 2}
		c := diagnostic{Message: "other", Severity: 1}
		s.update("go", "main.go", []diagnostic{a}, true)
		time.Sleep(time.Second)
		synctest.Wait()
		if len(reports) != 0 {
			t.Fatal("baseline reported")
		}
		s.update("go", "main.go", []diagnostic{a, b}, false)
		s.attach("late")
		snap := s.snapshot()
		if len(snap) != 2 {
			t.Fatalf("snapshot: %+v", snap)
		}
		var ignored string
		for _, i := range snap {
			if i.Message == "new" {
				ignored = i.ID
			}
		}
		if !s.ignore(ignored) {
			t.Fatal("ignore failed")
		}
		s.update("go", "main.go", []diagnostic{a, b, c}, false)
		time.Sleep(time.Second)
		synctest.Wait()
		if len(reports) != 1 || len(reports[0].Issues) != 1 || reports[0].Issues[0].Message != "other" {
			t.Fatalf("reports: %+v", reports)
		}
		if len(reports[0].Sessions) != 2 {
			t.Fatalf("recipients: %+v", reports)
		}
		s.update("go", "main.go", []diagnostic{a, b, c}, false)
		time.Sleep(time.Second)
		synctest.Wait()
		if len(reports) != 1 {
			t.Fatal("unchanged report replayed")
		}
		s.update("go", "main.go", []diagnostic{a, b, c, {Message: "cleared"}}, false)
		s.update("go", "main.go", nil, false)
		time.Sleep(time.Second)
		synctest.Wait()
		if len(reports) != 1 || len(s.snapshot()) != 0 {
			t.Fatal("clear retained pending issues")
		}
	})
}

func TestIssuesLateAttachmentAndStableWorktreeIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var reports []Report
		s := newIssues("/managed/a", time.Second, func(r Report) { reports = append(reports, r) })
		defer s.close()
		s.attach("first")
		s.update("go", "main.go", nil, true)
		d := diagnostic{Message: "new"}
		s.update("go", "main.go", []diagnostic{d}, false)
		id := s.snapshot()[0].ID
		s.attach("late")
		time.Sleep(time.Second)
		synctest.Wait()
		if len(reports) != 1 || len(reports[0].Sessions) != 1 || reports[0].Sessions[0] != "first" {
			t.Fatalf("late agent replay: %+v", reports)
		}
		s.update("go", "main.go", nil, false)
		s.update("go", "main.go", []diagnostic{d}, false)
		if s.snapshot()[0].ID != id {
			t.Fatal("identity changed")
		}
		other := newIssues("/managed/b", 0, func(Report) {})
		defer other.close()
		other.update("go", "main.go", []diagnostic{d}, true)
		if other.snapshot()[0].ID == id {
			t.Fatal("identity shared between worktrees")
		}
	})
}
