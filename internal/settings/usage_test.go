package settings

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// openWithScripts opens a store whose usage script defaults are defaults.
func openWithScripts(t *testing.T, defaults map[string]string, validate func(name, text string) error) (*Store, string, *changeLog) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMO_HOME", home)
	log := &changeLog{}
	s, err := Open(home, Options{UsageDefaults: defaults, ValidateUsageScript: validate, OnChange: log.add})
	if err != nil {
		t.Fatal(err)
	}
	return s, home, log
}

func pct(v float64) *float64 { return &v }

func TestUsageDefaultsAndBounds(t *testing.T) {
	s, home, _ := openTest(t)
	u := s.General().Usage
	if u.SoftPercent != 90 || u.HardPercent != 98 || time.Duration(u.RefreshInterval) != 5*time.Minute || u.Windows == nil || len(u.Windows) != 0 {
		t.Fatalf("usage defaults = %+v", u)
	}
	raw, _ := os.ReadFile(filepath.Join(home, "settings.yaml"))
	if !strings.Contains(string(raw), "usage:\n  soft_percent: 90\n  hard_percent: 98\n  refresh_interval: 5m\n") {
		t.Fatalf("usage not written:\n%s", raw)
	}

	tooMany := map[string]api.UsageThreshold{}
	for i := range 33 {
		tooMany[strings.Repeat("w", 1)+string(rune('a'+i%26))+strings.Repeat("x", i/26)] = api.UsageThreshold{}
	}
	cases := map[string]func(*api.UsageSettings){
		"usage.soft_percent":      func(u *api.UsageSettings) { u.SoftPercent = 0 },
		"usage.soft_percent ":     func(u *api.UsageSettings) { u.SoftPercent = math.NaN() },
		"usage.hard_percent":      func(u *api.UsageSettings) { u.HardPercent = 80 },
		"usage.hard_percent ":     func(u *api.UsageSettings) { u.HardPercent = 101 },
		"usage.refresh_interval":  func(u *api.UsageSettings) { u.RefreshInterval = Duration(30 * time.Second) },
		"usage.refresh_interval ": func(u *api.UsageSettings) { u.RefreshInterval = Duration(2 * time.Hour) },
		"usage.windows":           func(u *api.UsageSettings) { u.Windows = tooMany },
		"usage.windows ":          func(u *api.UsageSettings) { u.Windows = map[string]api.UsageThreshold{"Bad Name": {}} },
		"usage.windows.session.soft_percent": func(u *api.UsageSettings) {
			u.Windows = map[string]api.UsageThreshold{"session": {SoftPercent: pct(0)}}
		},
		"usage.windows.session.hard_percent": func(u *api.UsageSettings) {
			u.Windows = map[string]api.UsageThreshold{"session": {HardPercent: pct(85)}}
		},
		"usage.windows.weekly.hard_percent": func(u *api.UsageSettings) {
			u.Windows = map[string]api.UsageThreshold{"weekly": {SoftPercent: pct(50), HardPercent: pct(40)}}
		},
		"usage.windows.weekly.soft_percent": func(u *api.UsageSettings) {
			u.Windows = map[string]api.UsageThreshold{"weekly": {SoftPercent: pct(99)}}
		},
		"usage.windows.fable_weekly.hard_percent ": func(u *api.UsageSettings) {
			u.Windows = map[string]api.UsageThreshold{"fable_weekly": {HardPercent: pct(100.5)}}
		},
	}
	for name, mutate := range cases {
		g := Defaults()
		mutate(&g.Usage)
		err := s.SetGeneral(g)
		want := strings.TrimSpace(name)
		if fieldOf(err) != want {
			t.Errorf("%s: err = %v (field %q)", name, err, fieldOf(err))
		}
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.CodeInvalidSettings {
			t.Errorf("%s: err = %v, want invalid_settings", name, err)
		}
	}
	// Accepted: overrides with either value left out, a window soft above
	// the general soft but below the general hard, the bounds themselves.
	g := Defaults()
	g.Usage = api.UsageSettings{SoftPercent: 1, HardPercent: 100, RefreshInterval: Duration(time.Hour), Windows: map[string]api.UsageThreshold{
		"session":      {SoftPercent: pct(80)},
		"weekly":       {HardPercent: pct(95)},
		"fable_weekly": {SoftPercent: pct(50), HardPercent: pct(50)},
	}}
	if err := s.SetGeneral(g); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.General().Usage, g.Usage) {
		t.Fatalf("usage = %+v", s.General().Usage)
	}
	// The store's copy is not shared with callers.
	got := s.General()
	*got.Usage.Windows["session"].SoftPercent = 1
	if *s.General().Usage.Windows["session"].SoftPercent != 80 {
		t.Fatal("caller changed the store")
	}
	// A hand edit out of bounds is a file error naming the key.
	writeFile(t, filepath.Join(home, "settings.yaml"), "usage:\n  soft_percent: 95\n  hard_percent: 90\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 1 || !strings.Contains(errs[0].Message, "usage.hard_percent") {
		t.Fatalf("errors = %+v", errs)
	}
	// Keys a file leaves out keep their defaults.
	writeFile(t, filepath.Join(home, "settings.yaml"), "usage:\n  soft_percent: 80\n")
	s.Reload()
	if u := s.General().Usage; u.SoftPercent != 80 || u.HardPercent != 98 || time.Duration(u.RefreshInterval) != 5*time.Minute || u.Windows == nil {
		t.Fatalf("usage = %+v", u)
	}
}

