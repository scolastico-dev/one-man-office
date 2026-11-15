package textsafe

import (
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"unicode/utf8"
)

type fixture struct {
	Hidden [][]string `json:"hidden"`
	Shown  []string   `json:"shown"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/hidden.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func hex(t *testing.T, s string) rune {
	t.Helper()
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		t.Fatalf("bad code point %q: %v", s, err)
	}
	return rune(v)
}

// The shared list is the whole rule: every code point in it is hidden and
// every other one is not.
func TestIsHiddenMatchesSharedList(t *testing.T) {
	f := loadFixture(t)
	want := map[rune]bool{}
	for _, r := range f.Hidden {
		if len(r) < 2 {
			t.Fatalf("bad range %v", r)
		}
		for c := hex(t, r[0]); c <= hex(t, r[1]); c++ {
			want[c] = true
		}
	}
	for _, s := range f.Shown {
		if want[hex(t, s)] {
			t.Fatalf("%s is listed as hidden and as shown", s)
		}
	}
	for c := rune(0); c <= utf8.MaxRune; c++ {
		if c >= 0xd800 && c <= 0xdfff {
			continue
		}
		if got := IsHidden(c); got != want[c] {
			t.Errorf("IsHidden(U+%04X) = %v, the shared list says %v", c, got, want[c])
		}
	}
}
