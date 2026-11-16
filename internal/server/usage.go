package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/engine"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/usage"
)

// usageFeature holds the state of the usage routes and of the usage tracker.
type usageFeature struct {
	tracker *usage.Tracker
	// changed carries the tracker's "something changed" to the goroutine
	// that records usage.changed; one pending signal stands for any number.
	changed chan struct{}
	// engine is woken after a change of usage; set once the engine exists.
	engine atomic.Pointer[engine.Engine]
	// test lets one script test run at a time.
	test sync.Mutex

	runMu sync.RWMutex
	run   usage.RunFunc
}

// newUsageRun makes the function that runs usage scripts: the real runner,
// with the user's home folder and real network access. Tests replace it.
var newUsageRun = func() usage.RunFunc { return usage.NewRunFunc(usage.RunOptions{}) }

// scriptNamePattern is the settings store's pattern for item names: a
// script test under any other name is refused, as saving it would be.
var scriptNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// setRun replaces how scripts run, for the tracker and the test route.
func (u *usageFeature) setRun(f usage.RunFunc) {
	u.runMu.Lock()
	defer u.runMu.Unlock()
	u.run = f
}

// runScript runs one job with the current run function.
func (u *usageFeature) runScript(ctx context.Context, j usage.Job) (usage.Result, error) {
	u.runMu.RLock()
	run := u.run
	u.runMu.RUnlock()
	return run(ctx, j)
}

// registerUsage registers the usage routes (and POST /api/tickets/{n}/start-anyway).
// Every one is the user's alone: usage scripts are code that runs as the
// user with the user's credentials, and the readings show the user's
// accounts and how much of them is used.
func (s *Server) registerUsage(mux *http.ServeMux) {
	// The routes are built once the engine exists: from now on a change of
	// usage wakes it.
	if s.engine != nil {
		s.usage.engine.Store(s.engine)
	}
	s.registerRoutes(mux, []apiRoute{
		{"GET /api/usage", s.getUsage, userOnly},
		{"POST /api/usage/refresh", s.refreshUsage, userOnly},
		{"GET /api/usage/scripts", s.listUsageScripts, userOnly},
		{"GET /api/usage/scripts/{name}", s.getUsageScript, userOnly},
		{"PUT /api/usage/scripts/{name}", s.putUsageScript, userOnly},
		{"DELETE /api/usage/scripts/{name}", s.deleteUsageScript, userOnly},
		{"POST /api/usage/scripts/{name}/test", s.testUsageScript, userOnly},
		{"POST /api/tickets/{n}/start-anyway", s.startAnyway, userOnly},
	})
}

// startUsage starts the usage tracker. It runs after the board and the
// settings exist and before the engine starts. The tracker reads the kept
// readings at once, so the engine's first question already sees a model
// that was at its limit before omo stopped.
func (s *Server) startUsage(ctx context.Context) {
	u := &s.usage
	u.run = newUsageRun()
	u.changed = make(chan struct{}, 1)
	u.tracker = usage.NewTracker(usage.TrackerOptions{
		Source:    s.settings,
		Run:       u.runScript,
		StatePath: filepath.Join(s.opts.Layout.Run, "usage.json"),
		OnChange:  s.usageChanged,
		Log:       s.log,
	})
	s.background(u.tracker.Run)
	s.background(s.writeUsageEvents)
}

// stopUsage stops the usage tracker before the database is closed. The
// tracker, the event writer and every script test run as background work of
// the server: Close has cancelled them and waited for them before it calls
// stopUsage, so nothing is left to stop here.
func (s *Server) stopUsage() {}

// usageView is what the engine asks about models; nil means every model is
// unmetered.
func (s *Server) usageView() engine.UsageView {
	if s.usage.tracker == nil {
		return nil
	}
	return s.usage.tracker
}

// usageChanged is the tracker's OnChange: it must not block.
func (s *Server) usageChanged() {
	select {
	case s.usage.changed <- struct{}{}:
	default:
	}
}

// writeUsageEvents records usage.changed (the tracker's state: names,
// numbers, times and its cleaned error texts only) after each change that
// changed the state, and wakes the event stream and the engine, until ctx
// ends.
func (s *Server) writeUsageEvents(ctx context.Context) {
	var last []byte
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.usage.changed:
		}
		data, err := json.Marshal(s.usage.tracker.State())
		if err != nil {
			s.log.Printf("record %s event: %v", api.KindUsageChanged, err)
			continue
		}
		if bytes.Equal(data, last) {
			continue
		}
		last = data
		if err := s.appendSystemEvent(api.KindUsageChanged, json.RawMessage(data)); err != nil {
			s.log.Printf("record %s event: %v", api.KindUsageChanged, err)
		}
		s.events.Wake()
		if e := s.usage.engine.Load(); e != nil {
			e.Wake()
		}
	}
}

// usageSettingsChanged tells the tracker about a change of what its accounts
// and thresholds are made of: profiles, usage scripts and the general
// settings (thresholds, agent environment).
func (s *Server) usageSettingsChanged(c settings.Change) {
	if s.usage.tracker == nil {
		return
	}
	switch c.Section {
	case settings.SectionProfiles, settings.SectionUsage, settings.SectionGeneral:
		s.usage.tracker.SettingsChanged()
	}
}

// boundedByServer returns ctx bounded by d and ended when the server
// closes.
func (s *Server) boundedByServer(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, d)
	stop := context.AfterFunc(s.ctx, cancel)
	return ctx, func() { stop(); cancel() }
}

func (s *Server) getUsage(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.usage.tracker.State())
}

