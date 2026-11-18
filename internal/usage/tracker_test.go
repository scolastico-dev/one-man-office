package usage

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// guard bounds every wait of these tests for something the tracker does; it
// is never reached when the tracker works.
const guard = 10 * time.Second

// fakeSource is a settings store in memory.
type fakeSource struct {
	mu       sync.Mutex
	profiles []api.Profile
	scripts  map[string]string
	general  api.General
}

func (s *fakeSource) Profiles() []api.Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]api.Profile, len(s.profiles))
	for i, p := range s.profiles {
		out[i] = cloneProfile(p)
	}
	return out
}

func (s *fakeSource) UsageScript(name string) (string, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	text, ok := s.scripts[name]
	return text, false, ok
}

func (s *fakeSource) General() settings.General {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.general
	g.Usage = cloneSettings(g.Usage)
	return settings.General(g)
}

func (s *fakeSource) edit(f func(s *fakeSource)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

func profile(name, script, dir string, models map[string][]string) api.Profile {
	p := api.Profile{Name: name, CLI: "claude", Command: "claude", UsageScript: script, AccountDir: dir, Models: map[string]api.ProfileModel{}}
	for m, ws := range models {
		p.Models[m] = api.ProfileModel{UsageWindows: ws}
	}
	return p
}

func newSource(profiles ...api.Profile) *fakeSource {
	return &fakeSource{
		profiles: profiles,
		scripts:  map[string]string{"s": "function usage() return {} end", "t": "function usage() return {} end"},
		general:  api.General{Usage: api.UsageSettings{SoftPercent: 90, HardPercent: 98, RefreshInterval: api.Duration(10 * time.Minute)}},
	}
}

// fakeClock is a clock that moves only when the test moves it.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at time.Time
	ch chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) timerAt(at time.Time) (<-chan time.Time, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tm := &fakeTimer{at: at, ch: make(chan time.Time, 1)}
	if !at.After(c.now) {
		tm.ch <- c.now
		return tm.ch, func() {}
	}
	c.timers = append(c.timers, tm)
	return tm.ch, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.timers = slices.DeleteFunc(c.timers, func(x *fakeTimer) bool { return x == tm })
	}
}

func (c *fakeClock) set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
	c.timers = slices.DeleteFunc(c.timers, func(tm *fakeTimer) bool {
		if tm.at.After(now) {
			return false
		}
		tm.ch <- now
		return true
	})
}

func (c *fakeClock) advance(d time.Duration) { c.set(c.Now().Add(d)) }

// call is one call of the fake run function; the test answers it.
type call struct {
	job   Job
	ctx   context.Context
	reply chan reply
}

type reply struct {
	res Result
	err error
}

func (c *call) ok(ws ...Window) { c.reply <- reply{res: Result{Windows: ws}} }

func (c *call) fail(msg string) { c.reply <- reply{err: errors.New(msg)} }

// fakeRunner hands every call to the test and fails the test when an
// account runs twice at once.
type fakeRunner struct {
	t        *testing.T
	calls    chan *call
	mu       sync.Mutex
	inflight map[string]int
	total    int
	// afterCancel, when set, holds a run that was cancelled until it is
	// closed: the run function has not returned yet.
	afterCancel chan struct{}
}

func newRunner(t *testing.T) *fakeRunner {
	return &fakeRunner{t: t, calls: make(chan *call, 64), inflight: map[string]int{}}
}

func (r *fakeRunner) run(ctx context.Context, j Job) (Result, error) {
	key := AccountKey(j.Script, j.Profile)
	r.mu.Lock()
	r.inflight[key]++
	r.total++
	if r.inflight[key] > 1 {
		r.t.Errorf("account %s of %s runs twice at once", key, j.Profile.Name)
	}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.inflight[key]--
		r.mu.Unlock()
	}()
	c := &call{job: j, ctx: ctx, reply: make(chan reply, 1)}
	r.calls <- c
	select {
	case rp := <-c.reply:
		return rp.res, rp.err
	case <-ctx.Done():
		if r.afterCancel != nil {
			<-r.afterCancel
		}
		return Result{}, ctx.Err()
	}
}

type idleReport struct {
	wake    time.Time
	running int
}

type harness struct {
	t       *testing.T
	src     *fakeSource
	clock   *fakeClock
	runner  *fakeRunner
	tr      *Tracker
	changes chan struct{}
	idle    chan idleReport
	cancel  context.CancelFunc
	done    chan struct{}
	logs    *strings.Builder
}

func newHarness(t *testing.T, src *fakeSource, opts func(o *TrackerOptions)) *harness {
	t.Helper()
	h := &harness{
		t:       t,
		src:     src,
		clock:   &fakeClock{now: t0},
		runner:  newRunner(t),
		changes: make(chan struct{}, 1024),
		idle:    make(chan idleReport, 1024),
		logs:    &strings.Builder{},
	}
	var logMu sync.Mutex
	o := TrackerOptions{
		Source:   src,
		Run:      h.runner.run,
		Now:      h.clock.Now,
		OnChange: func() { h.changes <- struct{}{} },
		Log:      log.New(lockedWriter{&logMu, h.logs}, "", 0),
		timerAt:  h.clock.timerAt,
		idle:     func(wake time.Time, running int) { h.idle <- idleReport{wake, running} },
	}
	if opts != nil {
		opts(&o)
	}
	h.tr = NewTracker(o)
	return h
}

