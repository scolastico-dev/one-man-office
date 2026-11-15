package usage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log"
	"maps"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// RunTimeout bounds one run of a usage script, the runner's own 60 seconds
// included.
const RunTimeout = 70 * time.Second

// maxSleep is the longest the tracker waits without looking at the clock
// again, so that a clock that jumped (a suspended computer) is noticed.
const maxSleep = time.Minute

// Source is what the tracker reads; *settings.Store satisfies it.
type Source interface {
	Profiles() []api.Profile
	UsageScript(name string) (text string, customized, ok bool)
	General() settings.General
}

// TrackerOptions configure a Tracker.
type TrackerOptions struct {
	Source Source
	// Run runs one script for one account. It must return when its context
	// ends.
	Run RunFunc
	// StatePath is the file that keeps the last readings (<run>/usage.json);
	// "" keeps them in memory only.
	StatePath string
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// OnChange is called after any reading or state change, outside the
	// tracker's lock. It must not block.
	OnChange func()
	// Log receives the few lines the tracker writes; nil discards them.
	Log *log.Logger
	// Parallel is how many scripts run at once; 0 means 2.
	Parallel int

	// timerAt fires once at the given time of Now's clock; tests replace
	// it with a fake clock. The function returned stops the timer.
	timerAt func(at time.Time) (<-chan time.Time, func())
	// idle is called each time the loop is about to wait, with the time it
	// wakes at and the number of runs under way. Tests only.
	idle func(wake time.Time, running int)
	// runTimeout replaces RunTimeout when not 0. Tests only.
	runTimeout time.Duration
}

// Tracker runs the usage script of each account on a timer, keeps the last
// readings and judges every model against the thresholds. An account is a
// profile's script, account folder and environment: profiles that share all
// three share one reading. Its methods are safe for concurrent use.
type Tracker struct {
	o       TrackerOptions
	log     *log.Logger
	timeout time.Duration
	sem     chan struct{}
	poke    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	saveMu  sync.Mutex
	// syncMu keeps two reads of the settings from being applied out of
	// order.
	syncMu sync.Mutex

	mu       sync.Mutex
	closed   bool
	th       api.UsageSettings
	agentEnv map[string]string
	accounts map[string]*account
	models   map[string]modelRef
	kept     map[string]keptAccount
}

// modelRef is what judging a model needs: its account ("" without a script)
// and the windows that count for it.
type modelRef struct {
	key     string
	windows []string
}

// account is the tracker's record of one account.
type account struct {
	key      string
	script   string
	profiles []string
	job      Job
	print    string // changes when anything the run sees changes
	missing  bool   // the script does not exist

	windows   []api.UsageWindow
	fetchedAt time.Time
	err       string
	// staleSince is when the first failed run after the last success
	// ended; zero while the last run succeeded.
	staleSince time.Time
	backoff    backoff
	lastEnd    time.Time
	next       time.Time

	running bool // started or waiting for a free place
	started bool // the run function has been called
	again   bool // run once more as soon as the current run ends
	gone    bool // no profile uses it; kept until its run ends
	waiters []chan struct{}
}

// NewTracker makes a tracker and reads the kept readings and the settings at
// once, so that a model that was at its limit before omo stopped is judged
// so from the first question on. Runs start with Run or Refresh.
func NewTracker(o TrackerOptions) *Tracker {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Parallel <= 0 {
		o.Parallel = 2
	}
	t := &Tracker{
		o:        o,
		log:      o.Log,
		timeout:  RunTimeout,
		sem:      make(chan struct{}, o.Parallel),
		poke:     make(chan struct{}, 1),
		accounts: map[string]*account{},
		models:   map[string]modelRef{},
	}
	if t.log == nil {
		t.log = log.New(io.Discard, "", 0)
	}
	if o.runTimeout != 0 {
		t.timeout = o.runTimeout
	}
	if t.o.timerAt == nil {
		t.o.timerAt = t.realTimer
	}
	t.ctx, t.cancel = context.WithCancel(context.Background())
	kept, why := loadKept(o.StatePath, t.now())
	if why != "" {
		t.log.Print(keptIgnored + why)
	}
	t.kept = kept
	t.reconcile()
	t.mu.Lock()
	t.kept = nil // entries of accounts that no longer exist are dropped
	t.mu.Unlock()
	return t
}

func (t *Tracker) now() time.Time { return t.o.Now().Round(0) }

func (t *Tracker) realTimer(at time.Time) (<-chan time.Time, func()) {
	tm := time.NewTimer(at.Sub(t.now()))
	return tm.C, func() { tm.Stop() }
}

