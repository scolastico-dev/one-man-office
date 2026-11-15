package session

import (
	"strings"
	"testing"
)

const fakeSecret = "fake-secret-0123456789"

func feedAll(r *redactor, chunks ...string) string {
	var out strings.Builder
	for _, chunk := range chunks {
		out.Write(r.feed([]byte(chunk)))
	}
	out.Write(r.flush())
	return out.String()
}

func TestRedactorAcrossChunks(t *testing.T) {
	for name, chunks := range map[string][]string{
		"one write": {"id=" + fakeSecret + "\n"},
		"split":     {"id=" + fakeSecret[:7], fakeSecret[7:] + "\n"},
		"bytewise":  strings.Split("id="+fakeSecret+"\n", ""),
		"many":      {strings.Repeat(fakeSecret+" ", 50) + fakeSecret[:3], fakeSecret[3:]},
		"overlap":   {"fake-" + fakeSecret[:5], fakeSecret[5:]},
	} {
		got := feedAll(newRedactor(fakeSecret), chunks...)
		if strings.Contains(got, fakeSecret) {
			t.Errorf("%s: secret survived: %q", name, got)
		}
		if !strings.Contains(got, RedactedPlaceholder) {
			t.Errorf("%s: no placeholder in %q", name, got)
		}
	}
	// Text that only looks like the start of the secret comes out unchanged.
	if got := feedAll(newRedactor(fakeSecret), "fake-sec", "ond half"); got != "fake-second half" {
		t.Fatalf("partial match = %q", got)
	}
	r := newRedactor(fakeSecret)
	if out := string(r.feed([]byte("abc fake-se"))); out != "abc " {
		t.Fatalf("kept back wrongly: %q", out)
	}
	if len(r.pending) >= len(fakeSecret) {
		t.Fatalf("kept back %d bytes", len(r.pending))
	}
}
