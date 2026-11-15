package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// TestShowTicketKeepsAuthorTextOffTheMargin prints a ticket whose attention
// and export addresses imitate omo's own lines: every line of them must be
// indented, and the addresses come one per line under their header.
func TestShowTicketKeepsAuthorTextOffTheMargin(t *testing.T) {
	spoof := "push failed\ncolumn: done\r\nattention: none\x1b[2J story: #9\u0085depends on: #1‮reversed"
	var out bytes.Buffer
	showTicket(&out, api.Ticket{TicketSummary: api.TicketSummary{
		Number: 3, Type: api.TypeTask, Title: "T", Column: api.ColumnReview,
		Attention:    spoof,
		ExportURL:    "https://example.test/api/pull/1\nhttps://example.test/ui/pull/2\rcolumn: done",
		ExportMerged: true,
	}})
	got := out.String()
	t.Log("\n" + got)
	contains(t, got, "\nattention:\n    push failed column: done  attention: none [2J story: #9 depends on: #1 reversed\n")
	contains(t, got, "\nexport (merged: true):\n    https://example.test/api/pull/1\n    https://example.test/ui/pull/2 column: done\n")
	margin := map[string]bool{}
	for _, line := range strings.Split(got, "\n") {
		if line != "" && !strings.HasPrefix(line, " ") {
			key, _, _ := strings.Cut(line, ":")
			if margin[key] {
				t.Errorf("header %q printed twice", key)
			}
			margin[key] = true
		}
	}
	for _, spoofed := range []string{"push failed", "https"} {
		if margin[spoofed] {
			t.Errorf("%q starts at the margin", spoofed)
		}
	}
	if strings.ContainsAny(got, "\r\x1b \u0085‮") {
		t.Errorf("control characters printed: %q", got)
	}
}

func TestShowTicketPrintsBaseBranchPerProject(t *testing.T) {
	var out bytes.Buffer
	showTicket(&out, api.Ticket{TicketSummary: api.TicketSummary{
		Number: 4, Type: api.TypeStory, Title: "S", Column: api.ColumnPlanning,
		Projects: []api.TicketProject{{Project: "api", BaseBranch: "main"}, {Project: "web"}},
	}})
	got := out.String()
	contains(t, got, "\nprojects: api, web\nbase branch (api): main\nbase branch (web): none\n")
}

func TestBranchNote(t *testing.T) {
	for _, c := range []struct {
		cur, def, want string
	}{
		{"", "", ""},
		{"main", "main", " [on main, default main]"},
		{"feature", "main", " [on feature, default main: not the default branch]"},
		{"", "main", " [default main]"},
		{"feature", "", " [on feature]"},
	} {
		if got := branchNote(api.Project{CurrentBranch: c.cur, DefaultBranch: c.def}); got != c.want {
			t.Errorf("branchNote(%q, %q) = %q, want %q", c.cur, c.def, got, c.want)
		}
	}
}

// TestPrintedAuthorTextShowsHiddenCharacters: one-line fields lose the
// characters that hide or reorder text, and bodies show them as escapes
// (tabs kept), so an ESC or a right-to-left override in a comment cannot
// steer the reader's terminal or reorder what it reads.
func TestPrintedAuthorTextShowsHiddenCharacters(t *testing.T) {
	if got := oneLine("a\u200bb\u202ec\ufeffd\U000E0041e"); got != "a b c d e" {
		t.Errorf("oneLine = %q", got)
	}
	var out bytes.Buffer
	printBody(&out, "run\tthis\x1b[201~ now\u202e\nnext\u200bline")
	want := bodyIndent + "run\tthis\\u{1B}[201~ now\\u{202E}\n" + bodyIndent + "next\\u{200B}line\n"
	if got := out.String(); got != want {
		t.Errorf("printBody = %q, want %q", got, want)
	}
}