// Run runs due accounts until ctx ends, then stops every run under way and
// waits for them. Call it once.
func (t *Tracker) Run(ctx context.Context) {
	stop := context.AfterFunc(ctx, t.cancel)
	defer stop()
	for {
		wake, running := t.startDue()
		if t.o.idle != nil {
			t.o.idle(wake, running)
		}
		var fire <-chan time.Time
		stopTimer := func() {}
		if !wake.IsZero() {
			fire, stopTimer = t.o.timerAt(wake)
		}
		select {
		case <-t.ctx.Done():
			stopTimer()
			t.mu.Lock()
			t.closed = true
			t.mu.Unlock()
			t.wg.Wait()
			return
		case <-t.poke:
		case <-fire:
		}
		stopTimer()
	}
}

// startDue starts every idle account whose time has come and returns when
// the loop must look again (zero: only a poke) and how many runs are under
// way.
func (t *Tracker) startDue() (time.Time, int) {
	t.mu.Lock()
	now := t.now()
	interval := refreshInterval(t.th)
	var wake time.Time
	started := false
	for _, a := range t.accounts {
		if a.gone || a.missing || a.running {
			continue
		}
		if limit := now.Add(interval); a.next.After(limit) {
			a.next = limit // the clock went back
		}
		if !a.next.After(now) {
			t.launch(a)
			started = true
			continue
		}
		if wake.IsZero() || a.next.Before(wake) {
			wake = a.next
		}
	}
	running := 0
	for _, a := range t.accounts {
		if a.running {
			running++
		}
	}
	if limit := now.Add(maxSleep); !wake.IsZero() && wake.After(limit) {
		wake = limit
	}
	t.mu.Unlock()
	if started {
		t.changed()
	}
	return wake, running
}

// launch starts a run of a. The caller holds mu.
func (t *Tracker) launch(a *account) {
	if t.closed || t.ctx.Err() != nil {
		return
	}
	a.running, a.started = true, false
	a.next = time.Time{}
	t.wg.Add(1)
	go t.runOne(a)
}

// runOne waits for a free place, then runs the account's job as it is at
// that moment.
func (t *Tracker) runOne(a *account) {
	defer t.wg.Done()
	select {
	case t.sem <- struct{}{}:
	case <-t.ctx.Done():
		t.finish(a, Result{}, t.ctx.Err(), false)
		return
	}
	t.mu.Lock()
	a.started = true
	job := a.job
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.ctx, t.timeout)
	res, err := t.o.Run(ctx, job)
	deadline := err != nil && ctx.Err() == context.DeadlineExceeded
	cancel()
	<-t.sem
	t.finish(a, res, err, deadline)
}

// finish records the end of a run.
func (t *Tracker) finish(a *account, res Result, err error, deadline bool) {
	t.mu.Lock()
	a.running, a.started = false, false
	if t.ctx.Err() != nil || a.gone || a.missing {
		// Stopped, removed or its script was deleted: the result does not
		// count.
		a.again = false
		if a.gone && t.accounts[a.key] == a {
			delete(t.accounts, a.key)
		}
		t.release(a)
		t.mu.Unlock()
		return
	}
	now := t.now()
	var ws []api.UsageWindow
	if err == nil {
		ws, err = cleanWindows(res.Windows, now)
	}
	var limited *RateLimitError
	switch {
	case err == nil:
		a.windows, a.fetchedAt, a.err, a.staleSince, a.backoff = ws, now, "", time.Time{}, backoff{}
	case errors.As(err, &limited):
		a.err = RateLimitText
		a.backoff.failures++
		a.backoff.limited++
		a.backoff.retryAfter = limited.RetryAfter
	default:
		a.err = errText(err, deadline)
		a.backoff.failures++
		a.backoff.limited, a.backoff.retryAfter = 0, 0
	}
	if err != nil && a.staleSince.IsZero() {
		a.staleSince = now
	}
	a.lastEnd = now
	if a.again {
		a.again = false
		t.launch(a)
	} else {
		a.next = nextDue(a.lastEnd, a.backoff, a.windows, refreshInterval(t.th), now)
		t.release(a)
	}
	t.mu.Unlock()
	t.save()
	t.changed()
	t.wake()
}

// release tells everyone waiting for a that its runs are over. The caller
// holds mu.
func (t *Tracker) release(a *account) {
	for _, ch := range a.waiters {
		close(ch)
	}
	a.waiters = nil
}

func (t *Tracker) wake() {
	select {
	case t.poke <- struct{}{}:
	default:
	}
}

func (t *Tracker) changed() {
	if t.o.OnChange != nil {
		t.o.OnChange()
	}
}

