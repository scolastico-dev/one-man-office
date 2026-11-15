package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// settingsFeed carries the settings store's changes from its callback to one
// worker goroutine that records them in the event log, in order.
type settingsFeed struct {
	mu       sync.Mutex
	queue    []settings.Change
	draining bool
	idle     chan struct{} // closed when the queue is drained; replaced on the next change
}

// registerSettings adds the settings, project, profile, model, role and
// prompt routes.
func (s *Server) registerSettings(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/projects", s.listProjects)
	mux.HandleFunc("POST /api/projects", s.createProject)
	mux.HandleFunc("PATCH /api/projects/{id}", s.patchProject)
	mux.HandleFunc("DELETE /api/projects/{id}", s.deleteProject)
	mux.HandleFunc("DELETE /api/projects", s.deleteMissingProjects)

	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)

	mux.HandleFunc("GET /api/profiles", s.listProfiles)
	mux.HandleFunc("PUT /api/profiles/{name}", s.putProfile)
	mux.HandleFunc("DELETE /api/profiles/{name}", s.deleteProfile)

	mux.HandleFunc("GET /api/models", s.listModels)

	mux.HandleFunc("GET /api/roles", s.listRoles)
	mux.HandleFunc("PUT /api/roles/{role}", s.putRole)

	mux.HandleFunc("GET /api/prompts", s.listPrompts)
	mux.HandleFunc("GET /api/prompts/{name}", s.getPrompt)
	mux.HandleFunc("PUT /api/prompts/{name}", s.putPrompt)
	mux.HandleFunc("DELETE /api/prompts/{name}", s.deletePrompt)
}

// settingsChanged receives every change of the settings store, from its own
// writes and from hand edits. The store calls it with its operation lock
// held: it only queues the change for the worker that writes the events.
func (s *Server) settingsChanged(c settings.Change) {
	f := &s.feed
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.idle == nil || len(f.queue) == 0 && !f.draining {
		f.idle = make(chan struct{})
	}
	f.queue = append(f.queue, c)
	if !f.draining {
		f.draining = true
		if !s.background(s.drainSettings) {
			// Shutting down: the change stays in the files; nothing records it.
			f.draining = false
			f.queue = nil
			close(f.idle)
		}
	}
}

// drainSettings records the queued changes until the queue is empty.
func (s *Server) drainSettings(ctx context.Context) {
	f := &s.feed
	// Recording finishes even when shutdown has begun: Close waits for this
	// worker before it closes the database.
	ctx = context.WithoutCancel(ctx)
	for {
		f.mu.Lock()
		if len(f.queue) == 0 {
			f.draining = false
			close(f.idle)
			f.mu.Unlock()
			return
		}
		c := f.queue[0]
		f.queue = f.queue[1:]
		f.mu.Unlock()
		s.recordSettingsChange(ctx, c)
	}
}

// waitSettingsEvents blocks until the changes queued so far are in the event
// log, so that an answer to a settings write is sent after its event exists.
func (s *Server) waitSettingsEvents(ctx context.Context) {
	f := &s.feed
	f.mu.Lock()
	idle := f.idle
	pending := f.draining || len(f.queue) > 0
	f.mu.Unlock()
	if !pending || idle == nil {
		return
	}
	select {
	case <-idle:
	case <-ctx.Done():
	}
}

// recordSettingsChange appends the event of one change (actor system),
// wakes the stream and, after a general change that left the CEO off, hands
// what the CEO supervised to the user.
func (s *Server) recordSettingsChange(ctx context.Context, c settings.Change) {
	kind, data := s.settingsEvent(c)
	if kind != "" {
		if err := s.appendSystemEvent(kind, data); err != nil {
			s.log.Printf("record %s event: %v", kind, err)
		}
		s.events.Wake()
	}
	generalChanged := c.Section == settings.SectionGeneral && c.Err == nil && !c.Removed
	if (generalChanged || s.ceoReassignFailed.Load()) && c.Err == nil {
		s.reassignCEOIfOff(ctx)
	}
}

// reassignCEOIfOff hands what the CEO supervised to the user when the CEO is
// off. It is idempotent: it does nothing when the CEO supervises nothing. A
// failure is logged, shown as an invalid-settings event on settings.yaml and
// retried on the next settings change.
func (s *Server) reassignCEOIfOff(ctx context.Context) {
	if s.settings.General().CEO.Enabled {
		s.ceoReassignFailed.Store(false)
		return
	}
	err := s.board.ReassignCEO(ctx)
	if err == nil {
		s.ceoReassignFailed.Store(false)
		return
	}
	s.log.Printf("reassign CEO work: %v", err)
	s.ceoReassignFailed.Store(true)
	s.emitSettingsInvalid(filepath.Join(s.opts.Layout.Root, "settings.yaml"), "the CEO is off but its work could not be handed to the user: "+err.Error())
}

// emitSettingsInvalid records and announces an invalid-settings event.
func (s *Server) emitSettingsInvalid(file, message string) {
	if err := s.appendSystemEvent(api.KindSettingsInvalid, api.SettingsInvalidData{File: file, Message: message}); err != nil {
		s.log.Printf("record %s event: %v", api.KindSettingsInvalid, err)
		return
	}
	s.events.Wake()
}

// reportStartupSettings runs once per start, when the board and the event
// log exist: it hands the CEO's work to the user when the CEO is off, as a
// hand edit may have switched it off while the server was down.
func (s *Server) reportStartupSettings() {
	s.reassignCEOIfOff(s.ctx)
}

