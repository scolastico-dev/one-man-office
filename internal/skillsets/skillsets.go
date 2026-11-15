// Package skillsets keeps one checkout per skill set below the omo home and
// builds the text that points agents at them.
//
// A skill set is a git repository the user names in skillsets/<name>.yaml.
// omo checks it out into skills/<name> through gitops.FetchSource (no
// hooks, filters, submodules or links; a fetch replaces the folder) and
// tells the agents of a role where it is, with the user's text for that
// role (see Text). The checkout is shared by every agent. Agents run as the
// same OS user and can change files in it; omo does not watch for that: the
// next update replaces the whole folder with a fresh checkout, so changes
// last until then. omo itself never reads the checkout's files.
//
// Fetches run in the background, at most two at a time and never two of
// one set at once. A fetch that fails or is cancelled keeps the last good
// checkout and records a short, credential-free message.
package skillsets

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// States of a skill set's checkout.
const (
	StateMissing  = "missing"
	StateUpdating = "updating"
	StateOK       = "ok"
	StateFailed   = "failed"
)

// parallel is how many fetches run at once.
const parallel = 2

// maxMessage is the most characters of a failure message that are kept.
const maxMessage = 500

// DefaultPoll is how often the manager compares the settings with its
// checkouts, to notice skill set files written by hand.
const DefaultPoll = 5 * time.Second

// Options configure a Manager.
type Options struct {
	// Dir is the skills folder (globalhome.Layout.Skills); "" disables
	// every checkout.
	Dir   string
	Git   *gitops.Git
	Store *settings.Store
	// Log receives what goes wrong; nil discards.
	Log *log.Logger
	// OnChange is called with a set's new state (skillset.changed); nil
	// drops it. It is not called once the manager stops.
	OnChange func(api.SkillSetState)
	// Poll is how often the settings are compared with the checkouts; 0 is
	// DefaultPoll.
	Poll time.Duration
	// Now is the clock of UpdatedAt; nil is time.Now.
	Now func() time.Time
}

// source is what a checkout was made from.
type source struct{ repository, ref string }

// job is the fetch of one set: running, maybe asked to run again.
type job struct {
	cancel  context.CancelFunc
	done    chan struct{}
	again   bool
	removed bool
}

// Manager keeps the checkouts of the skill sets.
type Manager struct {
	o   Options
	now func() time.Time
	sem chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	started bool
	stopped bool
	states  map[string]api.SkillSetState
	seen    map[string]source
	jobs    map[string]*job
	locks   map[string]*sync.Mutex
}

// New returns a manager; Start begins its work.
func New(o Options) *Manager {
	if o.Log == nil {
		o.Log = log.New(io.Discard, "", 0)
	}
	if o.Poll <= 0 {
		o.Poll = DefaultPoll
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		o: o, now: now, sem: make(chan struct{}, parallel),
		ctx: ctx, cancel: cancel,
		states: map[string]api.SkillSetState{},
		seen:   map[string]source{},
		jobs:   map[string]*job{},
		locks:  map[string]*sync.Mutex{},
	}
}

// Start begins the manager's work in the background and returns at once:
// it removes checkouts of sets that no longer exist and leftovers of
// interrupted fetches, updates every set whose update is at_start, fetches
// every set without checkout, and from then on compares the settings with
// the checkouts every Poll. Everything ends when ctx ends or Stop is
// called.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started || m.stopped || m.o.Dir == "" {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.wg.Add(1)
	m.mu.Unlock()
	stop := context.AfterFunc(ctx, m.cancel)
	go func() {
		defer m.wg.Done()
		defer stop()
		m.cleanFolder()
		for _, k := range m.o.Store.SkillSets() {
			m.mu.Lock()
			m.seen[k.Name] = source{k.Repository, k.Ref}
			m.mu.Unlock()
			if _, ok := m.Checkout(k.Name); !ok || k.Update == api.SkillSetAtStart {
				m.enqueue(k.Name)
			}
		}
		t := time.NewTicker(m.o.Poll)
		defer t.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-t.C:
				m.Reconcile()
			}
		}
	}()
}

// Stop cancels every fetch and waits until the manager's work has ended.
// A cancelled fetch leaves the previous checkout as it was.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
	m.cancel()
	m.wg.Wait()
}

// Checkout returns the checkout folder of the set name when it exists.
func (m *Manager) Checkout(name string) (string, bool) {
	return CheckoutIn(m.o.Dir)(name)
}

