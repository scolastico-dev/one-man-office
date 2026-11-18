package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/textsafe"
)

// Bounds of the plugin routes.
const (
	// pluginInstallBound bounds an install, update, change or removal:
	// the fetch itself is bounded to 5 minutes by gitops, the copy and
	// checks follow.
	pluginInstallBound = 6 * time.Minute
	// pluginActionBound bounds a manual action (a hook's own time limit is
	// at most 5 minutes too).
	pluginActionBound = 5 * time.Minute
	// pluginLogLines is how many log lines the log route answers.
	pluginLogLines = 200
	// maxPluginMessage bounds the reason of a refused install, in
	// characters.
	maxPluginMessage = 500
	// pluginFollowInterval is how often the dispatch reads the event log
	// for new events; pluginFollowBatch is how many one read fetches.
	pluginFollowInterval = 250 * time.Millisecond
	pluginFollowBatch    = 500
	// catalogFresh is how long a fetched plugin list is used before it is
	// fetched again; catalogRetry is how long a failure is answered before
	// the next try (Retry on the page fetches at once). catalogBound bounds
	// one fetch of the list.
	catalogFresh = 10 * time.Minute
	catalogRetry = time.Minute
	catalogBound = 2 * time.Minute
)

// pluginFeature holds the state of the plugin routes and of the plugin
// runtime.
type pluginFeature struct {
	m *plugins.Manager
	o pluginmanager.Options

	// reload carries "the plugin settings changed" from the settings drain
	// to the goroutine that reloads the runtime; one pending signal stands
	// for any number.
	reload chan struct{}

	// mu serializes loading the runtime and removing a plugin. loaded is
	// the name and revision of every plugin of the last load; valid is
	// false until the first load and after a load that left one out.
	mu     sync.Mutex
	loaded []string
	valid  bool

	// busy names the plugins an install, update, change or removal works
	// on; a second one for the same plugin is refused.
	busyMu sync.Mutex
	busy   map[string]bool

	// pending holds the newest info of every plugin whose state changed
	// and that is not recorded yet, in the order they changed first;
	// changed wakes the writer.
	pendingMu sync.Mutex
	pending   map[string]api.PluginInfo
	order     []string
	changed   chan struct{}

	// The plugin list omo provides, as last fetched: the list or the
	// failure and when. catMu is held during a fetch, so one fetch runs at a
	// time and requests meanwhile wait for its answer.
	catMu  sync.Mutex
	catAt  time.Time
	cat    pluginmanager.Catalog
	catErr error

	// The additional plugin lists (General.PluginLists), as last fetched,
	// by address. listMu is held while they are fetched.
	listMu sync.Mutex
	lists  map[string]listRead
}

// listRead is one additional list as last fetched: the list or the failure
// and when.
type listRead struct {
	at   time.Time
	list pluginmanager.List
	err  error
}

// listFetchBound bounds one fetch of an additional list; tests shorten it.
var listFetchBound = pluginmanager.ListTimeout

// registerPlugins registers the /api/plugins routes. Every one is the
// user's alone: a plugin is code that runs as the user, and its settings,
// log and actions are the user's.
//
// A plugin's programs get OMO_SOCKET and no session: on the socket a
// request without a session is the user's, so a plugin can do through
// `omo ai` and the API everything the user can, and its requests cannot be
// told apart from the user's own. That is why only the user may install,
// change, update, remove or trigger plugins.
func (s *Server) registerPlugins(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"GET /api/plugins", s.listPlugins, userOnly},
		{"GET /api/plugins/catalog", s.pluginCatalog, userOnly},
		{"POST /api/plugins", s.installPlugin, userOnly},
		{"PUT /api/plugins/{name}", s.putPlugin, userOnly},
		{"POST /api/plugins/{name}/update", s.updatePlugin, userOnly},
		{"DELETE /api/plugins/{name}", s.deletePlugin, userOnly},
		{"GET /api/plugins/{name}/log", s.pluginLog, userOnly},
		{"POST /api/plugins/{name}/actions/{action}", s.runPluginAction, userOnly},
	})
}

// startPlugins starts the plugin runtime. It runs after the board and the
// settings exist and before the engine starts. It loads the installed
// plugins of the settings; nothing is fetched or installed here: a
// settings file without an installed tree shows as not installed until
// the user installs it (the update route).
func (s *Server) startPlugins(ctx context.Context) {
	f := &s.plugins
	f.o = pluginmanager.Options{Root: s.opts.Layout.Plugins, Git: s.git}
	f.reload = make(chan struct{}, 1)
	f.changed = make(chan struct{}, 1)
	f.busy = map[string]bool{}
	f.pending = map[string]api.PluginInfo{}
	f.m = plugins.New(plugins.Options{
		Root:    s.opts.Layout.Plugins,
		DB:      s.db,
		Socket:  s.socketEndpoint,
		Log:     s.log,
		Changed: s.pluginChanged,
		Agents:  pluginAgents{s},
	})
	s.reloadPlugins(true)
	att, after := s.pluginStartPoint(ctx)
	s.background(f.m.Run)
	s.background(s.writePluginEvents)
	s.background(s.runPluginReloads)
	s.background(func(ctx context.Context) { s.dispatchPluginEvents(ctx, att, after) })
}

