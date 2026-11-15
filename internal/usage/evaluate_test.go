package usage

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

var t0 = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func ptrF(v float64) *float64 { return &v }

func ptrT(v time.Time) *time.Time { return &v }

func reading(ws ...api.UsageWindow) *api.UsageReading {
	if ws == nil {
		ws = []api.UsageWindow{}
	}
	return &api.UsageReading{Key: "0123456789abcdef", Script: "s", Profiles: []string{"p"}, Windows: ws, FetchedAt: ptrT(t0)}
}

func win(name string, used float64) api.UsageWindow {
	return api.UsageWindow{Name: name, UsedPercent: used}
}

func winAt(name string, used float64, at time.Time) api.UsageWindow {
	return api.UsageWindow{Name: name, UsedPercent: used, ResetsAt: ptrT(at)}
}

func th(soft, hard float64) api.UsageSettings {
	return api.UsageSettings{SoftPercent: soft, HardPercent: hard, RefreshInterval: api.Duration(5 * time.Minute)}
}

type evalCase struct {
	name    string
	windows []string
	r       *api.UsageReading
	th      api.UsageSettings
	now     time.Time
	state   string
	window  string
	used    *float64
	resets  *time.Time
}

func runEval(t *testing.T, cases []evalCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := c.now
			if now.IsZero() {
				now = t0
			}
			got := Evaluate("p/m", c.windows, c.r, c.th, now)
			if got.Model != "p/m" {
				t.Errorf("model %q", got.Model)
			}
			if got.State != c.state || got.Window != c.window {
				t.Errorf("state %q window %q, want %q %q", got.State, got.Window, c.state, c.window)
			}
			switch {
			case c.used == nil && got.UsedPercent != nil:
				t.Errorf("used %v, want none", *got.UsedPercent)
			case c.used != nil && (got.UsedPercent == nil || *got.UsedPercent != *c.used):
				t.Errorf("used %v, want %v", got.UsedPercent, *c.used)
			}
			switch {
			case c.resets == nil && got.ResetsAt != nil:
				t.Errorf("resets %v, want none", *got.ResetsAt)
			case c.resets != nil && (got.ResetsAt == nil || !got.ResetsAt.Equal(*c.resets)):
				t.Errorf("resets %v, want %v", got.ResetsAt, *c.resets)
			}
		})
	}
}

func TestEvaluateFullestWindow(t *testing.T) {
	runEval(t, []evalCase{
		{name: "b fuller", r: reading(win("a", 40), win("b", 80)), th: th(90, 98), state: api.UsageOK, window: "b", used: ptrF(80)},
		{name: "a fuller", r: reading(win("a", 85), win("b", 10)), th: th(90, 98), state: api.UsageOK, window: "a", used: ptrF(85)},
		{name: "tie takes first name", r: reading(win("b", 50), win("a", 50)), th: th(90, 98), state: api.UsageOK, window: "a", used: ptrF(50)},
		{name: "reset time of the chosen window", r: reading(win("a", 10), winAt("b", 60, t0.Add(time.Hour))), th: th(90, 98), state: api.UsageOK, window: "b", used: ptrF(60), resets: ptrT(t0.Add(time.Hour))},
		{name: "at soft is soft", r: reading(win("a", 90)), th: th(90, 98), state: api.UsageSoft, window: "a", used: ptrF(90)},
		{name: "just below soft is ok", r: reading(win("a", 89.999)), th: th(90, 98), state: api.UsageOK, window: "a", used: ptrF(89.999)},
		{name: "at hard is hard", r: reading(win("a", 98)), th: th(90, 98), state: api.UsageHard, window: "a", used: ptrF(98)},
		{name: "100 is hard", r: reading(win("a", 100)), th: th(90, 98), state: api.UsageHard, window: "a", used: ptrF(100)},
	})
}