// Reconcile compares the settings with the checkouts: a new set without
// checkout is fetched; a set whose repository or ref changed is fetched
// when its update is at_start or it has no checkout (a pinned set keeps its
// checkout until Update); the checkout of a set whose file is gone is
// removed. A set whose fetch failed is not fetched again until its source
// changes or Update is asked for.
func (m *Manager) Reconcile() {
	if m.ctx.Err() != nil || m.o.Dir == "" {
		return
	}
	sets := m.o.Store.SkillSets()
	present := map[string]bool{}
	var fetch []string
	m.mu.Lock()
	for _, k := range sets {
		present[k.Name] = true
		src := source{k.Repository, k.Ref}
		prev, known := m.seen[k.Name]
		m.seen[k.Name] = src
		_, has := CheckoutIn(m.o.Dir)(k.Name)
		switch {
		case !known && !has:
			fetch = append(fetch, k.Name)
		case known && prev != src && (k.Update == api.SkillSetAtStart || !has):
			fetch = append(fetch, k.Name)
		}
	}
	var gone []string
	for name := range m.seen {
		if !present[name] && m.o.Store.Revision(settings.SectionSkillSets, name) == api.NoRevision {
			gone = append(gone, name)
		}
	}
	m.mu.Unlock()
	for _, name := range fetch {
		m.enqueue(name)
	}
	for _, name := range gone {
		m.Removed(name)
	}
}

// Update fetches the set name now (joining a fetch that is already
// running, which then runs once more) and waits for it or for ctx. It
// returns the set's state then.
func (m *Manager) Update(ctx context.Context, name string) api.SkillSetState {
	if j := m.enqueue(name); j != nil {
		select {
		case <-j.done:
		case <-ctx.Done():
		}
	}
	return m.State(name)
}

// Removed forgets the set name and deletes its checkout: a running fetch is
// cancelled and awaited first. When the set exists again by then (written
// anew), its checkout is kept.
func (m *Manager) Removed(name string) {
	if !namePattern.MatchString(name) {
		return
	}
	m.mu.Lock()
	j := m.jobs[name]
	if j != nil {
		j.removed = true
		j.cancel()
	}
	delete(m.states, name)
	delete(m.seen, name)
	m.mu.Unlock()
	if j != nil {
		<-j.done
	}
	if m.o.Dir == "" {
		return
	}
	l := m.lock(name)
	l.Lock()
	defer l.Unlock()
	if _, ok := m.o.Store.SkillSet(name); ok {
		return
	}
	if err := os.RemoveAll(filepath.Join(m.o.Dir, name)); err != nil {
		m.o.Log.Printf("skill set %s: remove the checkout: %v", name, err)
	}
}