type lockedWriter struct {
	mu *sync.Mutex
	b  *strings.Builder
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (h *harness) start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan struct{})
	go func() {
		h.tr.Run(ctx)
		close(h.done)
	}()
	h.t.Cleanup(h.stop)
}

func (h *harness) stop() {
	if h.cancel == nil {
		return
	}
	h.cancel()
	<-h.done
	h.cancel = nil
}

// next returns the next call of the run function.
func (h *harness) next() *call {
	h.t.Helper()
	return <-h.runner.calls
}

// settle waits until the loop has nothing under way and checks that no call
// is pending.
func (h *harness) settle() {
	h.t.Helper()
	for {
		select {
		case <-h.idle:
			continue
		default:
		}
		break
	}
	h.tr.wake()
	for {
		select {
		case r := <-h.idle:
			if r.running == 0 {
				if n := len(h.runner.calls); n > 0 {
					h.t.Fatalf("%d unexpected runs", n)
				}
				return
			}
			h.tr.wake()
		}
	}
}

func (h *harness) reading(profile string) api.UsageReading {
	h.t.Helper()
	for _, r := range h.tr.State().Readings {
		if slices.Contains(r.Profiles, profile) {
			return r
		}
	}
	h.t.Fatalf("no reading for %s", profile)
	return api.UsageReading{}
}

func (h *harness) nextAt(profile string) time.Time {
	h.t.Helper()
	r := h.reading(profile)
	if r.NextAt == nil {
		h.t.Fatalf("no next run for %s", profile)
	}
	return *r.NextAt
}

func TestTrackerOneRunPerAccount(t *testing.T) {
	src := newSource(
		profile("b", "s", "/acct/one/", map[string][]string{"m": nil}),
		profile("a", "s", "/acct/one", map[string][]string{"m": {"session"}, "n": {"weekly"}}),
		profile("c", "s", "/acct/two", map[string][]string{"m": nil}),
		profile("d", "", "/acct/one", map[string][]string{"m": nil}),
		profile("e", "nope", "", map[string][]string{"m": nil}),
	)
	h := newHarness(t, src, nil)
	h.start()
	calls := map[string]*call{}
	for range 2 {
		c := h.next()
		calls[c.job.Profile.Name] = c
	}
	if calls["a"] == nil || calls["c"] == nil {
		t.Fatalf("runs for %v", calls)
	}
	if j := calls["a"].job; j.Script != "s" || j.Text != src.scripts["s"] || j.Profile.AccountDir != "/acct/one" {
		t.Errorf("job %+v", j)
	}
	calls["a"].ok(Window{Name: "session", UsedPercent: 95}, Window{Name: "weekly", UsedPercent: 99})
	calls["c"].ok(Window{Name: "session", UsedPercent: 10})
	h.settle()
	if h.runner.total != 2 {
		t.Errorf("%d runs", h.runner.total)
	}
	st := h.tr.State()
	if len(st.Readings) != 3 {
		t.Fatalf("readings %+v", st.Readings)
	}
	shared := h.reading("a")
	if !slices.Equal(shared.Profiles, []string{"a", "b"}) || shared.Key != AccountKey("s", src.profiles[1]) || shared.Error != "" || len(shared.Windows) != 2 || shared.FetchedAt == nil || !shared.FetchedAt.Equal(t0) {
		t.Errorf("shared reading %+v", shared)
	}
	if e := h.reading("e"); e.Error != "the script nope does not exist" || e.NextAt != nil || e.Running || e.FetchedAt != nil {
		t.Errorf("missing script reading %+v", e)
	}
	want := map[string]string{
		"a/m": api.UsageSoft, "a/n": api.UsageHard, "b/m": api.UsageHard,
		"c/m": api.UsageOK, "d/m": api.UsageUnmetered, "e/m": api.UsageUnknown,
	}
	var ids []string
	for _, m := range st.Models {
		ids = append(ids, m.Model)
		if want[m.Model] != m.State {
			t.Errorf("%s: %s, want %s", m.Model, m.State, want[m.Model])
		}
		if got := h.tr.Model(m.Model); got.State != m.State || got.Window != m.Window {
			t.Errorf("Model(%s) = %+v, State has %+v", m.Model, got, m)
		}
	}
	if !slices.Equal(ids, []string{"a/m", "a/n", "b/m", "c/m", "d/m", "e/m"}) {
		t.Errorf("models %v", ids)
	}
	if m := h.tr.Model("x/y"); m.State != api.UsageUnmetered || m.Model != "x/y" {
		t.Errorf("unknown model %+v", m)
	}
}

func TestTrackerRunsAfterReset(t *testing.T) {
	h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), nil)
	h.start()
	h.next().ok(Window{Name: "session", UsedPercent: 99, ResetsAt: t0.Add(time.Minute)}, Window{Name: "weekly", UsedPercent: 50, ResetsAt: t0.Add(48 * time.Hour)})
	h.settle()
	if got := h.nextAt("a"); !got.Equal(t0.Add(90 * time.Second)) {
		t.Fatalf("next at %v", got)
	}
	if m := h.tr.Model("a/m"); m.State != api.UsageHard {
		t.Fatalf("before the reset: %+v", m)
	}
	h.clock.advance(time.Minute)
	if m := h.tr.Model("a/m"); m.State != api.UsageOK || m.Window != "weekly" {
		t.Fatalf("after the reset, before the run: %+v", m)
	}
	h.settle() // not due yet
	h.clock.advance(30 * time.Second)
	c := h.next()
	c.ok(Window{Name: "session", UsedPercent: 1, ResetsAt: t0.Add(6 * time.Hour)})
	h.settle()
	if got, want := h.nextAt("a"), t0.Add(90*time.Second+10*time.Minute); !got.Equal(want) {
		t.Fatalf("next at %v, want %v", got, want)
	}
}

