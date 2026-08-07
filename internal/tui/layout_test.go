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