// Refresh runs every account now and waits for the runs, at most until ctx
// ends. An account whose run is under way runs once more right after it, so
// the answer reflects the state at the call. It returns the state then.
func (t *Tracker) Refresh(ctx context.Context) api.UsageState {
	t.mu.Lock()
	var waits []chan struct{}
	if !t.closed {
		for _, a := range t.accounts {
			if a.gone || a.missing {
				continue
			}
			switch {
			case a.started:
				a.again = true
			case !a.running:
				t.launch(a)
			}
			if a.running {
				ch := make(chan struct{})
				a.waiters = append(a.waiters, ch)
				waits = append(waits, ch)
			}
		}
	}
	t.mu.Unlock()
	if len(waits) > 0 {
		t.changed()
	}
	for _, ch := range waits {
		select {
		case <-ch:
		case <-ctx.Done():
			return t.State()
		}
	}
	return t.State()
}

// SettingsChanged reads the profiles, scripts and usage settings again. A new
// account, or one whose script, agent environment or profile changed, runs
// at once; an account no profile uses any more is dropped.
func (t *Tracker) SettingsChanged() {
	t.reconcile()
	t.changed()
	t.wake()
}

// reconcile rebuilds the accounts and models from the source.
func (t *Tracker) reconcile() {
	t.syncMu.Lock()
	defer t.syncMu.Unlock()
	profiles := t.o.Source.Profiles()
	g := api.General(t.o.Source.General())
	type found struct {
		script   string
		text     string
		ok       bool
		profiles []api.Profile
	}
	accounts := map[string]*found{}
	models := map[string]modelRef{}
	texts := map[string]*found{}
	for _, p := range profiles {
		key := ""
		if p.UsageScript != "" {
			key = AccountKey(p.UsageScript, p)
			f := accounts[key]
			if f == nil {
				s := texts[p.UsageScript]
				if s == nil {
					s = &found{script: p.UsageScript}
					s.text, _, s.ok = t.o.Source.UsageScript(p.UsageScript)
					texts[p.UsageScript] = s
				}
				f = &found{script: s.script, text: s.text, ok: s.ok}
				accounts[key] = f
			}
			f.profiles = append(f.profiles, p)
		}
		for name, m := range p.Models {
			models[p.Name+"/"+name] = modelRef{key: key, windows: slices.Clone(m.UsageWindows)}
		}
	}

	t.mu.Lock()
	now := t.now()
	t.th = cloneSettings(g.Usage)
	t.agentEnv = maps.Clone(g.AgentEnv)
	t.models = models
	interval := refreshInterval(t.th)
	for key, a := range t.accounts {
		if _, ok := accounts[key]; !ok && !a.gone {
			a.gone, a.again = true, false
			if !a.running {
				t.release(a)
				delete(t.accounts, key)
			}
		}
	}
	for key, f := range accounts {
		sort.Slice(f.profiles, func(i, j int) bool { return f.profiles[i].Name < f.profiles[j].Name })
		job := Job{Script: f.script, Text: f.text, Profile: cloneProfile(f.profiles[0]), AgentEnv: maps.Clone(t.agentEnv)}
		names := make([]string, len(f.profiles))
		for i, p := range f.profiles {
			names[i] = p.Name
		}
		print := fingerprint(job, !f.ok)
		a := t.accounts[key]
		due := false
		switch {
		case a == nil:
			a = &account{key: key, script: f.script}
			if k, ok := t.kept[key]; ok && k.Script == f.script {
				a.windows, a.fetchedAt = k.Windows, k.FetchedAt
			}
			t.accounts[key] = a
			due = true
		case a.gone || a.print != print:
			// Back after removal, or something the run sees changed: a
			// run under way does not count for the new state.
			a.gone = false
			a.backoff = backoff{}
			due = true
		}
		wasMissing := a.missing
		a.profiles, a.job, a.print, a.missing = names, job, print, !f.ok
		switch {
		case a.missing:
			a.err = "the script " + f.script + " does not exist"
			a.windows, a.fetchedAt, a.next, a.again, a.backoff, a.staleSince = nil, time.Time{}, time.Time{}, false, backoff{}, time.Time{}
		case wasMissing:
			a.err = ""
		}
		switch {
		case a.missing:
		case due && a.started:
			a.again = true
		case due && a.running:
			// Not started yet: it runs the new job.
		case due:
			a.next = now
		case !a.running && !a.lastEnd.IsZero():
			a.next = nextDue(a.lastEnd, a.backoff, a.windows, interval, now)
		}
	}
	t.mu.Unlock()
}

// fingerprint changes when anything a run of the job sees changes.
func fingerprint(j Job, missing bool) string {
	raw, _ := json.Marshal([]any{missing, j.Script, j.Text, j.AgentEnv, j.Profile})
	sum := sha256.Sum256(raw)
	return string(sum[:])
}

