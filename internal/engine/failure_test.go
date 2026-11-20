package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// summaryRunes is the length a reason's summary is cut to.
const summaryRunes = 200

// longTempRootLength is how long useLongTempRoot makes the temporary root.
// On Windows a program cannot be started in a folder whose path is longer
// than 258 characters, and git opens no file beyond 260. The deepest file
// these tests make lies about 160 characters below the root, so the root
// stays at 80 there; the reasons, which carry a path below the root, are
// still longer than a summary (wantReasonSummary checks that).
func longTempRootLength() int {
	if runtime.GOOS == "windows" {
		return 80
	}
	return 220
}

// longReasons is set while a test runs under useLongTempRoot: every reason
// it checks must then be longer than a summary, or the test would no longer
// exercise the cut it is there for. Tests of this package do not run in
// parallel.
var longReasons bool

// useLongTempRoot makes path-bearing reasons exceed the card/project summary
// budget, even on machines with a short default temporary directory.
func useLongTempRoot(t *testing.T) {
	t.Helper()
	base, err := os.MkdirTemp("", "long reasons  -")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(base); err != nil {
			t.Error(err)
		}
	})
	// Extend only as far as needed: an already long TMPDIR must not be
	// doubled past SQLite's path limit.
	root := base
	for want := longTempRootLength(); len(root) < want-1; {
		segment := strings.Repeat("long-path-", 4)
		if room := want - len(root) - 1; room < len(segment) {
			segment = segment[:room]
		}
		root = filepath.Join(root, segment)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	// testing.T.TempDir uses GOTMPDIR independently of the platform's
	// os.TempDir variables. Override both before the first t.TempDir call.
	for _, key := range []string{"GOTMPDIR", "TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, root)
	}
	longReasons = true
	t.Cleanup(func() { longReasons = false })
}

// wantReasonSummary checks the summary contract separately from assertions on
// the full comment. These fixtures contain plain text; whitespace in paths is
// collapsed for display while the full comment preserves it exactly.
func wantReasonSummary(t *testing.T, got, full string) {
	t.Helper()
	want := strings.Join(strings.Fields(full), " ")
	if rs := []rune(want); len(rs) > summaryRunes {
		want = strings.TrimSpace(string(rs[:summaryRunes-1])) + "…"
	} else if longReasons {
		t.Fatalf("the reason has %d runes, no more than a summary holds (%d), although the test runs under a long temporary root: %q", len(rs), summaryRunes, full)
	}
	if got != want {
		t.Fatalf("summary %q, want %q (full reason %q)", got, want, full)
	}
}

func (h *harness) wantAttentionReason(n int64, reason string) {
	h.t.Helper()
	want := "Needs attention: " + reason
	if !slices.Contains(h.comments(n), want) {
		h.t.Fatalf("missing full failure comment %q; comments: %q", want, h.comments(n))
	}
	wantReasonSummary(h.t, h.ticket(n).Attention, reason)
}

// exportFailureReason reads the authoritative comment, never either bounded
// summary. The caller still asserts the specific refusal reason it expects.
func (h *harness) exportFailureReason(n int64, project string) string {
	h.t.Helper()
	prefix := "Export failed: Export of " + project + " failed: "
	var reasons []string
	for _, comment := range h.comments(n) {
		if reason, ok := strings.CutPrefix(comment, prefix); ok {
			reasons = append(reasons, reason)
		}
	}
	if len(reasons) != 1 {
		h.t.Fatalf("got %d full export failure comments for %s, want one: %q", len(reasons), project, h.comments(n))
	}
	return reasons[0]
}