func TestTrackerBacksOffAfterFailure(t *testing.T) {
	h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), nil)
	h.start()
	h.next().ok(Window{Name: "session", UsedPercent: 99})
	h.settle()
	if m := h.tr.Model("a/m"); m.State != api.UsageHard {
		t.Fatalf("%+v", m)
	}
	h.clock.advance(10 * time.Minute)
	h.next().fail("line 3: login expired")
	h.settle()
	r := h.reading("a")
	failedAt := h.clock.Now()
	if r.Error != "line 3: login expired" || len(r.Windows) != 1 || r.StaleSince == nil || !r.StaleSince.Equal(failedAt) ||
		r.FetchedAt == nil || !r.FetchedAt.Equal(t0) {
		t.Fatalf("reading %+v", r)
	}
	// The last good windows keep counting for a while after the failure.
	if m := h.tr.Model("a/m"); m.State != api.UsageHard || m.Window != "session" {
		t.Fatalf("a failed run must keep the last readings counting: %+v", m)
	}
	for i, wait := range []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute} {
		now := h.clock.Now()
		if got := h.nextAt("a"); !got.Equal(now.Add(wait)) {
			t.Fatalf("failure %d: next at %v, want %v", i+1, got.Sub(now), wait)
		}
		h.clock.advance(wait - time.Second)
		h.settle()
		h.clock.advance(time.Second)
		h.next().fail("again")
		h.settle()
	}
	if got := h.nextAt("a"); !got.Equal(h.clock.Now().Add(10 * time.Minute)) {
		t.Fatalf("after many failures: %v", got.Sub(h.clock.Now()))
	}
	// The first failure was 18 minutes ago; from 30 minutes after it the
	// model is unknown.
	if r := h.reading("a"); !r.StaleSince.Equal(failedAt) {
		t.Fatalf("stale since moved: %+v", r)
	}
	if m := h.tr.Model("a/m"); m.State != api.UsageHard {
		t.Fatalf("18 minutes after the failure: %+v", m)
	}
	for _, minutes := range []int{28, 38} {
		h.clock.advance(10 * time.Minute)
		h.next().fail("again")
		h.settle()
		if m, want := h.tr.Model("a/m"), map[int]string{28: api.UsageHard, 38: api.UsageUnknown}[minutes]; m.State != want {
			t.Fatalf("%d minutes after the failure: %+v, want %s", minutes, m, want)
		}
	}
	h.clock.advance(10 * time.Minute)
	h.next().ok(Window{Name: "session", UsedPercent: 5})
	h.settle()
	if r := h.reading("a"); r.Error != "" || r.StaleSince != nil {
		t.Fatalf("error kept after a success: %+v", r)
	}
	if got := h.nextAt("a"); !got.Equal(h.clock.Now().Add(10 * time.Minute)) {
		t.Fatalf("after a success: %v", got.Sub(h.clock.Now()))
	}
	h.clock.advance(10 * time.Minute)
	h.next().fail("once more")
	h.settle()
	if got := h.nextAt("a"); !got.Equal(h.clock.Now().Add(time.Minute)) {
		t.Fatalf("the back-off did not start over: %v", got.Sub(h.clock.Now()))
	}
}

// A rate-limited run backs off longer each time, or as long as the answer
// asked, and its last readings keep counting.
func TestTrackerBacksOffAfterRateLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), func(o *TrackerOptions) { o.StatePath = path })
	h.start()
	h.next().ok(Window{Name: "session", UsedPercent: 100})
	h.settle()
	limited := func(after time.Duration) { h.next().reply <- reply{err: &RateLimitError{RetryAfter: after}} }
	h.clock.advance(10 * time.Minute)
	limited(0)
	h.settle()
	if r := h.reading("a"); r.Error != RateLimitText || len(r.Windows) != 1 {
		t.Fatalf("reading %+v", r)
	}
	if m := h.tr.Model("a/m"); m.State != api.UsageHard {
		t.Fatalf("model %+v", m)
	}
	for i, c := range []struct {
		wait, after time.Duration
	}{{2 * time.Minute, 0}, {4 * time.Minute, 7 * time.Minute}, {7 * time.Minute, 0}, {10 * time.Minute, 0}} {
		now := h.clock.Now()
		if got := h.nextAt("a"); !got.Equal(now.Add(c.wait)) {
			t.Fatalf("rate limit %d: next in %v, want %v", i+1, got.Sub(now), c.wait)
		}
		h.clock.advance(c.wait)
		limited(c.after)
		h.settle()
	}
	// The kept readings survive the failures: a restart still knows them.
	h.stop()
	h2 := newHarness(t, h.src, func(o *TrackerOptions) { o.StatePath = path })
	if r := h2.reading("a"); len(r.Windows) != 1 || r.FetchedAt == nil || !r.FetchedAt.Equal(t0) {
		t.Fatalf("after a restart: %+v", r)
	}
}

