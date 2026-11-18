package engine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

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
	// doubled past SQLite's path limit. The reason still exceeds 200 runes.
	root := base
	for len(root) < 220 {
		root = filepath.Join(root, strings.Repeat("long-path-", 4))
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, root)
	}
}

// wantReasonSummary checks the summary contract separately from assertions on
// the full comment. These fixtures contain plain text; whitespace in paths is
// collapsed for display while the full comment preserves it exactly.
func wantReasonSummary(t *testing.T, got, full string) {
	t.Helper()
	want := strings.Join(strings.Fields(full), " ")
	if rs := []rune(want); len(rs) > 200 {
		want = strings.TrimSpace(string(rs[:199])) + "…"
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