func TestEvaluateOnlyCountedWindows(t *testing.T) {
	r := reading(win("a", 20), win("b", 99))
	runEval(t, []evalCase{
		{name: "only a counts", windows: []string{"a"}, r: r, th: th(90, 98), state: api.UsageOK, window: "a", used: ptrF(20)},
		{name: "empty list: all count", windows: []string{}, r: r, th: th(90, 98), state: api.UsageHard, window: "b", used: ptrF(99)},
		{name: "nil list: all count", windows: nil, r: r, th: th(90, 98), state: api.UsageHard, window: "b", used: ptrF(99)},
		{name: "counted window missing from the reading", windows: []string{"c"}, r: r, th: th(90, 98), state: api.UsageUnknown},
		{name: "one of two counted present", windows: []string{"c", "a"}, r: r, th: th(90, 98), state: api.UsageOK, window: "a", used: ptrF(20)},
		{name: "reading without windows", r: reading(), th: th(90, 98), state: api.UsageUnknown},
	})
}

func TestEvaluateThresholdOverrides(t *testing.T) {
	base := th(90, 98)
	base.Windows = map[string]api.UsageThreshold{
		"fable_weekly": {SoftPercent: ptrF(80)},
		"weekly":       {HardPercent: ptrF(95)},
		"odd":          {SoftPercent: ptrF(99)}, // soft above the general hard: hard follows soft
	}
	runEval(t, []evalCase{
		{name: "override soft", r: reading(win("session", 50), win("fable_weekly", 85)), th: base, state: api.UsageSoft, window: "fable_weekly", used: ptrF(85)},
		{name: "global soft elsewhere", r: reading(win("session", 85)), th: base, state: api.UsageOK, window: "session", used: ptrF(85)},
		{name: "override hard", r: reading(win("weekly", 96)), th: base, state: api.UsageHard, window: "weekly", used: ptrF(96)},
		{name: "override soft keeps global hard", r: reading(win("fable_weekly", 97)), th: base, state: api.UsageSoft, window: "fable_weekly", used: ptrF(97)},
		{name: "soft above hard: hard is soft", r: reading(win("odd", 98.5)), th: base, state: api.UsageOK, window: "odd", used: ptrF(98.5)},
		{name: "soft above hard: at soft is hard", r: reading(win("odd", 99)), th: base, state: api.UsageHard, window: "odd", used: ptrF(99)},
		{name: "state explains: a lower window over its own soft wins over a fuller one", r: reading(win("session", 88), win("fable_weekly", 81)), th: base, state: api.UsageSoft, window: "fable_weekly", used: ptrF(81)},
	})
}

func TestEvaluateHardBeatsSoft(t *testing.T) {
	runEval(t, []evalCase{
		{name: "hard and soft windows", r: reading(win("a", 92), win("b", 98)), th: th(90, 98), state: api.UsageHard, window: "b", used: ptrF(98)},
		{name: "hard by override below a fuller soft window", r: reading(win("a", 95), win("b", 70)), th: api.UsageSettings{SoftPercent: 90, HardPercent: 98, Windows: map[string]api.UsageThreshold{"b": {SoftPercent: ptrF(50), HardPercent: ptrF(60)}}}, state: api.UsageHard, window: "b", used: ptrF(70)},
	})
}

func TestEvaluateResetPassed(t *testing.T) {
	runEval(t, []evalCase{
		{name: "passed reset counts as 0", r: reading(winAt("a", 99, t0.Add(-time.Second))), th: th(90, 98), state: api.UsageOK, window: "a", used: ptrF(0)},
		{name: "reset exactly now counts as 0", r: reading(winAt("a", 99, t0)), th: th(90, 98), state: api.UsageOK, window: "a", used: ptrF(0)},
		{name: "reset ahead counts", r: reading(winAt("a", 99, t0.Add(time.Second))), th: th(90, 98), state: api.UsageHard, window: "a", used: ptrF(99), resets: ptrT(t0.Add(time.Second))},
		{name: "other window decides after a reset", r: reading(winAt("a", 99, t0.Add(-time.Minute)), win("b", 91)), th: th(90, 98), state: api.UsageSoft, window: "b", used: ptrF(91)},
		{name: "clock went back: a reset far ahead still counts", r: reading(winAt("a", 99, t0.Add(time.Hour))), th: th(90, 98), now: t0.Add(-24 * time.Hour), state: api.UsageHard, window: "a", used: ptrF(99), resets: ptrT(t0.Add(time.Hour))},
	})
}