func TestTrackerSettingsChanged(t *testing.T) {
	src := newSource(profile("a", "s", "", map[string][]string{"m": nil}), profile("b", "t", "/b", map[string][]string{"m": nil}))
	h := newHarness(t, src, nil)
	h.start()
	for range 2 {
		h.next().ok(Window{Name: "session", UsedPercent: 91})
	}
	h.settle()

	// Thresholds: no run, the state follows.
	src.edit(func(s *fakeSource) { s.general.Usage.SoftPercent = 95 })
	h.tr.SettingsChanged()
	h.settle()
	if m := h.tr.Model("a/m"); m.State != api.UsageOK {
		t.Fatalf("after soft 95: %+v", m)
	}
	src.edit(func(s *fakeSource) {
		s.general.Usage.Windows = map[string]api.UsageThreshold{"session": {SoftPercent: ptrF(80), HardPercent: ptrF(91)}}
	})
	h.tr.SettingsChanged()
	h.settle()
	if m := h.tr.Model("a/m"); m.State != api.UsageHard {
		t.Fatalf("after a window override: %+v", m)
	}

	// The profile switches its script: the new account runs at once, the
	// old one is gone.
	oldKey := h.reading("a").Key
	src.edit(func(s *fakeSource) { s.profiles[0].UsageScript = "t" })
	h.tr.SettingsChanged()
	c := h.next()
	if c.job.Profile.Name != "a" || c.job.Script != "t" {
		t.Fatalf("job %+v", c.job)
	}
	if m := h.tr.Model("a/m"); m.State != api.UsageUnknown {
		t.Fatalf("a new account before its first answer: %+v", m)
	}
	c.ok(Window{Name: "session", UsedPercent: 10})
	h.settle()
	for _, r := range h.tr.State().Readings {
		if r.Key == oldKey {
			t.Fatal("the old account is still listed")
		}
	}

	// The script text and the agent environment each make every account
	// that uses them run again.
	for _, edit := range []func(s *fakeSource){
		func(s *fakeSource) { s.scripts["t"] = "function usage() return {{name='x', used=1}} end" },
		func(s *fakeSource) { s.general.AgentEnv = map[string]string{"X": "1"} },
	} {
		src.edit(edit)
		h.tr.SettingsChanged()
		got := map[string]bool{}
		for range 2 {
			c := h.next()
			got[c.job.Profile.Name] = true
			c.ok()
		}
		if !got["a"] || !got["b"] {
			t.Errorf("ran %v", got)
		}
		h.settle()
	}

	// The interval: the next run moves.
	now := h.clock.Now()
	src.edit(func(s *fakeSource) { s.general.Usage.RefreshInterval = api.Duration(2 * time.Minute) })
	h.tr.SettingsChanged()
	h.settle()
	if got := h.nextAt("a"); !got.Equal(now.Add(2 * time.Minute)) {
		t.Fatalf("after the interval change: %v", got.Sub(now))
	}

	// A script that goes away and comes back.
	src.edit(func(s *fakeSource) { delete(s.scripts, "t") })
	h.tr.SettingsChanged()
	h.settle()
	if r := h.reading("a"); r.Error != "the script t does not exist" || len(r.Windows) != 0 || r.NextAt != nil {
		t.Fatalf("missing: %+v", r)
	}
	src.edit(func(s *fakeSource) { s.scripts["t"] = "function usage() return {} end" })
	h.tr.SettingsChanged()
	h.next().ok(Window{Name: "w", UsedPercent: 3}) // both profiles use t
	h.next().ok(Window{Name: "w", UsedPercent: 3})
	h.settle()
	if r := h.reading("a"); r.Error != "" || len(r.Windows) != 1 {
		t.Fatalf("back: %+v", r)
	}
}

// TestTrackerCommandChangeRunsOnlyItsAccount pins that a change to one
// profile runs only the account that profile belongs to.
func TestTrackerCommandChangeRunsOnlyItsAccount(t *testing.T) {
	src := newSource(profile("a", "s", "", map[string][]string{"m": nil}), profile("b", "s", "/b", map[string][]string{"m": nil}))
	h := newHarness(t, src, nil)
	h.start()
	h.next().ok()
	h.next().ok()
	h.settle()
	src.edit(func(s *fakeSource) { s.profiles[1].Command = "other" })
	h.tr.SettingsChanged()
	c := h.next()
	if c.job.Profile.Name != "b" || c.job.Profile.Command != "other" {
		t.Fatalf("job %+v", c.job.Profile)
	}
	c.ok()
	h.settle()
}

func TestTrackerPersistsWindowsOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")
	src := newSource(profile("a", "s", "", map[string][]string{"m": nil}), profile("b", "s", "/b", map[string][]string{"m": nil}))
	src.profiles[0].Env = map[string]string{"TOKEN": "env-secret-value"}
	h := newHarness(t, src, func(o *TrackerOptions) { o.StatePath = path })
	h.start()
	for range 2 {
		c := h.next()
		if c.job.Profile.Name == "a" {
			c.reply <- reply{res: Result{Windows: []Window{{Name: "session", UsedPercent: 99, ResetsAt: t0.Add(time.Hour)}, {Name: "weekly", UsedPercent: 20}}, Log: []string{"log-secret-line"}}}
		} else {
			c.fail("error-secret-text")
		}
	}
	h.settle()
	h.stop()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	for _, bad := range []string{"secret", "function usage", "/b", `"a"`, "TOKEN"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("the file holds %q: %s", bad, raw)
		}
	}
	var f keptFile
	if err := json.Unmarshal(raw, &f); err != nil || len(f.Accounts) != 1 || f.Accounts[0].Key != AccountKey("s", src.profiles[0]) {
		t.Fatalf("file %s (%v)", raw, err)
	}

	// A restart: the model is at its limit before any run.
	h2 := newHarness(t, src, func(o *TrackerOptions) { o.StatePath = path })
	if m := h2.tr.Model("a/m"); m.State != api.UsageHard || m.Window != "session" {
		t.Fatalf("after a restart: %+v", m)
	}
	if m := h2.tr.Model("b/m"); m.State != api.UsageUnknown {
		t.Fatalf("a failed account after a restart: %+v", m)
	}
	h2.clock.advance(time.Hour)
	if m := h2.tr.Model("a/m"); m.State != api.UsageOK || m.Window != "weekly" {
		t.Fatalf("after the reset: %+v", m)
	}

	// A profile that no longer exists: its kept entry is dropped on the
	// next write.
	src.edit(func(s *fakeSource) { s.profiles = s.profiles[1:] })
	h3 := newHarness(t, src, func(o *TrackerOptions) { o.StatePath = path })
	h3.start()
	h3.next().ok(Window{Name: "x", UsedPercent: 1})
	h3.settle()
	h3.stop()
	raw, _ = os.ReadFile(path)
	if err := json.Unmarshal(raw, &f); err != nil || len(f.Accounts) != 1 || f.Accounts[0].Key != AccountKey("s", src.profiles[0]) {
		t.Fatalf("file %s (%v)", raw, err)
	}
}

func TestTrackerIgnoresBadStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.json")
	os.WriteFile(path, []byte(`{"accounts": [ secret-in-file`), 0o600)
	h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), func(o *TrackerOptions) { o.StatePath = path })
	if got := h.logs.String(); got != keptIgnored+"the file is not valid\n" {
		t.Fatalf("log %q", got)
	}
	if m := h.tr.Model("a/m"); m.State != api.UsageUnknown {
		t.Fatalf("%+v", m)
	}
	h.start()
	h.next().ok(Window{Name: "w", UsedPercent: 1})
	h.settle()
	h.stop()
	if _, why := loadKept(path, t0); why != "" {
		t.Fatalf("the file was not replaced: %s", why)
	}
}

func TestTrackerRefreshWaits(t *testing.T) {
	src := newSource(profile("a", "s", "", map[string][]string{"m": nil}), profile("b", "s", "/b", map[string][]string{"m": nil}), profile("e", "nope", "", nil))
	h := newHarness(t, src, nil)
	// Refresh works before Run and starts every account.
	answer := make(chan api.UsageState, 1)
	go func() { answer <- h.tr.Refresh(context.Background()) }()
	c1, c2 := h.next(), h.next()
	c1.ok(Window{Name: "w", UsedPercent: 1})
	select {
	case <-answer:
		t.Fatal("Refresh returned before every run ended")
	default:
	}
	c2.ok(Window{Name: "w", UsedPercent: 2})
	var st api.UsageState
	st = <-answer
	for _, r := range st.Readings {
		if r.Script == "s" && (r.FetchedAt == nil || r.Running) {
			t.Errorf("reading %+v", r)
		}
	}

	// A Refresh while a run is under way runs that account once more after
	// it, never twice at once.
	h.start()
	h.clock.advance(10 * time.Minute)
	first := []*call{h.next(), h.next()}
	go func() { answer <- h.tr.Refresh(context.Background()) }()
	// Wait until Refresh has registered: it marks the runs to repeat.
	waitFor(t, func() bool {
		h.tr.mu.Lock()
		defer h.tr.mu.Unlock()
		n := 0
		for _, a := range h.tr.accounts {
			if a.again {
				n++
			}
		}
		return n == 2
	})
	for _, c := range first {
		c.ok(Window{Name: "w", UsedPercent: 3})
	}
	h.next().ok(Window{Name: "w", UsedPercent: 4})
	h.next().ok(Window{Name: "w", UsedPercent: 4})
	st = <-answer
	for _, r := range st.Readings {
		if r.Script == "s" && r.Windows[0].UsedPercent != 4 {
			t.Errorf("Refresh answered before the repeated run: %+v", r)
		}
	}
	h.settle()
}

// waitFor waits for a condition the tracker reaches on its own goroutine.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for !cond() {

		runtime.Gosched()
	}
}

// The default deadline is checked against virtual time so receiving the run
// cannot consume the scheduling slack of a wall-clock deadline assertion.
func TestTrackerUsesDefaultRunTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), nil)
		start := time.Now() // the virtual time the run is launched at
		h.start()
		c := h.next()
		deadline, ok := c.ctx.Deadline()
		if want := start.Add(RunTimeout); !ok || !deadline.Equal(want) {
			t.Fatalf("run deadline %v (%v), want %v", deadline, ok, want)
		}
		h.stop()
	})
}

