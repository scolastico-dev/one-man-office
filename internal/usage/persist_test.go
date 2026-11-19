package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// extraBadFile makes a named pipe where the platform has them.
var extraBadFile func(path string)

func writeKept(t *testing.T, path string, f keptFile) {
	t.Helper()
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPersistRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	in := []keptAccount{{Key: "0123456789abcdef", Script: "claude", FetchedAt: t0,
		Windows: []api.UsageWindow{winAt("session", 42.5, t0.Add(time.Hour)), win("weekly", 12)}}}
	if err := saveKept(path, in); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	got, why := loadKept(path, t0.Add(time.Minute))
	if why != "" {
		t.Fatal(why)
	}
	a, ok := got["0123456789abcdef"]
	if !ok || a.Script != "claude" || !a.FetchedAt.Equal(t0) || len(a.Windows) != 2 || a.Windows[0].UsedPercent != 42.5 || !a.Windows[0].ResetsAt.Equal(t0.Add(time.Hour)) || a.Windows[1].ResetsAt != nil {
		t.Fatalf("got %+v", got)
	}
	// No temporary file is left next to it.
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("files %v", entries)
	}
}

// A window's label is kept with it.
func TestPersistLabels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	labelled := winAt("fable_weekly", 43, t0.Add(time.Hour))
	labelled.Label = "Weekly, Fable"
	if err := saveKept(path, []keptAccount{{Key: "0123456789abcdef", Script: "claude", FetchedAt: t0, Windows: []api.UsageWindow{labelled, win("weekly", 12)}}}); err != nil {
		t.Fatal(err)
	}
	got, why := loadKept(path, t0.Add(time.Minute))
	if a := got["0123456789abcdef"]; why != "" || len(a.Windows) != 2 || a.Windows[0].Label != "Weekly, Fable" || a.Windows[1].Label != "" {
		t.Fatalf("got %+v, %q", got, why)
	}

	// A label edited by hand is cleaned as a script's would be.
	edited := `{"accounts":[{"key":"0123456789abcdef","script":"claude","windows":[{"name":"session","label":"Session\n\u001b[2J` + strings.Repeat("x", 80) + `","used_percent":1,"resets_at":null}],"fetched_at":"2026-11-15T12:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ = loadKept(path, t0.Add(time.Minute))
	if l := got["0123456789abcdef"].Windows[0].Label; l != "Session [2J"+strings.Repeat("x", 49) {
		t.Fatalf("edited label = %q", l)
	}
}

func TestPersistMissingFile(t *testing.T) {
	got, why := loadKept(filepath.Join(t.TempDir(), "usage.json"), t0)
	if why != "" || len(got) != 0 || got == nil {
		t.Fatalf("got %v %q", got, why)
	}
	got, why = loadKept("", t0)
	if why != "" || got == nil {
		t.Fatalf("no path: %v %q", got, why)
	}
}

func TestPersistIgnoresBadFiles(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-ant-kept-secret"
	cases := map[string]struct {
		make func(path string)
		why  string
	}{
		"corrupt":    {func(p string) { os.WriteFile(p, []byte(`{"accounts": [`+secret), 0o600) }, "the file is not valid"},
		"wrong type": {func(p string) { os.WriteFile(p, []byte(`{"accounts": "`+secret+`"}`), 0o600) }, "the file is not valid"},
		"too large": {func(p string) {
			os.WriteFile(p, []byte(`{"accounts":[],"x":"`+strings.Repeat("a", maxStateFile)+`"}`), 0o600)
		}, "the file is larger than 1 MiB"},
		"folder": {func(p string) { os.Mkdir(p, 0o700) }, "the file is not a regular file"},
		"link": {func(p string) {
			target := filepath.Join(dir, "target-"+filepath.Base(p))
			writeKept(t, target, keptFile{Accounts: []keptAccount{{Key: "0123456789abcdef", Script: "s", FetchedAt: t0}}})
			os.Symlink(target, p)
		}, "the file is a link"},
	}
	if extraBadFile != nil {
		cases["named pipe"] = struct {
			make func(path string)
			why  string
		}{extraBadFile, "the file is not a regular file"}
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
			c.make(path)
			done := make(chan struct{})
			var got map[string]keptAccount
			var why string
			go func() {
				got, why = loadKept(path, t0)
				close(done)
			}()
			<-done
			if len(got) != 0 || got == nil {
				t.Errorf("got %v", got)
			}
			if !strings.HasPrefix(why, c.why) {
				t.Errorf("why %q, want %q", why, c.why)
			}
			if strings.Contains(why, secret) || strings.Contains(why, path) {
				t.Errorf("message holds content or path: %q", why)
			}
		})
	}
}

func TestPersistDropsBadEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	now := t0.Add(time.Hour)
	good := func(key string, ws ...api.UsageWindow) keptAccount {
		if ws == nil {
			ws = []api.UsageWindow{win("a", 50)}
		}
		return keptAccount{Key: key, Script: "s", FetchedAt: t0, Windows: ws}
	}
	many := make([]api.UsageWindow, MaxWindows+1)
	for i := range many {
		many[i] = win(strings.Repeat("w", i+1), 1)
	}
	old := good("1111111111111111")
	old.FetchedAt = now.Add(-MaxReadingAge - time.Second)
	zero := good("2222222222222222")
	zero.FetchedAt = time.Time{}
	writeKept(t, path, keptFile{Accounts: []keptAccount{
		good("0123456789abcdef", winAt("passed", 99, now.Add(-time.Second)), winAt("ahead", 50, now.Add(time.Minute)), winAt("far", 70, now.Add(MaxResetAhead+time.Hour)), win("plain", 10)),
		good("0123456789abcdef", win("dup", 1)), // second entry of a key
		good("0123456789ABCDEF"),
		good("short"),
		good("../../etc/passwd0"),
		old,
		zero,
		good("3333333333333333", win("Bad", 1)),
		good("4444444444444444", win("a", 1), win("a", 2)),
		good("5555555555555555", win("a", 101)),
		good("6666666666666666", win("a", -1)),
		good("7777777777777777", many...),
	}})
	got, why := loadKept(path, now)
	if why != "" {
		t.Fatal(why)
	}
	if len(got) != 1 {
		t.Fatalf("kept %d entries: %+v", len(got), got)
	}
	a := got["0123456789abcdef"]
	names := []string{}
	for _, w := range a.Windows {
		names = append(names, w.Name)
	}
	if strings.Join(names, ",") != "ahead,far,plain" {
		t.Errorf("windows %v", names)
	}
	if a.Windows[1].ResetsAt != nil {
		t.Errorf("a reset beyond the bound kept: %v", a.Windows[1].ResetsAt)
	}
}

// TestPersistNaNInFile pins that a hand-written NaN makes the file invalid.
func TestPersistNaNInFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	os.WriteFile(path, []byte(`{"accounts":[{"key":"0123456789abcdef","script":"s","fetched_at":"2026-11-15T12:00:00Z","windows":[{"name":"a","used_percent":NaN}]}]}`), 0o600)
	got, why := loadKept(path, t0)
	if why != "the file is not valid" || len(got) != 0 {
		t.Fatalf("got %v %q", got, why)
	}
}