// States returns the state of every skill set of the settings, sorted by
// name; never nil.
func (m *Manager) States() []api.SkillSetState {
	sets := m.o.Store.SkillSets()
	out := make([]api.SkillSetState, 0, len(sets))
	for _, k := range sets {
		out = append(out, m.State(k.Name))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// State returns the state of the set name: what the last fetch of this run
// left, else what is on disk (ok with the checkout's commit, or missing).
func (m *Manager) State(name string) api.SkillSetState {
	m.mu.Lock()
	st, ok := m.states[name]
	m.mu.Unlock()
	if ok {
		return st
	}
	return m.diskState(name)
}

// diskState describes the checkout of name as it is on disk.
func (m *Manager) diskState(name string) api.SkillSetState {
	st := api.SkillSetState{Name: name, State: StateMissing}
	if m.o.Dir == "" || !namePattern.MatchString(name) {
		return st
	}
	st.Path = filepath.Join(m.o.Dir, name)
	path, ok := m.Checkout(name)
	if !ok {
		return st
	}
	commit, err := m.o.Git.SourceCommit(context.Background(), path)
	if err != nil {
		return st
	}
	st.State, st.Commit = StateOK, commit
	if fi, err := os.Lstat(path); err == nil {
		at := fi.ModTime().UTC()
		st.UpdatedAt = &at
	}
	return st
}

// lock returns the lock that serializes fetching and removing the checkout
// of name.
func (m *Manager) lock(name string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[name]
	if !ok {
		l = &sync.Mutex{}
		m.locks[name] = l
	}
	return l
}

// enqueue starts a fetch of name, or asks a running one to run once more.
// It returns nil once the manager stopped.
func (m *Manager) enqueue(name string) *job {
	if !namePattern.MatchString(name) || m.o.Dir == "" {
		return nil
	}
	m.mu.Lock()
	if m.stopped || m.ctx.Err() != nil {
		m.mu.Unlock()
		return nil
	}
	if j := m.jobs[name]; j != nil {
		j.again = true
		m.mu.Unlock()
		return j
	}
	ctx, cancel := context.WithCancel(m.ctx)
	j := &job{cancel: cancel, done: make(chan struct{})}
	m.jobs[name] = j
	st := m.stateLocked(name)
	st.State, st.Message = StateUpdating, ""
	m.states[name] = st
	m.wg.Add(1)
	m.mu.Unlock()
	m.changed(st)
	go m.run(ctx, name, j)
	return j
}

// stateLocked is State with m.mu held.
func (m *Manager) stateLocked(name string) api.SkillSetState {
	if st, ok := m.states[name]; ok {
		return st
	}
	return m.diskState(name)
}

// run fetches name until no further run was asked for.
func (m *Manager) run(ctx context.Context, name string, j *job) {
	defer m.wg.Done()
	defer func() {
		j.cancel()
		m.mu.Lock()
		if m.jobs[name] == j {
			delete(m.jobs, name)
		}
		m.mu.Unlock()
		close(j.done)
	}()
	for {
		m.fetchOnce(ctx, name, j)
		m.mu.Lock()
		again := j.again && !j.removed && ctx.Err() == nil
		j.again = false
		if again {
			st := m.stateLocked(name)
			st.State, st.Message = StateUpdating, ""
			m.states[name] = st
		}
		m.mu.Unlock()
		if !again {
			return
		}
	}
}

// fetchOnce fetches name once, at most parallel at a time, and records the
// outcome.
func (m *Manager) fetchOnce(ctx context.Context, name string, j *job) {
	select {
	case m.sem <- struct{}{}:
	case <-ctx.Done():
		m.finish(name, j, "", ctx.Err())
		return
	}
	defer func() { <-m.sem }()
	l := m.lock(name)
	l.Lock()
	defer l.Unlock()
	k, ok := m.o.Store.SkillSet(name)
	if !ok {
		m.finish(name, j, "", errors.New("the skill set no longer exists"))
		return
	}
	m.mu.Lock()
	if !j.removed {
		m.seen[name] = source{k.Repository, k.Ref}
	}
	m.mu.Unlock()
	commit, err := m.o.Git.FetchSource(ctx, k.Repository, k.Ref, filepath.Join(m.o.Dir, name))
	if err != nil && ctx.Err() == nil {
		m.o.Log.Printf("skill set %s: %s", name, Message(err))
	}
	m.finish(name, j, commit, err)
}

// finish records the outcome of a fetch and reports it, unless the set was
// removed meanwhile or the manager stops.
func (m *Manager) finish(name string, j *job, commit string, err error) {
	m.mu.Lock()
	if j.removed {
		m.mu.Unlock()
		return
	}
	st := api.SkillSetState{Name: name, State: StateOK, Path: filepath.Join(m.o.Dir, name), Commit: commit}
	if err == nil {
		at := m.now().UTC()
		st.UpdatedAt = &at
	} else {
		// The last good checkout stays; say what it is.
		st = m.diskState(name)
		st.State = StateFailed
		st.Message = Message(err)
		if m.ctx.Err() != nil {
			st.Message = "omo stopped before the update finished"
		}
	}
	m.states[name] = st
	stopping := m.stopped || m.ctx.Err() != nil
	m.mu.Unlock()
	if !stopping {
		m.changed(st)
	}
}

// changed reports a state.
func (m *Manager) changed(st api.SkillSetState) {
	if m.o.OnChange != nil {
		m.o.OnChange(st)
	}
}

// cleanFolder removes what does not belong into the skills folder: the
// checkout of a set that has no settings file any more (also no broken
// one) and leftovers of fetches that were interrupted. Entries whose names
// are not set names are left alone.
func (m *Manager) cleanFolder() {
	entries, err := os.ReadDir(m.o.Dir)
	if err != nil {
		m.o.Log.Printf("skill sets: read %s: %v", m.o.Dir, err)
		return
	}
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(m.o.Dir, name)
		switch {
		case strings.HasPrefix(name, ".") && leftover(name[1:]):
		case namePattern.MatchString(name):
			if _, ok := m.o.Store.SkillSet(name); ok || m.o.Store.Revision(settings.SectionSkillSets, name) != api.NoRevision {
				continue
			}
		default:
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			m.o.Log.Printf("skill sets: remove %s: %v", path, err)
		}
	}
}

// leftover reports whether name (without its leading dot) is a temporary
// folder of gitops.FetchSource: "<set>.new-<random>" or
// "<set>.old-<random>".
func leftover(name string) bool {
	for _, mark := range []string{".new-", ".old-"} {
		if i := strings.LastIndex(name, mark); i > 0 && namePattern.MatchString(name[:i]) {
			return true
		}
	}
	return false
}

// Message is the text kept for a failed fetch: one line without
// credentials or hidden characters, at most 500 characters.
func Message(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "the update was cancelled"
	}
	text := strings.Join(strings.Fields(clean(gitops.HideCredentials(err.Error()))), " ")
	if r := []rune(text); len(r) > maxMessage {
		text = string(r[:maxMessage-1]) + "…"
	}
	if text == "" {
		text = "the update failed"
	}
	return text
}
