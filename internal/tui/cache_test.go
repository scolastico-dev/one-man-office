package tui

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/office"
	"github.com/scolastico-dev/one-man-office/internal/supervisor"
)

func TestRefreshIntervalsKeepPeekResponsiveAndThrottleOverview(t *testing.T) {
	if got := refreshInterval(modePeek); got != 100*time.Millisecond {
		t.Fatalf("peek refresh = %s", got)
	}
	for _, current := range []mode{modeOverview, modeDetail, modeComposeMessage, modeCommandConsole} {
		if got := refreshInterval(current); got != 500*time.Millisecond {
			t.Fatalf("mode %d refresh = %s", current, got)
		}
	}
}

func BenchmarkEventHistoryOverview(b *testing.B) {
	database, err := db.Open(filepath.Join(b.TempDir(), "omo.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer database.Close()
	tx, err := database.Begin()
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`INSERT INTO events(kind, detail) VALUES('benchmark', ?)`)
	if err != nil {
		b.Fatal(err)
	}
	for i := range 5000 {
		if _, err := statement.Exec(fmt.Sprintf("event %d", i)); err != nil {
			b.Fatal(err)
		}
	}
	statement.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	cfg := config.Defaults()
	sup := supervisor.New(&cfg, database, gitops.New(), b.TempDir(), nil)
	m := model{o: &office.Office{DB: database, Sup: sup}, mode: modeOverview, tab: tabEvents, w: 160, h: 40, cache: &viewCache{}}
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func TestViewCacheReusesHistoryAndFooterSnapshots(t *testing.T) {
	m := testModel(t)
	m.cache = &viewCache{}
	if got := len(m.messageHistory()); got != 0 {
		t.Fatalf("initial messages = %d", got)
	}
	if _, err := m.o.DB.Exec(`INSERT INTO messages(from_agent, to_target, subject, body) VALUES('omo','user','new','body')`); err != nil {
		t.Fatal(err)
	}
	if got := len(m.messageHistory()); got != 0 {
		t.Fatalf("same-view history was requeried: %d messages", got)
	}
	m.cache.beginView()
	if got := len(m.messageHistory()); got != 1 {
		t.Fatalf("next-view messages = %d", got)
	}

	unread, _ := m.footerSnapshot()
	if unread != 1 {
		t.Fatalf("footer unread = %d", unread)
	}
	if _, err := m.o.DB.Exec(`INSERT INTO messages(from_agent, to_target, subject, body) VALUES('omo','user','newer','body')`); err != nil {
		t.Fatal(err)
	}
	if unread, _ := m.footerSnapshot(); unread != 1 {
		t.Fatalf("footer cache missed: unread = %d", unread)
	}
	m.cache.footerAt = time.Now().Add(-time.Second)
	if unread, _ := m.footerSnapshot(); unread != 2 {
		t.Fatalf("expired footer did not refresh: unread = %d", unread)
	}
}