// bubbleWaitFor waits, inside a synctest bubble, for a condition a goroutine of
// the bubble reaches. Each round first lets every other goroutine run until it
// is blocked; a round that does not find the condition lets virtual time pass,
// so the wait ends at the condition and never after a guessed number of turns.
func bubbleWaitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(guard) // virtual time inside the bubble: not a wall clock
	for {
		synctest.Wait()
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(time.Millisecond)
	}
}

// A Refresh whose run never answers ends with its own context, starts no
// second run, and the stop ends the run. The test runs in a synctest bubble:
// synctest.Wait returns only when every goroutine of the test is blocked, so
// each observation is made at an exact point, and synctest.Test returns only
// when every goroutine the tracker started has ended, which is the proof that
// no run, worker or Refresh outlives the stop (a global goroutine count would
// also see goroutines the test does not own).
func TestTrackerRefreshThatHangs(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), nil)
		h.start()
		c := h.next() // never answered
		ctx, cancel := context.WithCancel(context.Background())
		answer := make(chan api.UsageState, 1)
		go func() { answer <- h.tr.Refresh(ctx) }()
		// Refresh has registered when the account is marked to repeat. The
		// poll reads that real condition; the virtual clock moves on only
		// while every goroutine of the test is blocked.
		bubbleWaitFor(t, func() bool {
			h.tr.mu.Lock()
			defer h.tr.mu.Unlock()
			return h.tr.accounts[AccountKey("s", h.src.profiles[0])].again
		})
		synctest.Wait()
		select {
		case <-answer:
			t.Fatal("Refresh answered while its run was under way")
		default:
		}
		cancel()
		if st := <-answer; !st.Readings[0].Running {
			t.Errorf("reading %+v", st.Readings[0])
		}
		if len(h.runner.calls) != 0 {
			t.Fatal("a second run of the same account started")
		}
		h.stop()
		select {
		case <-c.ctx.Done():
		default:
			t.Fatal("stopping did not end the run")
		}
		// Nothing runs after the stop; Refresh answers at once.
		h.tr.Refresh(context.Background())
		if len(h.runner.calls) != 0 {
			t.Fatal("a run started after the stop")
		}
	})
}

// Run returns only after the runs it started have returned: it joins them. A
// run held after its cancellation keeps Run from returning, and Run returns
// once the run is let go.
func TestTrackerStopWaitsForTheRunsItOwns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), nil)
		h.runner.afterCancel = make(chan struct{})
		h.start()
		c := h.next()
		h.cancel()
		<-c.ctx.Done()
		synctest.Wait()
		select {
		case <-h.done:
			close(h.runner.afterCancel)
			t.Fatal("Run returned while a run it started was still under way")
		default:
		}
		close(h.runner.afterCancel)
		<-h.done
		h.cancel = nil
	})
}

func TestTrackerRunDeadline(t *testing.T) {
	h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), func(o *TrackerOptions) { o.runTimeout = -1 })
	h.start()
	c := h.next()
	if !errors.Is(c.ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("ctx %v", c.ctx.Err())
	}
	h.settle()
	if r := h.reading("a"); r.Error != "the script did not finish within 70 seconds" {
		t.Fatalf("reading %+v", r)
	}
	if got := h.nextAt("a"); !got.Equal(t0.Add(time.Minute)) {
		t.Fatalf("next %v", got)
	}
}

func TestTrackerParallel(t *testing.T) {
	src := newSource(profile("a", "s", "/a", nil), profile("b", "s", "/b", nil), profile("c", "s", "/c", nil))
	h := newHarness(t, src, nil)
	h.start()
	c1, c2 := h.next(), h.next()
	if n := len(h.tr.sem); n != 2 {
		t.Fatalf("%d places taken", n)
	}
	if len(h.runner.calls) != 0 {
		t.Fatal("a third run started")
	}
	c1.ok()
	c3 := h.next()
	c2.ok()
	c3.ok()
	h.settle()
	if h.runner.total != 3 {
		t.Fatalf("%d runs", h.runner.total)
	}
}

func TestTrackerBadResults(t *testing.T) {
	cases := []struct {
		name  string
		ws    []Window
		err   string
		used  float64
		state string
	}{
		{"NaN", []Window{{Name: "a", UsedPercent: math.NaN()}}, "window 1: the used percentage is not a number from 0 to 100", 0, api.UsageUnknown},
		{"negative", []Window{{Name: "a", UsedPercent: -3}}, "window 1: the used percentage is not a number from 0 to 100", 0, api.UsageUnknown},
		{"infinite", []Window{{Name: "a", UsedPercent: math.Inf(-1)}}, "window 1: the used percentage is not a number from 0 to 100", 0, api.UsageUnknown},
		{"bad name", []Window{{Name: "a"}, {Name: "B"}}, "window 2: the name must be", 0, api.UsageUnknown},
		{"over 100", []Window{{Name: "a", UsedPercent: 1e9}}, "", 100, api.UsageHard},
		{"reset in the past", []Window{{Name: "a", UsedPercent: 99, ResetsAt: t0.Add(-time.Hour)}}, "", 0, api.UsageOK},
		{"reset far ahead", []Window{{Name: "a", UsedPercent: 99, ResetsAt: t0.AddDate(5, 0, 0)}}, "", 99, api.UsageHard},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, newSource(profile("p", "s", "", map[string][]string{"m": nil})), nil)
			h.start()
			h.next().ok(c.ws...)
			h.settle()
			r := h.reading("p")
			if !strings.HasPrefix(r.Error, c.err) || (c.err == "") != (r.Error == "") {
				t.Fatalf("error %q, want %q", r.Error, c.err)
			}
			m := h.tr.Model("p/m")
			if m.State != c.state || (m.UsedPercent != nil && *m.UsedPercent != c.used) {
				t.Fatalf("model %+v", m)
			}
			if c.name == "reset far ahead" && (len(r.Windows) != 1 || r.Windows[0].ResetsAt != nil) {
				t.Fatalf("windows %+v", r.Windows)
			}
		})
	}
}