// pluginAgents is the plugins' view of the agents the engine runs
// (omo.agents, omo.send_to_agent): each is named by its terminal id, never
// by its session id.
type pluginAgents struct{ s *Server }

// running lists the live sessions whose agent runs with a terminal, with
// TerminalID set.
func (a pluginAgents) running(ctx context.Context) ([]api.AgentSession, error) {
	if a.s.engine == nil {
		return nil, nil
	}
	live, err := a.s.board.AgentSessions(ctx, board.User, true)
	if err != nil {
		return nil, err
	}
	a.s.addTerminalIDs(live)
	out := live[:0]
	for _, sess := range live {
		if sess.TerminalID != "" {
			out = append(out, sess)
		}
	}
	return out, nil
}

// RunningAgents lists the agents the engine runs, the CEO included.
func (a pluginAgents) RunningAgents(ctx context.Context) ([]plugins.RunningAgent, error) {
	live, err := a.running(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]plugins.RunningAgent, 0, len(live))
	for _, sess := range live {
		ra := plugins.RunningAgent{
			Terminal: sess.TerminalID, Name: sess.Name, Role: string(sess.Role), Model: sess.Model,
			Purpose: string(sess.Purpose), Project: sess.Project,
		}
		if sess.Ticket != nil {
			ra.Ticket = *sess.Ticket
		}
		out = append(out, ra)
	}
	return out, nil
}

// SendToAgent hands text to the agent with terminal through the engine's
// notification path (Engine.Notify).
func (a pluginAgents) SendToAgent(ctx context.Context, terminal, text string) error {
	live, err := a.running(ctx)
	if err != nil {
		return err
	}
	for _, sess := range live {
		if terminal != "" && sess.TerminalID == terminal {
			if !a.s.engine.Notify(sess.ID, text) {
				return plugins.ErrNoSuchAgent
			}
			return nil
		}
	}
	return plugins.ErrNoSuchAgent
}

// stopPlugins stops the plugin runtime before the database is closed. The
// runtime, the reloads, the event writer, the dispatch and every install,
// update, change, removal and action run as background work of the server:
// Close has cancelled them and waited for them before it calls
// stopPlugins. Closing the runtime again is then a no-op; it matters only
// for a server whose background never ran.
func (s *Server) stopPlugins() {
	if s.plugins.m != nil {
		s.plugins.m.Close()
	}
}

// pluginSettingsChanged pokes the reload after a change of plugin settings,
// made through omo or by hand on disk. It never blocks and never fetches
// anything: a changed configuration or enabled state reaches the runtime
// with the next load of the installed trees.
func (s *Server) pluginSettingsChanged(c settings.Change) {
	if c.Section != settings.SectionPlugins || s.plugins.reload == nil {
		return
	}
	select {
	case s.plugins.reload <- struct{}{}:
	default:
	}
}

// runPluginReloads reloads the runtime after each poke until ctx ends.
func (s *Server) runPluginReloads(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.plugins.reload:
			s.reloadPlugins(false)
		}
	}
}

// reloadPlugins loads the runtime with the current plugin settings. Without
// force it does nothing when no plugin's settings changed since the last
// load: a load restarts every plugin and cancels its running hooks.
func (s *Server) reloadPlugins(force bool) {
	f := &s.plugins
	f.mu.Lock()
	defer f.mu.Unlock()
	s.loadPluginsLocked(force, "")
}

// loadPluginsLocked loads the plugin settings, leaving out the plugin named
// without ("" for none). The caller holds f.mu.
func (s *Server) loadPluginsLocked(force bool, without string) {
	f := &s.plugins
	var list []api.PluginSettings
	var key []string
	for _, p := range s.settings.Plugins() {
		if p.Name == without {
			continue
		}
		list = append(list, p)
		key = append(key, p.Name+"@"+p.Revision)
	}
	if !force && f.valid && slices.Equal(key, f.loaded) {
		return
	}
	f.m.Load(list)
	f.loaded, f.valid = key, without == ""
}

// claimPlugin marks name as worked on; the returned function releases it
// (the first call does, later calls do nothing).
// A plugin already worked on is refused with 409 conflict.
func (s *Server) claimPlugin(name string) (func(), error) {
	f := &s.plugins
	f.busyMu.Lock()
	defer f.busyMu.Unlock()
	if f.busy[name] {
		return nil, api.Errorf(api.CodeConflict, "plugin %s is being installed, updated, changed or removed; try again when that is done", name)
	}
	f.busy[name] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			f.busyMu.Lock()
			delete(f.busy, name)
			f.busyMu.Unlock()
		})
	}, nil
}