func TestEvaluateUnmeteredAndUnknown(t *testing.T) {
	failed := reading(win("a", 99))
	failed.Error = "line 3: boom"
	never := reading()
	never.FetchedAt = nil
	old := reading(win("a", 99))
	old.FetchedAt = ptrT(t0.Add(-MaxReadingAge - time.Second))
	edge := reading(win("a", 99))
	edge.FetchedAt = ptrT(t0.Add(-MaxReadingAge))
	future := reading(win("a", 99))
	future.FetchedAt = ptrT(t0.Add(time.Hour))
	runEval(t, []evalCase{
		{name: "no reading is unmetered", r: nil, th: th(90, 98), state: api.UsageUnmetered},
		{name: "failed run is unknown, even with old windows", r: failed, th: th(90, 98), state: api.UsageUnknown},
		{name: "never answered is unknown", r: never, th: th(90, 98), state: api.UsageUnknown},
		{name: "a reading older than a day is unknown", r: old, th: th(90, 98), state: api.UsageUnknown},
		{name: "a reading exactly a day old still counts", r: edge, th: th(90, 98), state: api.UsageHard, window: "a", used: ptrF(99)},
		{name: "a reading from the future (clock went back) counts", r: future, th: th(90, 98), state: api.UsageHard, window: "a", used: ptrF(99)},
	})
}

func TestEvaluateBadNumbers(t *testing.T) {
	runEval(t, []evalCase{
		{name: "NaN window ignored", r: reading(win("a", math.NaN()), win("b", 10)), th: th(90, 98), state: api.UsageOK, window: "b", used: ptrF(10)},
		{name: "only NaN is unknown", r: reading(win("a", math.NaN())), th: th(90, 98), state: api.UsageUnknown},
		{name: "infinity ignored", r: reading(win("a", math.Inf(1))), th: th(90, 98), state: api.UsageUnknown},
		{name: "over 100 is 100", r: reading(win("a", 250)), th: th(90, 98), state: api.UsageHard, window: "a", used: ptrF(100)},
		{name: "below 0 is 0", r: reading(win("a", -5)), th: th(90, 98), state: api.UsageOK, window: "a", used: ptrF(0)},
	})
}

func TestEvaluateThresholdFallbacks(t *testing.T) {
	cases := []struct {
		name       string
		th         api.UsageSettings
		window     string
		soft, hard float64
	}{
		{"zero values take the defaults", api.UsageSettings{}, "a", 90, 98},
		{"NaN takes the defaults", api.UsageSettings{SoftPercent: math.NaN(), HardPercent: math.NaN()}, "a", 90, 98},
		{"over 100 takes the defaults", api.UsageSettings{SoftPercent: 101, HardPercent: 150}, "a", 90, 98},
		{"negative takes the defaults", api.UsageSettings{SoftPercent: -1, HardPercent: -1}, "a", 90, 98},
		{"hard below soft is soft", api.UsageSettings{SoftPercent: 95, HardPercent: 80}, "a", 95, 95},
		{"default hard below a high soft", api.UsageSettings{SoftPercent: 99}, "a", 99, 99},
		{"set values kept", api.UsageSettings{SoftPercent: 70, HardPercent: 85}, "a", 70, 85},
		{"bad override falls back to general", api.UsageSettings{SoftPercent: 70, HardPercent: 85, Windows: map[string]api.UsageThreshold{"a": {SoftPercent: ptrF(0), HardPercent: ptrF(math.NaN())}}}, "a", 70, 85},
		{"override of another window", api.UsageSettings{SoftPercent: 70, HardPercent: 85, Windows: map[string]api.UsageThreshold{"b": {SoftPercent: ptrF(10)}}}, "a", 70, 85},
		{"override 100", api.UsageSettings{Windows: map[string]api.UsageThreshold{"a": {SoftPercent: ptrF(100), HardPercent: ptrF(100)}}}, "a", 100, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			soft, hard := Thresholds(c.window, c.th)
			if soft != c.soft || hard != c.hard {
				t.Errorf("got %v/%v, want %v/%v", soft, hard, c.soft, c.hard)
			}
		})
	}
}

