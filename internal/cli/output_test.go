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
