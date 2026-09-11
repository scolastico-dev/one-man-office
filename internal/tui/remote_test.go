package tui

import "testing"

func TestRemoteTUIStateSwitchesPeekAndOverviewLikeLocalSelection(t *testing.T) {
	m := testModel(t)
	m.w, m.h = 120, 30

	updated, cmd := m.Update(tuiStateMsg{mode: "peek", peek: "developer-ada"})
	m = updated.(model)
	if cmd != nil || m.mode != modePeek || m.peek != "developer-ada" || !m.readOnly {
		t.Fatalf("remote peek state = mode=%v peek=%q readOnly=%v cmd=%v", m.mode, m.peek, m.readOnly, cmd)
	}

	updated, cmd = m.Update(tuiStateMsg{mode: "overview"})
	m = updated.(model)
	if cmd == nil || m.mode != modeOverview || m.peek != "" {
		t.Fatalf("remote overview state = mode=%v peek=%q cmd=%v", m.mode, m.peek, cmd)
	}
}

func TestRemoteTUIStateIsIgnoredByObserver(t *testing.T) {
	m := testModel(t)
	m.observer = true
	updated, cmd := m.Update(tuiStateMsg{mode: "peek", peek: "developer-ada"})
	got := updated.(model)
	if cmd != nil || got.mode != modeOverview || got.peek != "" {
		t.Fatalf("observer remote state = mode=%v peek=%q cmd=%v", got.mode, got.peek, cmd)
	}
}