// pluginOp runs op for plugin name as background work of the server,
// bounded by bound, and answers its result with status (a nil result is an
// empty answer). The work goes on to its end when the client leaves, so an
// install is never left half recorded by a closed tab, and is cancelled
// and awaited when the server closes. While it runs, no other operation
// of the same plugin starts.
func (s *Server) pluginOp(w http.ResponseWriter, r *http.Request, name string, status int, bound time.Duration, op func(ctx context.Context) (any, error)) {
	release, err := s.claimPlugin(name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	type result struct {
		v   any
		err error
	}
	done := make(chan result, 1)
	if !s.background(func(ctx context.Context) {
		defer release() // for an op that panics; release is once-only
		ctx, cancel := context.WithTimeout(ctx, bound)
		defer cancel()
		v, err := op(ctx)
		// Released before the answer is handed on: a request sent when the
		// answer arrives must find the plugin free.
		release()
		done <- result{v, err}
	}) {
		release()
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is stopping"))
		return
	}
	select {
	case res := <-done:
		switch {
		case res.err != nil:
			writeError(w, r, res.err)
		case res.v == nil:
			w.WriteHeader(status)
		default:
			writeJSON(w, status, res.v)
		}
	case <-r.Context().Done():
	}
}

// installPluginTree installs or updates p's tree and maps a refusal to an
// API error (see pluginInstallError).
func (s *Server) installPluginTree(ctx context.Context, p api.PluginSettings) (api.PluginSettings, error) {
	out, _, err := pluginmanager.Install(ctx, s.plugins.o, p)
	if err != nil {
		return p, pluginInstallError(ctx, err)
	}
	return out, nil
}

// pluginInstallError turns an install error into an API error: a refused
// field is 422 validation naming the field, an install stopped by its
// bound or by the server's end 503, any other reason 422 with the reason
// (see pluginMessage).
func pluginInstallError(ctx context.Context, err error) error {
	var fe *pluginmanager.FieldError
	switch {
	case errors.As(err, &fe):
		e := api.Errorf(api.CodeValidation, "%s", pluginMessage(fe.Message))
		e.Details = api.FieldDetail{Field: fe.Field}
		return e
	case errors.Is(err, pluginmanager.ErrNameTaken):
		e := api.Errorf(api.CodeValidation, "%s", err.Error())
		e.Details = api.FieldDetail{Field: "name"}
		return e
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return api.Errorf(api.CodeUnavailable, "the install took longer than %s and was stopped", pluginInstallBound)
	case ctx.Err() != nil:
		return api.Errorf(api.CodeUnavailable, "the install was stopped: omo is stopping")
	}
	return api.Errorf(api.CodeValidation, "the plugin could not be installed: %s", pluginMessage(err.Error()))
}

// pluginMessage is text for a message to the user: credentials in addresses
// hidden, control and hidden characters replaced by spaces, one line, cut to
// 500 characters.
func pluginMessage(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || textsafe.IsHidden(r) {
			return ' '
		}
		return r
	}, gitops.HideCredentials(s))
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxPluginMessage {
		s = string(r[:maxPluginMessage-1]) + "…"
	}
	return s
}

// pluginInfo is the info of one plugin of the runtime, with its installed
// commit; a plugin the runtime does not know is not installed.
func (s *Server) pluginInfo(name string) api.PluginInfo {
	for _, info := range s.plugins.m.Infos() {
		if info.Name == name {
			return s.withCommit(info)
		}
	}
	return api.PluginInfo{Name: name, State: plugins.StateNotInstalled, Hooks: []api.PluginHook{}, Buttons: []api.PluginButton{}, ConfigFields: []api.PluginConfigField{}}
}

// withCommit fills the commit of an installed plugin from its install
// record.
func (s *Server) withCommit(info api.PluginInfo) api.PluginInfo {
	if info.Commit != "" {
		return info
	}
	if inst, err := pluginmanager.Installed(s.plugins.o, info.Name); err == nil {
		info.Commit = inst.Commit
	}
	return info
}

// listPlugins answers every plugin of the runtime (its state, hooks and
// manual actions: the ticket panel reads its actions here), the settings
// and the plugin settings files that could not be read.
func (s *Server) listPlugins(w http.ResponseWriter, r *http.Request) {
	infos := []api.PluginInfo{}
	for _, info := range s.plugins.m.Infos() {
		infos = append(infos, s.withCommit(info))
	}
	list := s.settings.Plugins()
	if list == nil {
		list = []api.PluginSettings{}
	}
	errs := []api.FileError{}
	for _, e := range s.settings.Errors() {
		if filepath.Base(filepath.Dir(e.File)) == "plugins" {
			errs = append(errs, e)
		}
	}
	writeJSON(w, http.StatusOK, api.PluginsResponse{Plugins: infos, Settings: list, Errors: errs})
}

// installPlugin installs a new plugin from the name, source, subpath and
// ref of the body, then writes its settings file (create only; enabled,
// configured with the manifest's defaults) and loads it. A failed install
// writes no settings and loads nothing; an install whose settings cannot
// be written is removed again.
func (s *Server) installPlugin(w http.ResponseWriter, r *http.Request) {
	var req api.PluginInstall
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if err := checkAcknowledgement(req.Acknowledge); err != nil {
		writeError(w, r, err)
		return
	}
	name := req.Name
	if !plugins.ValidName(name) {
		e := api.Errorf(api.CodeValidation, "a plugin name is 1 to 64 letters, digits, '.', '_' or '-', starting with a letter or digit")
		e.Details = api.FieldDetail{Field: "name"}
		writeError(w, r, e)
		return
	}
	want := api.PluginSettings{Name: name, Source: req.Source, Subpath: req.Subpath, Ref: req.Ref, Enabled: true, Config: map[string]any{}}
	s.pluginOp(w, r, name, http.StatusCreated, pluginInstallBound, func(ctx context.Context) (any, error) {
		if s.settings.Revision(settings.SectionPlugins, name) != api.NoRevision {
			return nil, api.Errorf(api.CodeConflict, "a plugin named %s exists already", name)
		}
		if err := s.checkPluginSource(ctx, want, req.Acknowledge); err != nil {
			return nil, err
		}
		out, err := s.installPluginTree(ctx, want)
		if err != nil {
			return nil, err
		}
		if err := s.settings.PutPluginIf(api.NoRevision, out); err != nil {
			if rmErr := pluginmanager.Remove(s.plugins.o, name); rmErr != nil {
				s.log.Printf("plugins: remove %s after its settings were refused: %v", name, rmErr)
			}
			return nil, err
		}
		s.waitSettingsEvents(ctx)
		s.reloadPlugins(true)
		return s.pluginInfo(name), nil
	})
}