func TestUsageScriptsLikePrompts(t *testing.T) {
	s, home, log := openWithScripts(t, map[string]string{"claude": "function usage() return {} end\n"}, nil)
	list := s.UsageScripts()
	if len(list) != 1 || list[0].Name != "claude" || !list[0].HasDefault || list[0].Customized || list[0].Revision != NoRevision || list[0].UsedBy == nil {
		t.Fatalf("scripts = %+v", list)
	}
	text, custom, ok := s.UsageScript("claude")
	if !strings.HasPrefix(text, "function usage") || custom || !ok {
		t.Fatalf("UsageScript = %q %v %v", text, custom, ok)
	}
	// Customize with create-only, then a stale create is refused.
	if err := s.PutUsageScriptIf(NoRevision, "claude", "-- mine\nfunction usage() return {} end\n"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.PutUsageScriptIf(NoRevision, "claude", "-- again\n"), api.CodeStaleSettings)
	path := filepath.Join(home, "usage", "claude.lua")
	if b, err := os.ReadFile(path); err != nil || !strings.HasPrefix(string(b), "-- mine") {
		t.Fatalf("file = %q, %v", b, err)
	}
	list = s.UsageScripts()
	if !list[0].Customized || list[0].Revision == NoRevision || list[0].Revision != s.Revision(SectionUsage, "claude") {
		t.Fatalf("scripts = %+v", list)
	}
	if def, ok := s.UsageDefault("claude"); !ok || !strings.HasPrefix(def, "function usage") {
		t.Fatalf("default = %q %v", def, ok)
	}
	// Reset: the default again.
	if err := s.ResetUsageScriptIf(s.Revision(SectionUsage, "claude"), "claude"); err != nil {
		t.Fatal(err)
	}
	if _, custom, _ := s.UsageScript("claude"); custom || fileExists(path) {
		t.Fatal("still customized")
	}
	if err := s.ResetUsageScriptIf("", "claude"); err != nil {
		t.Fatalf("resetting a default: %v", err)
	}
	// A script without a default: created, listed, deleted.
	if err := s.PutUsageScriptIf("", "fake", "function usage() return {} end\n"); err != nil {
		t.Fatal(err)
	}
	if list = s.UsageScripts(); len(list) != 2 || list[1].Name != "fake" || list[1].HasDefault || !list[1].Customized {
		t.Fatalf("scripts = %+v", list)
	}
	if err := s.ResetUsageScriptIf("", "fake"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.UsageScript("fake"); ok {
		t.Fatal("deleted script still there")
	}
	wantCode(t, s.ResetUsageScriptIf("", "fake"), api.CodeNotFound)
	wantCode(t, s.ResetUsageScriptIf("", "../x"), api.CodeNotFound)
	changes := log.take()
	if len(changes) != 4 || changes[0].Section != SectionUsage || changes[3].Name != "fake" || !changes[3].Removed {
		t.Fatalf("changes = %+v", changes)
	}
	// A hand-written script is found by the poll, marked as from disk.
	writeFile(t, filepath.Join(home, "usage", "hand.lua"), "function usage() return {} end\n")
	if ch := s.Reload(); len(ch) != 1 || ch[0].Section != SectionUsage || ch[0].Name != "hand" || !ch[0].FromDisk {
		t.Fatalf("reload = %+v", ch)
	}
	// Bounds of the text and the name.
	for field, write := range map[string]func() error{
		"name":   func() error { return s.PutUsageScriptIf("", "Bad", "x") },
		"text":   func() error { return s.PutUsageScriptIf("", "big", strings.Repeat("-", 64<<10+1)) },
		"text ":  func() error { return s.PutUsageScriptIf("", "blank", " \n") },
		"text  ": func() error { return s.PutUsageScriptIf("", "utf", "\xff") },
	} {
		err := write()
		wantCode(t, err, api.CodeValidation)
		if fieldOf(err) != strings.TrimSpace(field) {
			t.Errorf("%q: field = %q", field, fieldOf(err))
		}
	}
	// A hand-written script with a hidden character is broken, the
	// script stays out.
	writeFile(t, filepath.Join(home, "usage", "bidi.lua"), "-- \u202e\nfunction usage() end\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 1 || !strings.HasSuffix(errs[0].File, "bidi.lua") {
		t.Fatalf("errors = %+v", errs)
	}
	if _, _, ok := s.UsageScript("bidi"); ok {
		t.Fatal("broken script loaded")
	}
	// CRLF line ends and tabs are fine.
	if err := s.PutUsageScriptIf("", "crlf", "function usage()\r\n\treturn {}\r\nend\r\n"); err != nil {
		t.Fatal(err)
	}
}

func TestUsageScriptInUse(t *testing.T) {
	s, _, _ := openWithScripts(t, map[string]string{"claude": "x"}, nil)
	if err := s.PutUsageScriptIf("", "fake", "function usage() end"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b", "a"} {
		p := fakeProfile(name)
		p.UsageScript = "fake"
		if err := s.PutProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	err := s.ResetUsageScriptIf("", "fake")
	wantCode(t, err, api.CodeInUse)
	if !strings.Contains(err.Error(), "a, b") {
		t.Fatalf("err = %v", err)
	}
	if info := s.UsageScripts(); !reflect.DeepEqual(info[1].UsedBy, []string{"a", "b"}) {
		t.Fatalf("used by = %+v", info)
	}
	// A script with a default may be reset while in use: the default
	// stays.
	p := fakeProfile("c")
	p.UsageScript = "claude"
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if err := s.PutUsageScriptIf("", "claude", "y"); err != nil {
		t.Fatal(err)
	}
	if err := s.ResetUsageScriptIf("", "claude"); err != nil {
		t.Fatal(err)
	}
}

func TestUsageScriptValidated(t *testing.T) {
	var called []string
	validate := func(name, text string) error {
		called = append(called, name)
		if strings.Contains(text, "broken") {
			return errors.New("line 1: the script does not compile")
		}
		return nil
	}
	s, home, log := openWithScripts(t, nil, validate)
	err := s.PutUsageScriptIf("", "fake", "broken")
	wantCode(t, err, api.CodeValidation)
	if fieldOf(err) != "text" || !strings.Contains(err.Error(), "does not compile") {
		t.Fatalf("err = %v", err)
	}
	if fileExists(filepath.Join(home, "usage", "fake.lua")) || len(log.take()) != 0 {
		t.Fatal("refused script was written")
	}
	if err := s.PutUsageScriptIf("", "fake", "function usage() end"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(called, []string{"fake", "fake"}) {
		t.Fatalf("validator calls = %v", called)
	}
	// Hand-written files are not compiled by the store.
	writeFile(t, filepath.Join(home, "usage", "hand.lua"), "broken")
	s.Reload()
	if _, _, ok := s.UsageScript("hand"); !ok || len(s.Errors()) != 0 {
		t.Fatalf("hand-written script: errors %+v", s.Errors())
	}
}

func TestProfileNamesExistingScript(t *testing.T) {
	s, home, _ := openWithScripts(t, map[string]string{"claude": "x"}, nil)
	p := fakeProfile("x")
	p.UsageScript = "nope"
	err := s.PutProfile(p)
	wantCode(t, err, api.CodeValidation)
	if fieldOf(err) != "usage_script" {
		t.Fatalf("err = %v", err)
	}
	if fileExists(filepath.Join(home, "profiles", "x.yaml")) {
		t.Fatal("refused profile was written")
	}
	p.UsageScript = "claude"
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	p.UsageScript = ""
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if err := s.PutUsageScriptIf("", "mine", "x"); err != nil {
		t.Fatal(err)
	}
	p.UsageScript = "mine"
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	// At load, a missing script is no error: the tracker shows it.
	writeFile(t, filepath.Join(home, "profiles", "y.yaml"), "cli: custom\ncommand: x\nusage_script: gone\nmodels:\n  a: {}\n")
	s.Reload()
	if pr, ok := s.Profile("y"); !ok || pr.UsageScript != "gone" || len(s.Errors()) != 0 {
		t.Fatalf("profile y = %+v %v, errors %+v", pr, ok, s.Errors())
	}
	// A store without script defaults does not check the name.
	s2, _, _ := openTest(t)
	p.UsageScript = "anything"
	if err := s2.PutProfile(p); err != nil {
		t.Fatal(err)
	}
}

func TestModelUsageWindowsValidated(t *testing.T) {
	s, home, _ := openTest(t)
	many := make([]string, 17)
	for i := range many {
		many[i] = "w" + strings.Repeat("x", i)
	}
	for field, windows := range map[string][]string{
		"models.a.usage_windows":   many,
		"models.a.usage_windows.1": {"session", "Weekly"},
		"models.a.usage_windows.2": {"session", "weekly", "session"},
		"models.a.usage_windows.0": {strings.Repeat("w", 41)},
	} {
		p := fakeProfile("x")
		p.Models = map[string]Model{"a": {UsageWindows: windows}}
		err := s.PutProfile(p)
		wantCode(t, err, api.CodeValidation)
		if fieldOf(err) != field {
			t.Errorf("%s: err = %v", field, err)
		}
	}
	p := fakeProfile("x")
	p.Models = map[string]Model{"a": {UsageWindows: []string{"session", "weekly", "fable_weekly", strings.Repeat("w", 40)}}}
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "profiles", "y.yaml"), "cli: custom\ncommand: x\nmodels:\n  a:\n    usage_windows: [a, a]\n")
	s.Reload()
	if errs := s.Errors(); len(errs) != 1 || !strings.Contains(errs[0].Message, "models.a.usage_windows.1") {
		t.Fatalf("errors = %+v", errs)
	}
}
