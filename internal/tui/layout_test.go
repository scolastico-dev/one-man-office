package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestPlaceFooterUsesFinalTerminalRow(t *testing.T) {
	view := placeFooter("header\nrow", " FOOTER ", 20, 7)
	lines := strings.Split(view, "\n")
	if len(lines) != 7 {
		t.Fatalf("view has %d rows, want terminal height 7:\n%q", len(lines), view)
	}
	if !strings.Contains(lines[len(lines)-1], "FOOTER") {
		t.Fatalf("footer is not on final row: %q", lines[len(lines)-1])
	}
}

func TestPlaceFooterClipsWideAndTallOverview(t *testing.T) {
	content := strings.Repeat("x", 80) + "\nsecond\nthird\nfourth\nfifth"
	view := placeFooter(content, "footer", 12, 4)
	lines := strings.Split(view, "\n")
	if len(lines) != 4 || lines[3] != "footer" {
		t.Fatalf("unexpected fitted view: %#v", lines)
	}
	for i, line := range lines {
		if width := ansi.StringWidth(line); width > 12 {
			t.Errorf("row %d width = %d, want <= 12: %q", i, width, line)
		}
	}
}

func TestOverviewTabHitsClipToTheWindow(t *testing.T) {
	m := testModel(t)
	m.w, m.h = 12, 5
	_ = m.View()

	var tabs []hitRect
	for _, rect := range m.hitMap.rects {
		if _, ok := rect.action.(tabAction); ok {
			tabs = append(tabs, rect)
		}
		if rect.x < 0 || rect.y < 0 || rect.x+rect.w > m.w || rect.y+rect.h > m.h {
			t.Fatalf("hit rectangle out of window bounds: %+v in %dx%d", rect, m.w, m.h)
		}
	}
	if len(tabs) != 2 || tabs[0].w != 8 || tabs[1].x != 8 || tabs[1].w != 4 {
		t.Fatalf("clipped tab hits = %+v, want Agents and clipped Messages", tabs)
	}
}