// refreshUsage runs every account's script now and answers the state once
// the runs ended, at most after one run's limit. A run under way runs once
// more right after it (the tracker's rule), so the answer is not older than
// the request.
func (s *Server) refreshUsage(w http.ResponseWriter, r *http.Request) {
	if err := decodeEmpty(w, r); err != nil {
		writeError(w, r, err)
		return
	}
	ctx, cancel := s.boundedByServer(r.Context(), usage.RunTimeout)
	defer cancel()
	writeJSON(w, http.StatusOK, s.usage.tracker.Refresh(ctx))
}

func (s *Server) listUsageScripts(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settings.UsageScripts())
}

// usageScriptResponse is one script with its default and its revision (read
// before the text).
func (s *Server) usageScriptResponse(name string) (api.UsageScript, error) {
	rev := s.settings.Revision(settings.SectionUsage, name)
	text, customized, ok := s.settings.UsageScript(name)
	if !ok {
		return api.UsageScript{}, api.Errorf(api.CodeNotFound, "no usage script %s", name)
	}
	def, hasDefault := s.settings.UsageDefault(name)
	return api.UsageScript{
		Name:       name,
		Text:       text,
		Default:    def,
		HasDefault: hasDefault,
		Customized: customized,
		Revision:   rev,
	}, nil
}

func (s *Server) getUsageScript(w http.ResponseWriter, r *http.Request) {
	out, err := s.usageScriptResponse(r.PathValue("name"))
	reply(w, r, http.StatusOK, out, err)
}

// putUsageScript saves a script, guarded by If-Match ("0" = create only).
// The store checks it with usage.Check before it writes; a refusal names
// the field text.
func (s *Server) putUsageScript(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.UsageScriptUpdate
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := r.PathValue("name")
	if err := s.settings.PutUsageScriptIf(rev, name, req.Text); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	out, err := s.usageScriptResponse(name)
	reply(w, r, http.StatusOK, out, err)
}

// deleteUsageScript resets a script to its default, or deletes one without
// a default (409 in_use while a profile names it).
func (s *Server) deleteUsageScript(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.settings.ResetUsageScriptIf(rev, r.PathValue("name")); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// testUsageScript runs a draft of a script once, as the named profile's
// account ("" = no profile), with the real helpers and the user's real
// credentials, and answers its windows, its error and its log (both
// cleaned by the runner of every secret the run touched) and how long it
// took. A failing script is an answer, not a refusal. One test runs at a
// time; it is bounded by one run's limit, does not end when the client
// leaves and ends when the server closes. Nothing is saved.
func (s *Server) testUsageScript(w http.ResponseWriter, r *http.Request) {
	var req api.UsageTest
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := r.PathValue("name")
	if !scriptNamePattern.MatchString(name) {
		writeError(w, r, api.Errorf(api.CodeNotFound, "no usage script can have that name"))
		return
	}
	if len(req.Text) > usage.MaxScript {
		e := api.Errorf(api.CodeValidation, "a usage script must be at most %d KiB", usage.MaxScript>>10)
		e.Details = api.FieldDetail{Field: "text"}
		writeError(w, r, e)
		return
	}
	var profile api.Profile
	if req.Profile != "" {
		p, ok := s.settings.Profile(req.Profile)
		if !ok {
			e := api.Errorf(api.CodeValidation, "there is no profile of that name")
			e.Details = api.FieldDetail{Field: "profile"}
			writeError(w, r, e)
			return
		}
		profile = p
	}
	job := usage.Job{Script: name, Text: req.Text, Profile: profile, AgentEnv: s.settings.General().AgentEnv}
	if !s.usage.test.TryLock() {
		writeError(w, r, api.Errorf(api.CodeConflict, "A usage script test is already running. Try again when it has finished."))
		return
	}
	var (
		res     usage.Result
		runErr  error
		elapsed time.Duration
	)
	finished := make(chan struct{})
	started := s.background(func(ctx context.Context) {
		// The lock is let go before the answer can be written (defers run last in, first out).
		defer close(finished)
		defer s.usage.test.Unlock()
		ctx, cancel := context.WithTimeout(ctx, usage.RunTimeout)
		defer cancel()
		begin := time.Now()
		res, runErr = s.usage.runScript(ctx, job)
		elapsed = time.Since(begin)
	})
	if !started {
		s.usage.test.Unlock()
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is shutting down"))
		return
	}
	<-finished
	out := api.UsageTestResult{Windows: []api.UsageWindow{}, Log: []string{}, Millis: int(elapsed.Milliseconds())}
	if runErr != nil {
		out.Error = runErr.Error()
	} else {
		for _, win := range res.Windows {
			uw := api.UsageWindow{Name: win.Name, UsedPercent: win.UsedPercent}
			if !win.ResetsAt.IsZero() {
				at := win.ResetsAt.UTC()
				uw.ResetsAt = &at
			}
			out.Windows = append(out.Windows, uw)
		}
	}
	out.Log = append(out.Log, res.Log...)
	writeJSON(w, http.StatusOK, out)
}

// startAnyway lets the next start of a ticket that waits for usage on its
// own model use that model once. The user only (route table).
func (s *Server) startAnyway(w http.ResponseWriter, r *http.Request) {
	n, err := pathInt(r, "n")
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.StartAnyway
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if !s.needEngine(w, r) {
		return
	}
	if err := s.engine.StartAnyway(r.Context(), n); err != nil {
		writeError(w, r, err)
		return
	}
	out, err := s.board.Get(r.Context(), actorFrom(r), n)
	reply(w, r, http.StatusOK, out, err)
}