func TestTrackerClockJumps(t *testing.T) {
	h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), nil)
	h.start()
	h.next().ok(Window{Name: "w", UsedPercent: 99})
	h.settle()
	// Back by a day: the next run is at most one interval away.
	h.clock.set(t0.Add(-24 * time.Hour))
	h.settle()
	if got := h.nextAt("a"); got.After(h.clock.Now().Add(10 * time.Minute)) {
		t.Fatalf("next run %v after the clock went back", got.Sub(h.clock.Now()))
	}
	if m := h.tr.Model("a/m"); m.State != api.UsageHard {
		t.Fatalf("a reading from the future still counts: %+v", m)
	}
	h.clock.advance(10 * time.Minute)
	h.next().ok(Window{Name: "w", UsedPercent: 99})
	h.settle()
	// Ahead by two days (a suspended computer): the reading is too old to
	// count and the account runs at once.
	h.clock.set(h.clock.Now().Add(48 * time.Hour))
	if m := h.tr.Model("a/m"); m.State != api.UsageUnknown {
		t.Fatalf("a reading two days old: %+v", m)
	}
	h.next().ok(Window{Name: "w", UsedPercent: 1})
	h.settle()
	if m := h.tr.Model("a/m"); m.State != api.UsageOK {
		t.Fatalf("%+v", m)
	}
}

// TestTrackerWaitsAtMostAMinute pins that the loop looks at the clock at
// least once a minute, so that a wall clock that jumps is noticed even when
// the timer does not see the jump.
func TestTrackerWaitsAtMostAMinute(t *testing.T) {
	h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), nil)
	h.start()
	h.next().ok()
	h.settle()
	h.tr.wake()
	for {
		r := <-h.idle
		if r.running == 0 {
			if want := h.clock.Now().Add(maxSleep); !r.wake.Equal(want) {
				t.Fatalf("wakes at %v, want %v", r.wake, want)
			}
			return
		}
	}
}

func TestTrackerStateHoldsNoScriptOutput(t *testing.T) {
	src := newSource(profile("a", "s", "/acct", map[string][]string{"m": nil}), profile("b", "s", "/b", map[string][]string{"m": nil}))
	src.scripts["s"] = "-- script-text-canary\nfunction usage() end"
	src.profiles[0].Env = map[string]string{"KEY": "env-canary"}
	src.general.AgentEnv = map[string]string{"AGENT": "agent-canary"}
	var mu sync.Mutex
	var events []string
	h := newHarness(t, src, nil)
	h.tr.o.OnChange = func() {
		raw, _ := json.Marshal(h.tr.State())
		mu.Lock()
		events = append(events, string(raw))
		mu.Unlock()
	}
	h.start()
	for range 2 {
		c := h.next()
		if c.job.Profile.Name == "a" {
			if c.job.AgentEnv["AGENT"] != "agent-canary" || c.job.Profile.Env["KEY"] != "env-canary" {
				t.Errorf("job %+v", c.job)
			}
			c.reply <- reply{res: Result{Windows: []Window{{Name: "w", UsedPercent: 5}}, Log: []string{"log-canary"}}}
		} else {
			c.fail("line 1: bad\nthing\x1b[31m " + strings.Repeat("x", 400))
		}
	}
	h.settle()
	raw, _ := json.Marshal(h.tr.State())
	mu.Lock()
	all := append(events, string(raw))
	mu.Unlock()
	for _, s := range all {
		for _, bad := range []string{"canary", "\x1b", "\n"} {
			if strings.Contains(s, bad) {
				t.Errorf("state holds %q: %s", bad, s)
			}
		}
	}
	r := h.reading("b")
	if !strings.HasPrefix(r.Error, `line 1: bad\u{A}thing\u{1B}[31m x`) || len([]rune(r.Error)) > 300+5*2 {
		t.Errorf("error %q", r.Error)
	}
	// Changing the returned state does not change the tracker.
	st := h.tr.State()
	st.Readings[0].Windows[0].UsedPercent = 77
	st.Readings[0].Profiles[0] = "zz"
	if r := h.tr.State().Readings[0]; r.Windows[0].UsedPercent == 77 || r.Profiles[0] == "zz" {
		t.Error("State shares memory with the tracker")
	}
}

func TestTrackerEmptyStateNotNil(t *testing.T) {
	h := newHarness(t, newSource(), nil)
	raw, _ := json.Marshal(h.tr.State())
	if string(raw) != `{"readings":[],"models":[]}` {
		t.Fatalf("%s", raw)
	}
	h2 := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), nil)
	r := h2.tr.State().Readings[0]
	if r.Windows == nil || r.Profiles == nil {
		t.Fatalf("%+v", r)
	}
}