// settingsEvent maps a change to its event kind and data; kind is empty when
// there is nothing to report.
func (s *Server) settingsEvent(c settings.Change) (string, any) {
	switch {
	case c.Err != nil:
		return api.KindSettingsInvalid, api.SettingsInvalidData{File: c.Err.File, Message: c.Err.Message}
	case c.Section == settings.SectionProjects && c.Removed:
		return api.KindProjectRemoved, api.ProjectRemovedData{ID: c.Name}
	case c.Section == settings.SectionProjects:
		p, ok := s.settings.Project(c.Name)
		if !ok {
			// Gone again since; its removal has its own change.
			return "", nil
		}
		return api.KindProjectChanged, api.ProjectChangedData{Project: p}
	default:
		return api.KindSettingsChanged, api.SettingsChangedData{Section: c.Section, Name: c.Name}
	}
}

func (s *Server) appendSystemEvent(kind string, data any) error {
	actor, err := json.Marshal(board.System.API())
	if err != nil {
		return err
	}
	detail, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = db.AppendEvent(s.db, kind, string(actor), 0, string(detail))
	return err
}

// ---- projects ----

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var ps []settings.Project
	if all, _ := strconv.ParseBool(r.URL.Query().Get("all")); all {
		for _, p := range s.settings.Projects(true) {
			if settings.MatchProject(p, q) {
				ps = append(ps, p)
			}
		}
	} else {
		ps = s.settings.SearchProjects(q)
	}
	if ps == nil {
		ps = []settings.Project{}
	}
	writeJSON(w, http.StatusOK, ps)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	var req api.CreateProject
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.settings.AddProject(req.Path, req.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	var req api.UpdateProject
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	var u settings.ProjectUpdate
	for _, f := range []struct {
		name string
		set  bool
		null bool
		fn   func()
	}{
		{"name", req.Name.Set, req.Name.Null, func() { u.Name = &req.Name.Value }},
		{"enabled", req.Enabled.Set, req.Enabled.Null, func() { u.Enabled = &req.Enabled.Value }},
		{"story_merge", req.StoryMerge.Set, req.StoryMerge.Null, func() { u.StoryMerge = &req.StoryMerge.Value }},
	} {
		if !f.set {
			continue
		}
		if f.null {
			writeError(w, r, api.Errorf(api.CodeBadRequest, "%s cannot be null", f.name))
			return
		}
		f.fn()
	}
	p, err := s.settings.UpdateProject(r.PathValue("id"), u)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	if err := s.settings.RemoveProject(r.PathValue("id")); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteMissingProjects(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	if r.URL.Query().Get("missing") != "true" {
		writeError(w, r, api.Errorf(api.CodeBadRequest, "DELETE /api/projects needs missing=true"))
		return
	}
	removed, err := s.settings.RemoveMissingProjects()
	if err != nil {
		writeError(w, r, err)
		return
	}
	if removed == nil {
		removed = []string{}
	}
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, api.RemovedProjects{Removed: removed})
}

// ---- general settings ----

func (s *Server) settingsResponse() api.SettingsResponse {
	return api.SettingsResponse{Settings: api.General(s.settings.General()), Errors: s.settings.Errors()}
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.settingsResponse())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	var g api.General
	if err := decode(w, r, &g); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.settings.SetGeneral(settings.General(g)); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, s.settingsResponse())
}

// ---- profiles and models ----

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	errs := []api.FileError{}
	for _, e := range s.settings.Errors() {
		if filepath.Base(filepath.Dir(e.File)) == "profiles" {
			errs = append(errs, e)
		}
	}
	writeJSON(w, http.StatusOK, api.ProfilesResponse{Profiles: s.settings.Profiles(), Errors: errs})
}

func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	var p api.Profile
	if err := decode(w, r, &p); err != nil {
		writeError(w, r, err)
		return
	}
	p.Name = r.PathValue("name")
	if err := s.settings.PutProfile(p); err != nil {
		writeError(w, r, err)
		return
	}
	stored, _ := s.settings.Profile(p.Name)
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, stored)
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	if err := s.settings.DeleteProfile(r.PathValue("name")); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	user := actorFrom(r).Role == api.RoleUser
	out := []api.Model{}
	for _, m := range s.settings.Models() {
		if user || m.Selectable {
			out = append(out, m)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- roles ----

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.settings.Roles())
}

func (s *Server) putRole(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	var role api.RoleSettings
	if err := decode(w, r, &role); err != nil {
		writeError(w, r, err)
		return
	}
	role.Name = r.PathValue("role")
	if err := s.settings.PutRole(role); err != nil {
		writeError(w, r, err)
		return
	}
	stored, _ := s.settings.Role(role.Name)
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, stored)
}

// ---- prompts ----

func (s *Server) listPrompts(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.settings.Prompts())
}

func (s *Server) promptResponse(name string) (api.Prompt, error) {
	text, customized, ok := s.settings.Prompt(name)
	if !ok {
		return api.Prompt{}, api.Errorf(api.CodeNotFound, "no prompt %s", name)
	}
	return api.Prompt{Name: name, Text: text, Customized: customized}, nil
}

func (s *Server) getPrompt(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	p, err := s.promptResponse(r.PathValue("name"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) putPrompt(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	var req api.PromptUpdate
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := r.PathValue("name")
	if err := s.settings.PutPrompt(name, req.Text); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.promptResponse(name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deletePrompt(w http.ResponseWriter, r *http.Request) {
	if !requireUser(w, r) {
		return
	}
	if err := s.settings.ResetPrompt(r.PathValue("name")); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	w.WriteHeader(http.StatusNoContent)
}