func TestCleanWindows(t *testing.T) {
	ahead := t0.Add(2 * time.Hour)
	many := make([]Window, MaxWindows+1)
	for i := range many {
		many[i] = Window{Name: "w" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('a'+i/26))}
	}
	cases := []struct {
		name string
		in   []Window
		want []api.UsageWindow
		err  string
	}{
		{name: "nil gives empty", in: nil, want: []api.UsageWindow{}},
		{name: "plain", in: []Window{{Name: "session", UsedPercent: 42.5, ResetsAt: ahead}, {Name: "weekly", UsedPercent: 12}},
			want: []api.UsageWindow{{Name: "session", UsedPercent: 42.5, ResetsAt: ptrT(ahead)}, {Name: "weekly", UsedPercent: 12}}},
		{name: "over 100 is 100", in: []Window{{Name: "a", UsedPercent: 120}}, want: []api.UsageWindow{{Name: "a", UsedPercent: 100}}},
		{name: "negative", in: []Window{{Name: "a", UsedPercent: -1}}, err: "window 1: the used percentage is not a number from 0 to 100"},
		{name: "NaN", in: []Window{{Name: "a"}, {Name: "b", UsedPercent: math.NaN()}}, err: "window 2: the used percentage is not a number from 0 to 100"},
		{name: "infinity", in: []Window{{Name: "a", UsedPercent: math.Inf(1)}}, err: "window 1: the used percentage is not a number from 0 to 100"},
		{name: "bad name", in: []Window{{Name: "Session"}}, err: "window 1: the name must be 1 to 40 lowercase letters, digits, - or _, starting with a letter or digit"},
		{name: "empty name", in: []Window{{Name: ""}}, err: "window 1: the name must be"},
		{name: "duplicate", in: []Window{{Name: "a"}, {Name: "a"}}, err: "window 2: the name is used twice"},
		{name: "too many", in: many, err: "a script may report at most 32 windows"},
		{name: "reset in the past kept", in: []Window{{Name: "a", ResetsAt: t0.Add(-time.Hour)}}, want: []api.UsageWindow{{Name: "a", ResetsAt: ptrT(t0.Add(-time.Hour))}}},
		{name: "reset far ahead dropped", in: []Window{{Name: "a", UsedPercent: 5, ResetsAt: t0.Add(MaxResetAhead + time.Second)}}, want: []api.UsageWindow{{Name: "a", UsedPercent: 5}}},
		{name: "reset at the bound kept", in: []Window{{Name: "a", ResetsAt: t0.Add(MaxResetAhead)}}, want: []api.UsageWindow{{Name: "a", ResetsAt: ptrT(t0.Add(MaxResetAhead))}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := cleanWindows(c.in, t0)
			if c.err != "" {
				if err == nil || !strings.HasPrefix(err.Error(), c.err) {
					t.Fatalf("err %v, want %q", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(c.want) || got == nil {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				g, w := got[i], c.want[i]
				if g.Name != w.Name || g.UsedPercent != w.UsedPercent || (g.ResetsAt == nil) != (w.ResetsAt == nil) || (g.ResetsAt != nil && !g.ResetsAt.Equal(*w.ResetsAt)) {
					t.Errorf("%d: got %+v, want %+v", i, g, w)
				}
			}
		})
	}
}

func TestCleanWindowsErrorHoldsNoValue(t *testing.T) {
	for _, ws := range [][]Window{{{Name: "Sk-Secret-Token"}}, {{Name: "sk-secret"}, {Name: "sk-secret"}}, {{Name: "sk-secret", UsedPercent: -1}}} {
		_, err := cleanWindows(ws, t0)
		if err == nil || strings.Contains(strings.ToLower(err.Error()), "secret") {
			t.Fatalf("err %v", err)
		}
	}
}

func TestNextDue(t *testing.T) {
	iv := 5 * time.Minute
	cases := []struct {
		name     string
		last     time.Time
		failures int
		windows  []api.UsageWindow
		now      time.Time
		want     time.Time
	}{
		{name: "interval after success", last: t0, now: t0, want: t0.Add(iv)},
		{name: "30s after an earlier reset", last: t0, windows: []api.UsageWindow{winAt("a", 50, t0.Add(time.Minute))}, now: t0, want: t0.Add(90 * time.Second)},
		{name: "earliest of several resets", last: t0, windows: []api.UsageWindow{winAt("a", 50, t0.Add(3*time.Minute)), winAt("b", 50, t0.Add(2*time.Minute)), win("c", 1)}, now: t0, want: t0.Add(150 * time.Second)},
		{name: "reset later than the interval", last: t0, windows: []api.UsageWindow{winAt("a", 50, t0.Add(time.Hour))}, now: t0, want: t0.Add(iv)},
		{name: "reset that had passed at the reading is ignored", last: t0, windows: []api.UsageWindow{winAt("a", 50, t0.Add(-time.Minute))}, now: t0, want: t0.Add(iv)},
		{name: "reset exactly at the reading is ignored", last: t0, windows: []api.UsageWindow{winAt("a", 50, t0)}, now: t0, want: t0.Add(iv)},
		{name: "first failure", last: t0, failures: 1, now: t0, want: t0.Add(time.Minute)},
		{name: "second failure", last: t0, failures: 2, now: t0, want: t0.Add(2 * time.Minute)},
		{name: "third failure", last: t0, failures: 3, now: t0, want: t0.Add(5 * time.Minute)},
		{name: "fourth failure: interval", last: t0, failures: 4, now: t0, want: t0.Add(iv)},
		{name: "failure ignores reset times", last: t0, failures: 1, windows: []api.UsageWindow{winAt("a", 50, t0.Add(10*time.Second))}, now: t0, want: t0.Add(time.Minute)},
		{name: "clock went back: at most one interval from now", last: t0, now: t0.Add(-time.Hour), want: t0.Add(-time.Hour + iv)},
		{name: "clock jumped ahead: due in the past means now", last: t0, now: t0.Add(time.Hour), want: t0.Add(iv)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := nextDue(c.last, backoff{failures: c.failures}, c.windows, iv, c.now)
			if !got.Equal(c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
	// A short interval caps the back-off.
	if got := nextDue(t0, backoff{failures: 3}, nil, 2*time.Minute, t0); !got.Equal(t0.Add(2 * time.Minute)) {
		t.Errorf("short interval: %v", got)
	}
}

// A rate-limited run waits what the answer asked for, else twice as long
// each time from 2 minutes up to the interval; never the 1-minute retry.
func TestNextDueAfterRateLimit(t *testing.T) {
	iv := 5 * time.Minute
	for _, c := range []struct {
		name string
		b    backoff
		iv   time.Duration
		want time.Duration
	}{
		{"first", backoff{failures: 1, limited: 1}, iv, 2 * time.Minute},
		{"second", backoff{failures: 2, limited: 2}, iv, 4 * time.Minute},
		{"third: the interval", backoff{failures: 3, limited: 3}, iv, iv},
		{"many", backoff{failures: 9, limited: 9}, iv, iv},
		{"after other failures", backoff{failures: 3, limited: 1}, iv, 2 * time.Minute},
		{"long interval", backoff{failures: 4, limited: 4}, time.Hour, 16 * time.Minute},
		{"short interval", backoff{failures: 1, limited: 1}, time.Minute, time.Minute},
		{"Retry-After", backoff{failures: 1, limited: 1, retryAfter: 3 * time.Minute}, iv, 3 * time.Minute},
		{"Retry-After beyond the interval", backoff{failures: 1, limited: 1, retryAfter: 20 * time.Minute}, iv, 20 * time.Minute},
		{"short Retry-After: at least a minute", backoff{failures: 1, limited: 1, retryAfter: 5 * time.Second}, iv, time.Minute},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := nextDue(t0, c.b, nil, c.iv, t0); !got.Equal(t0.Add(c.want)) {
				t.Errorf("waits %v, want %v", got.Sub(t0), c.want)
			}
		})
	}
}

func TestRefreshInterval(t *testing.T) {
	cases := []struct {
		in   api.Duration
		want time.Duration
	}{
		{0, 5 * time.Minute},
		{api.Duration(-time.Minute), 5 * time.Minute},
		{api.Duration(time.Second), time.Minute},
		{api.Duration(10 * time.Minute), 10 * time.Minute},
		{api.Duration(5 * time.Hour), time.Hour},
	}
	for _, c := range cases {
		if got := refreshInterval(api.UsageSettings{RefreshInterval: c.in}); got != c.want {
			t.Errorf("%v: got %v, want %v", time.Duration(c.in), got, c.want)
		}
	}
}

func TestAccountKey(t *testing.T) {
	p := api.Profile{Name: "a", AccountDir: "/home/u/.claude/", Env: map[string]string{"B": "2", "A": "1"}}
	k := AccountKey("claude", p)
	if len(k) != 16 || strings.Trim(k, "0123456789abcdef") != "" {
		t.Fatalf("key %q", k)
	}
	same := api.Profile{Name: "b", CLI: "codex", Command: "x", AccountDir: "/home/u/.claude", Env: map[string]string{"A": "1", "B": "2"},
		Models: map[string]api.ProfileModel{"m": {Env: map[string]string{"X": "y"}}}}
	if AccountKey("claude", same) != k {
		t.Error("name, CLI, command, models or a trailing slash changed the key")
	}
	for name, q := range map[string]api.Profile{
		"dir":      {AccountDir: "/home/u/.other"},
		"env":      {AccountDir: "/home/u/.claude", Env: map[string]string{"A": "1", "B": "3"}},
		"no env":   {AccountDir: "/home/u/.claude"},
		"shifted":  {AccountDir: "/home/u/.claude", Env: map[string]string{"A": "1B", "": "2"}},
		"more env": {AccountDir: "/home/u/.claude", Env: map[string]string{"A": "1", "B": "2", "C": ""}},
	} {
		if AccountKey("claude", q) == k {
			t.Errorf("%s: same key", name)
		}
	}
	if AccountKey("codex", p) == k {
		t.Error("script does not change the key")
	}
	if AccountKey("x", api.Profile{}) == AccountKey("x", api.Profile{AccountDir: "."}) {
		t.Error("no folder and the folder . share a key")
	}
}

func TestCleanError(t *testing.T) {
	long := strings.Repeat("é", 400)
	cases := []struct{ in, want string }{
		{"", "the script failed"},
		{"line 3: boom", "line 3: boom"},
		{"a\nb\tc", `a\u{A}b\u{9}c`},
		{"x\u202ey", `x\u{202E}y`},
		{long, strings.Repeat("é", maxErrorRunes)},
	}
	for _, c := range cases {
		if got := cleanError(c.in); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

func staleReading(since time.Time, ws ...api.UsageWindow) *api.UsageReading {
	r := reading(ws...)
	r.Error = "usage API returned HTTP 429"
	r.StaleSince = ptrT(since)
	return r
}

// After a failed run the last good windows keep counting for
// MaxStaleAfterFailure from the first failure; a window that has reset
// counts 0 %.
func TestEvaluateStaleReading(t *testing.T) {
	runEval(t, []evalCase{
		{name: "still counts", r: staleReading(t0.Add(5*time.Minute), win("session", 100)), th: th(90, 98), now: t0.Add(30 * time.Minute),
			state: api.UsageHard, window: "session", used: ptrF(100)},
		{name: "counts until the limit", r: staleReading(t0.Add(5*time.Minute), win("session", 100)), th: th(90, 98), now: t0.Add(35 * time.Minute),
			state: api.UsageHard, window: "session", used: ptrF(100)},
		{name: "unknown after the limit", r: staleReading(t0.Add(5*time.Minute), win("session", 100)), th: th(90, 98), now: t0.Add(35*time.Minute + time.Second),
			state: api.UsageUnknown},
		{name: "a reset window counts 0", r: staleReading(t0.Add(5*time.Minute), winAt("session", 100, t0.Add(10*time.Minute))), th: th(90, 98), now: t0.Add(20 * time.Minute),
			state: api.UsageOK, window: "session", used: ptrF(0)},
		{name: "an error without the time of the first failure", r: func() *api.UsageReading {
			r := reading(win("session", 100))
			r.Error = "x"
			return r
		}(), th: th(90, 98), state: api.UsageUnknown},
	})
}

// extra_usage is paid usage beyond the plan, not a rate window: it counts
// only for a model that lists it.
func TestEvaluateExtraUsageOnlyWhenListed(t *testing.T) {
	r := reading(win("session", 10), win("extra_usage", 100))
	runEval(t, []evalCase{
		{name: "every window", r: r, th: th(90, 98), state: api.UsageOK, window: "session", used: ptrF(10)},
		{name: "claude windows", windows: []string{"session", "weekly"}, r: r, th: th(90, 98), state: api.UsageOK, window: "session", used: ptrF(10)},
		{name: "listed", windows: []string{"session", "extra_usage"}, r: r, th: th(90, 98), state: api.UsageHard, window: "extra_usage", used: ptrF(100)},
		{name: "alone", r: reading(win("extra_usage", 100)), th: th(90, 98), state: api.UsageUnknown},
	})
}

// A model that counts a window the script did not report says so, and is
// judged by the windows that exist.
func TestEvaluateNamesMissingWindows(t *testing.T) {
	r := reading(win("session", 20), win("weekly", 95), win("extra_usage", 50))
	got := Evaluate("claude/fable", []string{"session", "weekly", "fable_weekly"}, r, th(90, 98), t0)
	if got.State != api.UsageSoft || got.Window != "weekly" ||
		got.Note != "claude/fable counts the window fable_weekly, which the script did not report" {
		t.Fatalf("%+v", got)
	}
	got = Evaluate("p/m", []string{"a", "session", "b"}, r, th(90, 98), t0)
	if got.Note != "p/m counts the windows a and b, which the script did not report" {
		t.Fatalf("%+v", got)
	}
	got = Evaluate("p/m", []string{"a", "b", "c"}, r, th(90, 98), t0)
	if got.State != api.UsageUnknown || got.Note != "p/m counts the windows a, b and c, which the script did not report" {
		t.Fatalf("%+v", got)
	}
	for _, c := range []struct {
		windows []string
		r       *api.UsageReading
	}{
		{[]string{"session"}, r},
		{nil, r},
		{[]string{"fable_weekly"}, &api.UsageReading{Windows: []api.UsageWindow{}}}, // never read
	} {
		if got := Evaluate("p/m", c.windows, c.r, th(90, 98), t0); got.Note != "" {
			t.Fatalf("%v: note %q", c.windows, got.Note)
		}
	}
}