func TestTrackerProfileRemovedWhileRunning(t *testing.T) {
	src := newSource(profile("a", "s", "", map[string][]string{"m": nil}))
	h := newHarness(t, src, nil)
	h.start()
	c := h.next()
	keep := src.profiles
	src.edit(func(s *fakeSource) { s.profiles = nil })
	h.tr.SettingsChanged()
	if st := h.tr.State(); len(st.Readings) != 0 || len(st.Models) != 0 {
		t.Fatalf("state %+v", st)
	}
	// Back while the old run is still under way: no second run at once,
	// one more run after it.
	src.edit(func(s *fakeSource) { s.profiles = keep })
	h.tr.SettingsChanged()
	if len(h.runner.calls) != 0 {
		t.Fatal("a second run started")
	}
	c.ok(Window{Name: "w", UsedPercent: 50})
	h.next().ok(Window{Name: "w", UsedPercent: 60})
	h.settle()
	if r := h.reading("a"); r.Windows[0].UsedPercent != 60 {
		t.Fatalf("%+v", r)
	}
	// Removed for good: the result of a run under way is dropped.
	h.clock.advance(10 * time.Minute)
	c = h.next()
	src.edit(func(s *fakeSource) { s.profiles = nil })
	h.tr.SettingsChanged()
	c.ok(Window{Name: "w", UsedPercent: 99})
	h.settle()
	if st := h.tr.State(); len(st.Readings) != 0 {
		t.Fatalf("state %+v", st)
	}
	h.tr.mu.Lock()
	n := len(h.tr.accounts)
	h.tr.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d accounts left", n)
	}
}

// OnChange is called after a run's reading is kept and after the settings
// change. Each call records what the state held when it was made, so "a change
// was announced after the reading" is read from the call itself and not from
// whether a notification has arrived by the time a settle returned (the run
// ends, and is announced, a little after the account stops running).
func TestTrackerOnChange(t *testing.T) {
	type seen struct{ fetched, running bool }
	calls := make(chan seen, 64)
	var tr *Tracker
	h := newHarness(t, newSource(profile("a", "s", "", map[string][]string{"m": nil})), func(o *TrackerOptions) {
		o.OnChange = func() {
			var s seen
			for _, r := range tr.State().Readings {
				s.fetched = s.fetched || r.FetchedAt != nil
				s.running = s.running || r.Running
			}
			calls <- s
		}
	})
	tr = h.tr
	await := func(what string, ok func(seen) bool) {
		t.Helper()
		for {
			s := <-calls
			if ok(s) {
				return
			}
		}
	}
	h.start()
	c := h.next()
	await("the start of the run", func(s seen) bool { return s.running && !s.fetched })
	c.ok(Window{Name: "w", UsedPercent: 1})
	await("after a reading", func(s seen) bool { return s.fetched && !s.running })
	h.settle()
	for len(calls) > 0 {
		<-calls
	}
	h.tr.SettingsChanged()
	// SettingsChanged announces before it returns, so the call is there.
	select {
	case <-calls:
	default:
		t.Fatal("no change after the settings changed")
	}
	h.settle()
}

// Refresh answers only after the readings of the runs it waited for are
// kept: whoever reads the state file, or removes its folder, right after
// Refresh must not meet a write that is still under way.
func TestTrackerRefreshReturnsAfterTheReadingsAreKept(t *testing.T) {
	for i := range 200 {
		path := filepath.Join(t.TempDir(), "usage.json")
		tr := NewTracker(TrackerOptions{Source: newSource(profile("a", "s", "", map[string][]string{"m": nil})), StatePath: path,
			Run: func(context.Context, Job) (Result, error) {
				return Result{Windows: []Window{{Name: "session", UsedPercent: 10}}}, nil
			}})
		tr.Refresh(context.Background())
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil || len(entries) != 1 || entries[0].Name() != "usage.json" {
			t.Fatalf("round %d: after Refresh the folder holds %v (%v), want only usage.json", i, entries, err)
		}
	}
}

// Held at the boundary: while the state file is being written, Refresh has not
// answered yet; it answers once the write is let go and the file is there.
// Inside a synctest bubble, synctest.Wait returns only when every goroutine of
// the test (the Refresh call, the run, the held save) is blocked and none can
// go on, so "Refresh has not returned" is observed at that exact point and not
// after a guessed number of scheduler turns: a Refresh that answered before
// the save would already have closed done.
func TestTrackerRefreshWaitsForAHeldSave(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "usage.json")
		inSave, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		tr := NewTracker(TrackerOptions{Source: newSource(profile("a", "s", "", map[string][]string{"m": nil})), StatePath: path,
			saving: func() {
				once.Do(func() { close(inSave) })
				<-release
			},
			Run: func(context.Context, Job) (Result, error) {
				return Result{Windows: []Window{{Name: "session", UsedPercent: 10}}}, nil
			}})
		done := make(chan struct{})
		go func() {
			tr.Refresh(context.Background())
			close(done)
		}()
		<-inSave
		synctest.Wait()
		select {
		case <-done:
			close(release)
			t.Fatal("Refresh answered while the readings were still being written")
		default:
		}
		if _, err := os.Stat(path); err == nil {
			t.Fatal("the state file exists before the held save finished")
		}
		close(release)
		<-done // blocks the bubble until Refresh answers; a hang is reported as a deadlock
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("after Refresh the state file is missing: %v", err)
		}
	})
}