func cloneSettings(s api.UsageSettings) api.UsageSettings {
	out := s
	out.Windows = make(map[string]api.UsageThreshold, len(s.Windows))
	for k, v := range s.Windows {
		c := api.UsageThreshold{}
		if v.SoftPercent != nil {
			x := *v.SoftPercent
			c.SoftPercent = &x
		}
		if v.HardPercent != nil {
			x := *v.HardPercent
			c.HardPercent = &x
		}
		out.Windows[k] = c
	}
	return out
}

func cloneProfile(p api.Profile) api.Profile {
	p.Args = slices.Clone(p.Args)
	p.Env = maps.Clone(p.Env)
	models := make(map[string]api.ProfileModel, len(p.Models))
	for k, m := range p.Models {
		m.Args = slices.Clone(m.Args)
		m.Env = maps.Clone(m.Env)
		m.UsageWindows = slices.Clone(m.UsageWindows)
		models[k] = m
	}
	p.Models = models
	return p
}

// save writes the windows of every account's last successful run, also
// while its later runs fail.
func (t *Tracker) save() {
	if t.o.StatePath == "" {
		return
	}
	t.saveMu.Lock()
	defer t.saveMu.Unlock()
	t.mu.Lock()
	var out []keptAccount
	for _, a := range t.accounts {
		if a.gone || a.missing || a.fetchedAt.IsZero() {
			continue
		}
		out = append(out, keptAccount{Key: a.key, Script: a.script, Windows: slices.Clone(a.windows), FetchedAt: a.fetchedAt.UTC()})
	}
	t.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if err := saveKept(t.o.StatePath, out); err != nil {
		t.log.Print("usage: the usage readings could not be kept: the file could not be written")
	}
}

// State returns the readings of every account and the state of every model
// of every profile. It is a copy; lists are never nil.
func (t *Tracker) State() api.UsageState {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	readings := make(map[string]*api.UsageReading, len(t.accounts))
	out := api.UsageState{Readings: make([]api.UsageReading, 0, len(t.accounts)), Models: make([]api.ModelUsage, 0, len(t.models))}
	for key, a := range t.accounts {
		if a.gone {
			continue
		}
		r := a.reading()
		readings[key] = &r
		out.Readings = append(out.Readings, r)
	}
	sort.Slice(out.Readings, func(i, j int) bool {
		a, b := out.Readings[i], out.Readings[j]
		if a.Script != b.Script {
			return a.Script < b.Script
		}
		if a.Profiles[0] != b.Profiles[0] {
			return a.Profiles[0] < b.Profiles[0]
		}
		return a.Key < b.Key
	})
	ids := slices.Sorted(maps.Keys(t.models))
	for _, id := range ids {
		ref := t.models[id]
		var r *api.UsageReading
		if ref.key != "" {
			r = readings[ref.key]
		}
		out.Models = append(out.Models, Evaluate(id, ref.windows, r, t.th, now))
	}
	return out
}

// Model returns the state of one model by id (<profile>/<model>). A model
// the tracker does not know is unmetered.
func (t *Tracker) Model(id string) api.ModelUsage {
	t.mu.Lock()
	defer t.mu.Unlock()
	ref, ok := t.models[id]
	if !ok || ref.key == "" {
		return api.ModelUsage{Model: id, State: api.UsageUnmetered}
	}
	var r *api.UsageReading
	if a := t.accounts[ref.key]; a != nil && !a.gone {
		rd := a.reading()
		r = &rd
	}
	return Evaluate(id, ref.windows, r, t.th, t.now())
}

// reading is the wire form of a. The caller holds the tracker's lock.
func (a *account) reading() api.UsageReading {
	r := api.UsageReading{
		Key:      a.key,
		Script:   a.script,
		Profiles: slices.Clone(a.profiles),
		Windows:  make([]api.UsageWindow, 0, len(a.windows)),
		Running:  a.running,
		Error:    a.err,
	}
	if r.Profiles == nil {
		r.Profiles = []string{}
	}
	for _, w := range a.windows {
		if w.ResetsAt != nil {
			at := *w.ResetsAt
			w.ResetsAt = &at
		}
		r.Windows = append(r.Windows, w)
	}
	if !a.fetchedAt.IsZero() {
		at := a.fetchedAt
		r.FetchedAt = &at
	}
	if !a.staleSince.IsZero() {
		at := a.staleSince
		r.StaleSince = &at
	}
	if !a.running && !a.missing && !a.next.IsZero() {
		at := a.next
		r.NextAt = &at
	}
	return r
}