// afterPluginInstall, when set, runs in a PUT between the reinstall and
// the settings write. Tests set it.
var afterPluginInstall func(name string)

// putPlugin changes a plugin's settings, guarded by If-Match. A changed
// source, subpath or ref installs the plugin again first; when that fails,
// nothing changes, and when the settings cannot be written afterwards, the
// earlier tree is put back.
func (s *Server) putPlugin(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var body api.PluginInstall
	if err := decodeSettings(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := checkAcknowledgement(body.Acknowledge); err != nil {
		writeError(w, r, err)
		return
	}
	req := body.PluginSettings
	name := r.PathValue("name")
	if err := bodyNameMatches(req.Name, name); err != nil {
		writeError(w, r, err)
		return
	}
	req.Name, req.Revision = name, ""
	s.pluginOp(w, r, name, http.StatusOK, pluginInstallBound, func(ctx context.Context) (any, error) {
		current := s.settings.Revision(settings.SectionPlugins, name)
		if current == api.NoRevision {
			return nil, api.Errorf(api.CodeNotFound, "no plugin %s", name)
		}
		if rev != "" && rev != current {
			return nil, api.Errorf(api.CodeStaleSettings, "plugin %s was changed since it was read", name)
		}
		cur, _ := s.settings.Plugin(name)
		next := req
		reinstall := next.Source != cur.Source || next.Subpath != cur.Subpath || next.Ref != cur.Ref
		var prev *pluginmanager.Previous
		if reinstall {
			if err := s.checkPluginSource(ctx, next, body.Acknowledge); err != nil {
				return nil, err
			}
			out, _, p, err := pluginmanager.InstallKeeping(ctx, s.plugins.o, next)
			if err != nil {
				return nil, pluginInstallError(ctx, err)
			}
			next, prev = out, p
			if afterPluginInstall != nil {
				afterPluginInstall(name)
			}
		}
		if err := s.settings.PutPluginIf(rev, next); err != nil {
			// The settings still name the earlier source: so does the tree.
			if reinstall {
				if rerr := prev.Restore(); rerr != nil {
					s.log.Printf("plugins: put back the earlier tree of %s: %v", name, rerr)
				}
				// A reload in between may have read the new tree.
				s.waitSettingsEvents(ctx)
				s.reloadPlugins(true)
			}
			return nil, err
		}
		prev.Discard()
		s.waitSettingsEvents(ctx)
		s.reloadPlugins(reinstall)
		return s.pluginInfo(name), nil
	})
}

// updatePlugin fetches a plugin again from its source and installs it; a
// plugin whose settings exist without an installed tree is installed this
// way. An update from the source, folder and ref the installed tree came
// from needs no acknowledgement (the one given at install covers it); any
// other (a source changed by hand, no installed tree) is checked like an
// install. New keys of the manifest's default configuration are added to
// the settings; the user's values stay.
func (s *Server) updatePlugin(w http.ResponseWriter, r *http.Request) {
	var body api.PluginUpdate
	if err := decodeSettings(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	if err := checkAcknowledgement(body.Acknowledge); err != nil {
		writeError(w, r, err)
		return
	}
	name := r.PathValue("name")
	s.pluginOp(w, r, name, http.StatusOK, pluginInstallBound, func(ctx context.Context) (any, error) {
		cur, ok := s.settings.Plugin(name)
		if !ok {
			return nil, api.Errorf(api.CodeNotFound, "no plugin %s", name)
		}
		if !s.installedFrom(cur) {
			if err := s.checkPluginSource(ctx, cur, body.Acknowledge); err != nil {
				return nil, err
			}
		}
		out, err := s.installPluginTree(ctx, cur)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(out.Config, cur.Config) {
			out.Revision = ""
			if err := s.settings.PutPluginIf(cur.Revision, out); err != nil {
				s.log.Printf("plugins: add the new default configuration of %s: %v", name, err)
			}
			s.waitSettingsEvents(ctx)
		}
		s.reloadPlugins(true)
		return s.pluginInfo(name), nil
	})
}

// installedFrom reports whether p's installed tree came from p's source,
// folder and ref.
func (s *Server) installedFrom(p api.PluginSettings) bool {
	inst, err := pluginmanager.Installed(s.plugins.o, p.Name)
	return err == nil && inst.Source == p.Source && inst.Subpath == p.Subpath && inst.Ref == p.Ref
}

// checkAcknowledgement refuses an acknowledgement the routes do not know.
func checkAcknowledgement(ack string) error {
	if pluginmanager.ValidAcknowledgement(ack) {
		return nil
	}
	e := api.Errorf(api.CodeValidation, "acknowledge must be empty, %q or %q", api.PluginRecommended, api.PluginCommunity)
	e.Details = api.FieldDetail{Field: "acknowledge"}
	return e
}

// checkPluginSource checks p's fields as an install does, decides the tag
// of its source, folder and ref from the plugin list (the cached one,
// fetched when it is old; a source the list does not name, and every source
// while the list cannot be read, counts as community) and refuses an
// install the acknowledgement ack does not cover. The client's view of the
// tag plays no part.
func (s *Server) checkPluginSource(ctx context.Context, p api.PluginSettings, ack string) error {
	if err := pluginmanager.Check(p); err != nil {
		return pluginInstallError(ctx, err)
	}
	tag := api.PluginCommunity
	if cat, _, err := s.readCatalog(ctx, false); err == nil {
		tag = cat.TagOf(p.Source, p.Subpath, p.Ref)
	}
	if pluginmanager.Acknowledged(tag, ack) {
		return nil
	}
	var e *api.Error
	if tag == api.PluginRecommended {
		e = api.Errorf(api.CodeValidation, "omo's maintainers looked at this plugin, but it is someone else's code, and its hooks run commands on this computer as you; install it only after confirming that (acknowledge %q)", api.PluginRecommended)
	} else {
		e = api.Errorf(api.CodeValidation, "omo's maintainers have not reviewed this plugin (it is a community plugin or a source omo's list does not name), and its hooks run commands on this computer as you; install it only after confirming that (acknowledge %q)", api.PluginCommunity)
	}
	e.Details = api.PluginConfirmation{Field: "acknowledge", Tag: tag}
	return e
}

// readCatalog answers the plugin list: the one fetched last while it is
// fresh (a failure while it is recent), else fetched now; refresh always
// fetches. A fetch cut short by ctx is not kept.
func (s *Server) readCatalog(ctx context.Context, refresh bool) (pluginmanager.Catalog, time.Time, error) {
	f := &s.plugins
	f.catMu.Lock()
	defer f.catMu.Unlock()
	if !refresh && !f.catAt.IsZero() {
		age := time.Since(f.catAt)
		if (f.catErr == nil && age < catalogFresh) || (f.catErr != nil && age < catalogRetry) {
			return f.cat, f.catAt, f.catErr
		}
	}
	fctx, cancel := context.WithTimeout(ctx, catalogBound)
	defer cancel()
	cat, err := pluginmanager.FetchCatalog(fctx, f.o, s.opts.PluginCatalog)
	if err != nil && ctx.Err() != nil {
		return pluginmanager.Catalog{}, time.Time{}, err
	}
	if err != nil && fctx.Err() != nil {
		err = fmt.Errorf("fetching the list took longer than %s", catalogBound)
	}
	f.cat, f.catErr, f.catAt = cat, err, time.Now()
	if err != nil {
		s.log.Printf("plugins: read the plugin list: %s", pluginMessage(err.Error()))
	}
	return cat, f.catAt, err
}

// pluginCatalog answers the plugin list omo provides (GET
// /api/plugins/catalog; ?refresh=true fetches it now). A list that cannot be
// fetched or read is an answer too: available false and the reason, so the
// page keeps working with the installed plugins and typed sources.
func (s *Server) pluginCatalog(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") == "true"
	// omo's own list and the additional lists are fetched side by side;
	// neither waits for the other's failure.
	addresses := s.settings.General().PluginLists
	var reads []listRead
	done := make(chan struct{})
	go func() {
		defer close(done)
		reads = s.readLists(r.Context(), addresses, refresh)
	}()
	cat, at, err := s.readCatalog(r.Context(), refresh)
	<-done
	loc := s.opts.PluginCatalog
	out := api.PluginCatalog{Source: gitops.HideCredentials(loc.Source), Ref: loc.Ref, Plugins: []api.CatalogPlugin{},
		Lists: make([]api.PluginListState, len(addresses))}
	if !at.IsZero() {
		out.FetchedAt = utcPtr(at)
	}
	var own []api.CatalogPlugin
	if err != nil {
		out.Message = "omo could not read its plugin list: " + pluginMessage(err.Error())
	} else {
		out.Available = true
		own = cat.Plugins
	}
	lists := make([]pluginmanager.List, 0, len(reads))
	for i, rd := range reads {
		st := api.PluginListState{URL: addresses[i], Origin: pluginmanager.ListOrigin(addresses[i]), Notes: []string{}}
		if !rd.at.IsZero() {
			st.FetchedAt = utcPtr(rd.at)
		}
		if rd.err != nil {
			st.Message = "omo could not read this plugin list: " + pluginMessage(rd.err.Error())
		} else {
			st.Available = true
			for _, n := range rd.list.Notes {
				st.Notes = append(st.Notes, pluginMessage(n))
			}
			lists = append(lists, rd.list)
		}
		out.Lists[i] = st
	}
	plugins, notes := pluginmanager.Combine(own, lists)
	out.Plugins = append(out.Plugins, plugins...)
	k := 0
	for i := range out.Lists {
		if out.Lists[i].Available {
			for _, n := range notes[k] {
				out.Lists[i].Notes = append(out.Lists[i].Notes, pluginMessage(n))
			}
			k++
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func utcPtr(t time.Time) *time.Time {
	t = t.UTC()
	return &t
}

// readLists answers the additional lists at addresses, in their order: each
// as fetched last while it is fresh (a failure while it is recent), else
// fetched now, all of them side by side, each bounded by listFetchBound;
// refresh fetches every one. Lists no longer named are forgotten. A fetch
// cut short by ctx is not kept.
func (s *Server) readLists(ctx context.Context, addresses []string, refresh bool) []listRead {
	f := &s.plugins
	f.listMu.Lock()
	defer f.listMu.Unlock()
	if f.lists == nil {
		f.lists = map[string]listRead{}
	}
	named := map[string]bool{}
	for _, a := range addresses {
		named[a] = true
	}
	for a := range f.lists {
		if !named[a] {
			delete(f.lists, a)
		}
	}
	out := make([]listRead, len(addresses))
	fetched := make([]bool, len(addresses))
	var wg sync.WaitGroup
	for i, a := range addresses {
		if rd, ok := f.lists[a]; ok && !refresh {
			age := time.Since(rd.at)
			if (rd.err == nil && age < catalogFresh) || (rd.err != nil && age < catalogRetry) {
				out[i] = rd
				continue
			}
		}
		fetched[i] = true
		wg.Go(func() {
			fctx, cancel := context.WithTimeout(ctx, listFetchBound)
			defer cancel()
			l, err := pluginmanager.FetchList(fctx, a)
			out[i] = listRead{at: time.Now(), list: l, err: err}
		})
	}
	wg.Wait()
	for i, a := range addresses {
		if !fetched[i] || ctx.Err() != nil {
			continue
		}
		f.lists[a] = out[i]
		if err := out[i].err; err != nil {
			s.log.Printf("plugins: read the plugin list %s: %s", pluginmanager.ListOrigin(a), pluginMessage(err.Error()))
		}
	}
	return out
}

// deletePlugin removes a plugin in this order: the runtime is loaded
// without it (its hooks stop), then its tree, fetch cache and data folder
// go, then its stored values and log, then its settings file. If-Match
// guards the settings file. A tree without settings is removed too.
func (s *Server) deletePlugin(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	name := r.PathValue("name")
	if !plugins.ValidName(name) {
		writeError(w, r, api.Errorf(api.CodeNotFound, "no plugin %s", name))
		return
	}
	s.pluginOp(w, r, name, http.StatusNoContent, pluginInstallBound, func(ctx context.Context) (any, error) {
		current := s.settings.Revision(settings.SectionPlugins, name)
		_, treeErr := os.Lstat(filepath.Join(s.plugins.o.Root, name))
		if current == api.NoRevision && treeErr != nil {
			return nil, api.Errorf(api.CodeNotFound, "no plugin %s", name)
		}
		if rev != "" && rev != current {
			return nil, api.Errorf(api.CodeStaleSettings, "plugin %s was changed since it was read", name)
		}
		f := &s.plugins
		f.mu.Lock()
		defer f.mu.Unlock()
		s.loadPluginsLocked(true, name)
		if err := pluginmanager.Remove(f.o, name); err != nil {
			return nil, api.Errorf(api.CodeInternal, "%s", pluginMessage(err.Error()))
		}
		if err := db.DeletePluginData(s.db, name); err != nil {
			s.log.Printf("plugins: delete the stored values of %s: %v", name, err)
			return nil, api.Errorf(api.CodeInternal, "the plugin's stored values and log cannot be deleted")
		}
		if current != api.NoRevision {
			if err := s.settings.DeletePluginIf(rev, name); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

// pluginLog answers the newest 200 lines of a plugin's log, oldest first.
// The runtime cleaned them when it wrote them (configuration values
// redacted, one line each, cut).
func (s *Server) pluginLog(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.knownPlugin(name) {
		writeError(w, r, api.Errorf(api.CodeNotFound, "no plugin %s", name))
		return
	}
	writeJSON(w, http.StatusOK, s.plugins.m.LogLines(name, pluginLogLines))
}

// knownPlugin reports whether name has settings or is known to the
// runtime.
func (s *Server) knownPlugin(name string) bool {
	if !plugins.ValidName(name) {
		return false
	}
	if s.settings.Revision(settings.SectionPlugins, name) != api.NoRevision {
		return true
	}
	for _, info := range s.plugins.m.Infos() {
		if info.Name == name {
			return true
		}
	}
	return false
}

// runPluginAction runs a manual action of a plugin for the user: a global
// action without ticket, a ticket action with the ticket the user names
// (read as the user; the hook gets the same ticket payload as events
// carry). One action per plugin runs at a time (409); the run is bounded
// by 5 minutes and goes on when the client leaves. The answer is the
// hook's message (cleaned and cut) and validated navigation, if supplied.
func (s *Server) runPluginAction(w http.ResponseWriter, r *http.Request) {
	var req api.PluginRun
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name, action := r.PathValue("name"), r.PathValue("action")
	if req.Ticket < 0 {
		e := api.Errorf(api.CodeValidation, "ticket must be a ticket number")
		e.Details = api.FieldDetail{Field: "ticket"}
		writeError(w, r, e)
		return
	}
	var ticket, project map[string]any
	var ticketProjects []api.TicketProject
	if req.Ticket > 0 {
		t, err := s.board.Get(r.Context(), board.User, int64(req.Ticket))
		if err != nil {
			writeError(w, r, err)
			return
		}
		ticket = plugins.TicketPayload(t.TicketSummary, s.Address())
		ticketProjects = t.Projects
	}
	if req.Project != "" {
		if _, ok := s.settings.Project(req.Project); !ok {
			writeError(w, r, api.Errorf(api.CodeNotFound, "project does not exist"))
			return
		}
		if req.Ticket > 0 && !slices.ContainsFunc(ticketProjects, func(p api.TicketProject) bool { return p.Project == req.Project }) {
			e := api.Errorf(api.CodeValidation, "project must belong to the selected ticket")
			e.Details = api.FieldDetail{Field: "project"}
			writeError(w, r, e)
			return
		}
		project = map[string]any{"id": req.Project}
	}
	type result struct {
		answer api.PluginRunResult
		err    error
	}
	done := make(chan result, 1)
	if !s.background(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, pluginActionBound)
		defer cancel()
		out, err := s.plugins.m.TriggerAction(ctx, name, action, ticket, project)
		// Never let the board's current bearer credential leave in any part of a
		// hook destination, even under an innocuous query key or encoded in a path.
		decoded, _ := url.QueryUnescape(out.URL)
		if err == nil && s.token != "" && (strings.Contains(out.URL, s.token) || strings.Contains(decoded, s.token)) {
			out = api.PluginRunResult{}
			err = errors.New("the action URL contains an omo login token")
		}
		done <- result{out, err}
	}) {
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is stopping"))
		return
	}
	select {
	case res := <-done:
		reply(w, r, http.StatusOK, res.answer, pluginActionError(name, action, res.err))
	case <-r.Context().Done():
	}
}

// pluginActionError maps an error of Trigger to an API error. A failing
// hook is 422 with the hook's error text, which the runtime cleaned
// (configuration values redacted) and cut.
func pluginActionError(name, action string, err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, plugins.ErrUnknownPlugin):
		return api.Errorf(api.CodeNotFound, "no plugin %s is installed and enabled", name)
	case errors.Is(err, plugins.ErrUnknownAction):
		return api.Errorf(api.CodeNotFound, "plugin %s has no action %s", name, action)
	case errors.Is(err, plugins.ErrTicketRequired):
		e := api.Errorf(api.CodeValidation, "action %s of plugin %s needs a ticket", action, name)
		e.Details = api.FieldDetail{Field: "ticket"}
		return e
	case errors.Is(err, plugins.ErrBusy):
		return api.Errorf(api.CodeConflict, "an action of plugin %s is already running", name)
	case errors.Is(err, plugins.ErrClosed):
		return api.Errorf(api.CodeUnavailable, "plugins are stopped")
	}
	return api.Errorf(api.CodeValidation, "the action failed: %s", err.Error())
}

// ---- plugin.changed ----

// pluginChanged is the runtime's Changed callback: it keeps the newest info
// of the plugin and wakes the writer. It never blocks.
func (s *Server) pluginChanged(info api.PluginInfo) {
	f := &s.plugins
	f.pendingMu.Lock()
	if _, ok := f.pending[info.Name]; !ok {
		f.order = append(f.order, info.Name)
	}
	f.pending[info.Name] = info
	f.pendingMu.Unlock()
	select {
	case f.changed <- struct{}{}:
	default:
	}
}

// writePluginEvents records plugin.changed (data: one api.PluginInfo, the
// newest of each plugin that changed; an info equal to the last recorded
// one of its plugin is not recorded again) and wakes the event stream,
// until ctx ends.
func (s *Server) writePluginEvents(ctx context.Context) {
	f := &s.plugins
	last := map[string]string{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.changed:
		}
		f.pendingMu.Lock()
		order, pending := f.order, f.pending
		f.order, f.pending = nil, map[string]api.PluginInfo{}
		f.pendingMu.Unlock()
		wrote := false
		for _, name := range order {
			data, err := json.Marshal(s.withCommit(pending[name]))
			if err != nil || string(data) == last[name] {
				continue
			}
			if err := s.appendSystemEvent(api.KindPluginChanged, json.RawMessage(data)); err != nil {
				s.log.Printf("record %s event: %v", api.KindPluginChanged, err)
				continue
			}
			last[name] = string(data)
			wrote = true
		}
		if wrote {
			s.events.Wake()
		}
	}
}

// ---- dispatch of the board's events ----

// pluginStartPoint is where the dispatch starts: the id of the newest
// event when omo starts and the attention texts the tickets have then
// (they are not news), from one board snapshot. It runs while omo starts,
// before anything else uses the database. When the board cannot be read,
// the dispatch starts after the newest event without attention texts; when
// not even that can be read, after is -1 and the dispatch reads the newest
// id itself.
func (s *Server) pluginStartPoint(ctx context.Context) (att map[int]string, after int64) {
	att = map[int]string{}
	list, err := s.board.List(ctx, board.User, api.TicketFilter{})
	if err == nil {
		for _, t := range list.Tickets {
			if t.Attention != "" && t.Column != api.ColumnDone {
				att[t.Number] = t.Attention
			}
		}
		return att, list.EventID
	}
	s.log.Printf("plugins: read the board: %v", err)
	if after, err = db.LastEventID(s.db); err == nil {
		return att, after
	}
	return att, -1
}

// dispatchPluginEvents hands the board's events after the start point to
// the plugins until ctx ends. It follows the event log by id, as the event
// stream does: events that happened while omo was stopped are not handed
// to plugins, and every streamed event is mapped exactly once (the log is
// the source of truth, ids grow with every commit, so reading in id order
// after the last id handled never skips an event). It reads the log only
// after the event stream's broker fanned out something newer, so an idle
// board costs no database reads. It never slows the board down: it reads
// the committed log on its own goroutine, nothing in the board's write
// path waits for it, and Dispatch never blocks.
func (s *Server) dispatchPluginEvents(ctx context.Context, att map[int]string, after int64) {
	ticket := func(n int) map[string]any {
		t, err := s.board.Get(ctx, board.User, int64(n))
		if err != nil {
			return nil
		}
		return plugins.TicketPayload(t.TicketSummary, s.Address())
	}
	tick := time.NewTicker(pluginFollowInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if after < 0 {
			last, err := db.LastEventID(s.db)
			if err != nil {
				continue
			}
			after = last
		}
		// The broker has fanned out every event it read; nothing newer
		// than what was handled here means nothing to read.
		if s.events.sent() <= after {
			continue
		}
		for ctx.Err() == nil {
			evs, err := db.EventsSince(s.db, after, pluginFollowBatch, true)
			if err != nil {
				s.log.Printf("plugins: read the event log: %v", err)
				break
			}
			for _, e := range evs {
				after = e.ID
				for _, pe := range pluginEvents(toAPIEvent(e), att, ticket, s.Address()) {
					s.plugins.m.Dispatch(pe)
				}
			}
			if len(evs) < pluginFollowBatch {
				break
			}
		}
	}
}

// pluginEvents maps one event of the board to the plugin events it stands
// for (none for most kinds):
//
//   - ticket.created → ticket_created
//   - ticket.moved that changes the column → ticket_moved
//   - comment.created → comment_written, then question_asked for a question
//   - agent.created → agent_started; agent.ended → agent_exited
//   - server.paused → omo_paused when the pause was switched on (by the
//     switch or by stopping all agents), omo_resumed when it was switched
//     off; the board records it only when the state changes
//   - ticket.created, ticket.updated or ticket.moved whose ticket has a
//     non-empty attention text other than the last one seen for it →
//     ticket_needs_attention, after the event's own (att keeps the texts
//     seen; an empty text or a Done ticket forgets it)
//
// Payloads come from the plugins package's builders: no session id,
// terminal id, folder or environment value; text written by people or
// agents is only ever a value in them. ticket reads a ticket's payload by
// number (nil when it cannot be read).
func pluginEvents(ev api.Event, att map[int]string, ticket func(int) map[string]any, address string) []plugins.Event {
	at, err := time.Parse(time.RFC3339Nano, ev.At)
	if err != nil {
		at = time.Now()
	}
	var out []plugins.Event
	add := func(name string, data map[string]any) {
		out = append(out, plugins.Event{Name: name, At: at, Data: data})
	}
	attention := func(t api.TicketSummary, payload map[string]any) {
		n := t.Number
		if t.Attention == "" || t.Column == api.ColumnDone {
			delete(att, n)
			return
		}
		if att[n] == t.Attention {
			return
		}
		att[n] = t.Attention
		add(plugins.EventNeedsAttention, map[string]any{"ticket": payload, "reason": payload["attention"]})
	}
	ticketOf := func(n *int) any {
		if n == nil {
			return nil
		}
		if p := ticket(*n); p != nil {
			return p
		}
		return nil
	}
	switch ev.Kind {
	case api.KindTicketCreated:
		var d api.TicketCreatedData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		p := plugins.TicketPayload(d.Ticket, address)
		add(plugins.EventTicketCreated, map[string]any{"ticket": p})
		attention(d.Ticket, p)
	case api.KindTicketMoved:
		var d api.TicketMovedData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		p := plugins.TicketPayload(d.Ticket, address)
		if d.From != d.To {
			add(plugins.EventTicketMoved, map[string]any{"ticket": p, "from": string(d.From), "to": string(d.To), "action": d.Action})
		}
		attention(d.Ticket, p)
	case api.KindTicketUpdated:
		var d api.TicketUpdatedData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		attention(d.Ticket, plugins.TicketPayload(d.Ticket, address))
	case api.KindCommentCreated:
		var d api.CommentCreatedData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		n := d.Comment.Ticket
		t := ticketOf(&n)
		c := plugins.CommentPayload(d.Comment)
		add(plugins.EventCommentWritten, map[string]any{"ticket": t, "comment": c})
		if d.Comment.QuestionTo != "" {
			add(plugins.EventQuestionAsked, map[string]any{"ticket": t, "comment": c})
		}
	case api.KindAgentCreated:
		var d api.AgentData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		add(plugins.EventAgentStarted, map[string]any{"ticket": ticketOf(d.Agent.Ticket), "agent": plugins.AgentPayload(d.Agent)})
	case api.KindAgentEnded:
		var d api.AgentData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		add(plugins.EventAgentExited, map[string]any{
			"ticket": ticketOf(d.Agent.Ticket), "agent": plugins.AgentPayload(d.Agent),
			"reason": plugins.ExitReason(d.Agent.EndReason),
		})
	case api.KindServerPaused:
		var d api.ServerPausedData
		if json.Unmarshal(ev.Data, &d) != nil {
			return nil
		}
		if d.Paused {
			add(plugins.EventPaused, map[string]any{})
		} else {
			add(plugins.EventResumed, map[string]any{})
		}
	}
	return out
}
