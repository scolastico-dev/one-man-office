package lsp

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestManagerSubscriptionsAndSuppressionScope(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := New(Options{})
		s := newIssues("/managed", time.Second, m.publish)
		m.roots["/managed"] = &worktree{issues: s}
		m.sessions["agent"] = []string{"/managed"}
		s.attach("agent")
		defer s.close()
		var reports []Report
		unsubscribe := m.Subscribe(func(r Report) { reports = append(reports, r) })
		s.update("test", "a.go", nil, true)
		s.update("test", "a.go", []diagnostic{{Message: "new"}}, false)
		id := m.Snapshot("agent")[0].ID
		if m.Ignore("unrelated", id) {
			t.Fatal("unrelated session suppressed issue")
		}
		time.Sleep(time.Second)
		synctest.Wait()
		if len(reports) != 1 {
			t.Fatalf("reports: %v", reports)
		}
		unsubscribe()
		s.update("test", "a.go", []diagnostic{{Message: "different"}}, false)
		time.Sleep(time.Second)
		synctest.Wait()
		if len(reports) != 1 {
			t.Fatal("unsubscribed callback ran")
		}
		id = m.Snapshot("agent")[0].ID
		if !m.Ignore("agent", id) {
			t.Fatal("attached session could not ignore")
		}
	})
}
func TestUnchangedReportsDoNotExtendDebounce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var reports []Report
		s := newIssues("/managed", 2*time.Second, func(r Report) { reports = append(reports, r) })
		defer s.close()
		s.attach("a")
		s.update("test", "a.go", nil, true)
		ds := []diagnostic{{Message: "new"}}
		s.update("test", "a.go", ds, false)
		time.Sleep(time.Second)
		s.update("test", "a.go", ds, false)
		time.Sleep(time.Second)
		synctest.Wait()
		if len(reports) != 1 {
			t.Fatal("repeated report postponed pending notification")
		}
	})
}
