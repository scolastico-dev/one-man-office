package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/branchname"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
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
// prompt routes. Only the project list and the model list may be read by
// agent sessions; everything else holds or changes code that runs as the user
// (commands, arguments, environments, prompts) and is the user's alone.
func (s *Server) registerSettings(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"GET /api/projects", s.listProjects, agentRead},
		{"POST /api/projects", s.createProject, userOnly},
		{"PATCH /api/projects/{id}", s.patchProject, userOnly},
		{"DELETE /api/projects/{id}", s.deleteProject, userOnly},
		{"DELETE /api/projects", s.deleteMissingProjects, userOnly},
		{"POST /api/projects/batch", s.batchProjects, userOnly},

		{"GET /api/settings", s.getSettings, userOnly},
		{"PUT /api/settings", s.putSettings, userOnly},
		{"PATCH /api/settings", s.patchSettings, userOnly},
		{"POST /api/settings/branch-preview", s.postBranchPreview, userOnly},

		{"GET /api/profiles", s.listProfiles, userOnly},
		{"PUT /api/profiles/{name}", s.putProfile, userOnly},
		{"DELETE /api/profiles/{name}", s.deleteProfile, userOnly},

		{"GET /api/models", s.listModels, agentRead},

		{"GET /api/roles", s.listRoles, userOnly},
		{"PUT /api/roles/{role}", s.putRole, userOnly},

		{"GET /api/prompts", s.listPrompts, userOnly},
		{"GET /api/prompts/{name}", s.getPrompt, userOnly},
		{"PUT /api/prompts/{name}", s.putPrompt, userOnly},
		{"DELETE /api/prompts/{name}", s.deletePrompt, userOnly},
	})
}

// postBranchPreview answers the branch name a draft of branches.template
// gives for an example ticket (#42 "Fix the login", a task of the first
// project), by the rules the engine names branches with, or the reason the
// template cannot be used. Nothing is saved.
func (s *Server) postBranchPreview(w http.ResponseWriter, r *http.Request) {
	var req api.BranchPreviewRequest
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	project := "app"
	if ps := s.settings.Projects(false); len(ps) > 0 {
		project = ps[0].ID
	}
	name, err := branchname.Expand(req.Template, branchname.Example(project))
	if err != nil {
		writeJSON(w, http.StatusOK, api.BranchPreview{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, api.BranchPreview{Branch: name})
}

// settingsChanged receives every change of the settings store, from its own
// writes and from hand edits. The store calls it with its operation lock
// held: it only wakes the engine and queues the change for the worker that
// writes the events and hands the change to the engine.
func (s *Server) settingsChanged(c settings.Change) {
	if s.engine != nil {
		s.engine.Wake()
	}
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
		s.scan.settingsChanged(c)
		s.usageSettingsChanged(c)
		s.pluginSettingsChanged(c)
		s.skillSetSettingsChanged(c)
		if s.engine != nil && c.Err == nil {
			// Outside the store's lock: this may run the agent
			// environment's commands for omo's own git client.
			s.engine.SettingsChanged()
		}
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

// recordSettingsChange logs a file it ignores, appends the event of one
// change (actor system), wakes the stream and, after a general change that left the CEO off, hands
// what the CEO supervised to the user.
func (s *Server) recordSettingsChange(ctx context.Context, c settings.Change) {
	if c.Err != nil {
		// As at the start: a file omo ignores is named in the log, where
		// whoever edited it by hand can look.
		s.log.Printf("settings file %s ignored: %s", c.Err.File, c.Err.Message)
	}
	kind, data := s.settingsEvent(s.ctx, c)
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
// log exist: it emits one invalid-settings event per file that is already
// broken, and hands the CEO's work to the user when the CEO is off, as a
// hand edit may have switched it off while the server was down.
func (s *Server) reportStartupSettings() {
	for _, e := range s.settings.Errors() {
		s.emitSettingsInvalid(e.File, e.Message)
	}
	s.reassignCEOIfOff(s.ctx)
}

// settingsEvent maps a change to its event kind and data; kind is empty when
// there is nothing to report.
func (s *Server) settingsEvent(ctx context.Context, c settings.Change) (string, any) {
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
		return api.KindProjectChanged, api.ProjectChangedData{Project: s.withBranches(ctx, []settings.Project{p})[0]}
	default:
		source := api.SourceApp
		if c.FromDisk {
			source = api.SourceDisk
		}
		return api.KindSettingsChanged, api.SettingsChangedData{Section: c.Section, Name: c.Name, Source: source}
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

// ---- request bodies and If-Match ----

// revisionPattern is the form of a revision in If-Match: the 16 hex digits
// the store hands out, or api.NoRevision.
var revisionPattern = regexp.MustCompile(`^"[0-9a-f]{1,64}"$`)

// ifMatch reads the optional If-Match header of a settings write: one quoted
// revision ("0123abcd"). Without the header it returns "" and the write is
// not checked. A weak tag (W/), a list, "*" or anything else is refused with
// bad_request; the header's value is never quoted back.
func ifMatch(r *http.Request) (string, error) {
	values := r.Header.Values("If-Match")
	if len(values) == 0 {
		return "", nil
	}
	v := strings.TrimSpace(values[0])
	if len(values) > 1 || !revisionPattern.MatchString(v) {
		return "", api.Errorf(api.CodeBadRequest, `If-Match must hold one quoted revision such as "%s", as the settings answers give it`, api.NoRevision)
	}
	return strings.Trim(v, `"`), nil
}

// readSettingsBody reads the body of a settings write: application/json, at
// most maxBody bytes.
func readSettingsBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if err := checkJSONContentType(r); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, api.Errorf(api.CodeTooLarge, "the request body exceeds %d bytes", maxBody)
		}
		return nil, api.Errorf(api.CodeBadRequest, "the request body could not be read")
	}
	return data, nil
}

// decodeSettings reads the body of a settings write into v as decode does
// (application/json, at most maxBody bytes, one value, no unknown fields at
// any depth), with refusals that never quote a value of the body.
func decodeSettings(w http.ResponseWriter, r *http.Request, v any) error {
	data, err := readSettingsBody(w, r)
	if err != nil {
		return err
	}
	return unmarshalStrict(data, v, "")
}

// unmarshalStrict decodes one JSON value from data into v, refusing unknown
// fields and anything after the value. field is the dotted key data stands
// for ("" for a whole body); refusals name it.
func unmarshalStrict(data []byte, v any, field string) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return settingsDecodeError(err, field, func() string {
			return failingKey(data, reflect.TypeOf(v).Elem(), field)
		})
	}
	if _, err := dec.Token(); err != io.EOF {
		return api.Errorf(api.CodeBadRequest, "the request body must hold a single JSON value")
	}
	return nil
}

// maxShownName is the most of an unknown field's name a refusal shows.
const maxShownName = 64

// settingsDecodeError words a JSON decoding failure of a settings body
// without quoting any value of it: the parsers of some types (durations)
// quote what they could not parse. Field names may be shown; values never
// are. locate finds the dotted key of a value that could not be decoded when
// the decoder does not name it (a type with its own decoding, such as a
// duration, reports no path).
func settingsDecodeError(err error, field string, locate func() string) *api.Error {
	join := func(name string) string {
		switch {
		case field == "":
			return name
		case name == "":
			return field
		}
		return field + "." + name
	}
	withField := func(e *api.Error, name string) *api.Error {
		if name != "" {
			e.Details = api.FieldDetail{Field: name}
		}
		return e
	}
	var syntax *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.EOF) && field == "":
		return api.Errorf(api.CodeBadRequest, "the request body is empty")
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return withField(api.Errorf(api.CodeBadRequest, "invalid request body: the JSON ends too early"), field)
	case errors.As(err, &syntax):
		return withField(api.Errorf(api.CodeBadRequest, "invalid request body: not valid JSON at byte %d", syntax.Offset), field)
	case errors.As(err, &typ):
		// Value is the JSON kind, followed by the number itself for a
		// number out of range: only the kind is shown.
		kind, _, _ := strings.Cut(typ.Value, " ")
		// The decoder's own path leaves out list indexes and map keys in
		// some Go versions, so the path found in the body comes first.
		name := locate()
		if name == "" {
			name = join(typ.Field)
		}
		if name == "" {
			return api.Errorf(api.CodeBadRequest, "invalid request body: the body must be a JSON object, not a JSON %s", kind)
		}
		return withField(api.Errorf(api.CodeBadRequest, "invalid request body: %s cannot be a JSON %s", name, kind), name)
	}
	if unknown, ok := strings.CutPrefix(err.Error(), `json: unknown field "`); ok {
		unknown = strings.TrimSuffix(unknown, `"`)
		if r := []rune(unknown); len(r) > maxShownName {
			unknown = string(r[:maxShownName]) + "…"
		}
		return withField(api.Errorf(api.CodeBadRequest, "invalid request body: unknown field %s", strconv.QuoteToASCII(join(unknown))), field)
	}
	at := locate()
	what := "a value"
	if at != "" {
		what = "a value of " + at
	}
	hint := ""
	if strings.HasPrefix(err.Error(), "time: ") {
		hint = "; write durations as Go duration text such as 15m or 1h30m"
	}
	return withField(api.Errorf(api.CodeBadRequest, "invalid request body: %s is not valid%s", what, hint), at)
}

var jsonUnmarshaler = reflect.TypeFor[json.Unmarshaler]()

// failingKey returns the dotted key of the deepest value in data that cannot
// be decoded into its place in t, at standing for data itself: it descends
// into objects (struct fields by their JSON names, map entries) and lists
// while the part decodes with an error on its own. A type with its own
// decoding is not descended into. Keys come from the type and the body's own
// object keys and list indexes; values are never part of them.
func failingKey(data []byte, t reflect.Type, at string) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if reflect.PointerTo(t).Implements(jsonUnmarshaler) {
		return at
	}
	join := func(key string) string {
		if at == "" {
			return key
		}
		return at + "." + key
	}
	switch t.Kind() {
	case reflect.Struct:
		var obj map[string]json.RawMessage
		if json.Unmarshal(data, &obj) != nil {
			return at
		}
		for _, k := range sortedNames(obj) {
			if f, ok := jsonField(t, k); ok && failsAlone(obj[k], f.Type) {
				return failingKey(obj[k], f.Type, join(k))
			}
		}
	case reflect.Map:
		var obj map[string]json.RawMessage
		if json.Unmarshal(data, &obj) != nil {
			return at
		}
		for _, k := range sortedNames(obj) {
			if failsAlone(obj[k], t.Elem()) {
				return failingKey(obj[k], t.Elem(), join(k))
			}
		}
	case reflect.Slice, reflect.Array:
		var list []json.RawMessage
		if json.Unmarshal(data, &list) != nil {
			return at
		}
		for i, e := range list {
			if failsAlone(e, t.Elem()) {
				return failingKey(e, t.Elem(), join(strconv.Itoa(i)))
			}
		}
	}
	return at
}

// failsAlone reports whether data cannot be decoded strictly into a new t.
func failsAlone(data []byte, t reflect.Type) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(reflect.New(t).Interface()) != nil
}

// jsonField finds the field of struct t that the JSON key decodes into, as
// encoding/json matches it: by its JSON name, else ignoring case.
func jsonField(t reflect.Type, key string) (reflect.StructField, bool) {
	var folded reflect.StructField
	found := false
	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if name == key {
			return f, true
		}
		if !found && strings.EqualFold(name, key) {
			folded, found = f, true
		}
	}
	return folded, found
}

// ---- in use ----

// maxListedTickets is the most ticket numbers an in_use refusal lists.
const maxListedTickets = 10

// ticketList words ticket numbers for a refusal: "#12, #15", at most
// maxListedTickets of them, then "and N more".
func ticketList(nums []int64) string {
	shown := nums
	if len(shown) > maxListedTickets {
		shown = shown[:maxListedTickets]
	}
	parts := make([]string, len(shown))
	for i, n := range shown {
		parts[i] = "#" + strconv.FormatInt(n, 10)
	}
	out := strings.Join(parts, ", ")
	if more := len(nums) - len(shown); more > 0 {
		out += fmt.Sprintf(" and %d more", more)
	}
	return out
}

// The in-use checks below ask the board which open tickets name a project
// or a model before the settings store removes it. Each runs with its
// removal under s.refs (see refsLock), which the board holds while it checks
// and writes a ticket's projects and model: the check and the removal are
// one step as far as ticket writes go.

// projectInUse refuses with in_use when open tickets use project id.
func (s *Server) projectInUse(ctx context.Context, id string) error {
	nums, err := s.board.OpenTicketsWithProject(ctx, id)
	if err != nil {
		return err
	}
	if len(nums) > 0 {
		return api.Errorf(api.CodeInUse, "Open tickets use this project: %s. %s", ticketList(nums), takeOffHint(id))
	}
	return nil
}

// takeOffHint tells how a project leaves the open tickets that use it.
func takeOffHint(id string) string {
	return fmt.Sprintf("Take it off each of them first (Remove from ticket under Branches, or `omo ai ticket remove-project <N> %s`), or cancel them; done tickets keep it as their history.", id)
}

// modelsInUse refuses with in_use when open tickets set one of the model ids
// as their model, naming each model with its tickets.
func (s *Server) modelsInUse(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	used, err := s.board.OpenTicketsWithModels(ctx, ids)
	if err != nil {
		return err
	}
	var parts []string
	for _, id := range slices.Sorted(maps.Keys(used)) {
		parts = append(parts, fmt.Sprintf("the model %s: %s", id, ticketList(used[id])))
	}
	if len(parts) > 0 {
		return api.Errorf(api.CodeInUse, "Open tickets use %s.", strings.Join(parts, "; "))
	}
	return nil
}

// modelIDs returns the model ids ("<profile>/<model>") of the models of p,
// or of only those missing from keep when keep is not nil.
func modelIDs(p api.Profile, keep map[string]api.ProfileModel) []string {
	var ids []string
	for _, name := range sortedNames(p.Models) {
		if keep != nil {
			if _, kept := keep[name]; kept {
				continue
			}
		}
		ids = append(ids, p.Name+"/"+name)
	}
	return ids
}

// ---- projects ----

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var ps []settings.Project
	all := false
	if raw := r.URL.Query().Get("all"); raw != "" {
		var err error
		if all, err = strconv.ParseBool(raw); err != nil {
			writeError(w, r, api.Errorf(api.CodeBadRequest, "all must be a boolean, got %q", raw))
			return
		}
	}
	if all {
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
	writeJSON(w, http.StatusOK, s.withBranches(r.Context(), ps))
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	var req api.CreateProject
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	p, err := s.settings.AddProject(req.Path, req.Name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusCreated, s.withBranches(r.Context(), []settings.Project{p})[0])
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) {
	var req api.UpdateProject
	if err := decodeSettings(w, r, &req); err != nil {
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
		{"merge", req.Merge.Set, req.Merge.Null, func() {}},
		{"merge.story", req.Merge.Value.Story.Set, req.Merge.Value.Story.Null, func() { u.MergeStory = &req.Merge.Value.Story.Value }},
		{"merge.task", req.Merge.Value.Task.Set, req.Merge.Value.Task.Null, func() { u.MergeTask = &req.Merge.Value.Task.Value }},
		{"merge.story_task", req.Merge.Value.StoryTask.Set, req.Merge.Value.StoryTask.Null, func() { u.MergeStoryTask = &req.Merge.Value.StoryTask.Value }},
		// The older key for merge.story.
		{"story_merge", req.StoryMerge.Set, req.StoryMerge.Null, func() { u.MergeStory = &req.StoryMerge.Value }},
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
	if req.StoryMerge.Set && req.Merge.Value.Story.Set {
		writeError(w, r, api.Errorf(api.CodeBadRequest, "story_merge and merge.story cannot both be set; use merge.story"))
		return
	}
	p, err := s.settings.UpdateProject(r.PathValue("id"), u)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, s.withBranches(r.Context(), []settings.Project{p})[0])
}

// removeProject removes project id unless open tickets use it (in_use) or
// the store refuses it (a workspace root would find it again).
func (s *Server) removeProject(ctx context.Context, id string) error {
	return s.removal(func() error {
		if _, ok := s.settings.Project(id); ok {
			return s.projectInUse(ctx, id)
		}
		return nil
	}, func() error { return s.settings.RemoveProject(id) })
}

func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	err := s.removeProject(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// maxProjectBatch is the most projects one batch may name.
const maxProjectBatch = 1000

// batchProjects removes, switches off or switches on several projects. Each
// is handled on its own, as DELETE /api/projects/{id} or PATCH
// /api/projects/{id} with enabled would handle it, so one refusal stops
// nothing: the answer says per id whether it was done or why not.
func (s *Server) batchProjects(w http.ResponseWriter, r *http.Request) {
	var req api.ProjectBatch
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	var do func(id string) error
	switch req.Action {
	case api.ProjectBatchRemove:
		do = func(id string) error { return s.removeProject(r.Context(), id) }
	case api.ProjectBatchDisable, api.ProjectBatchEnable:
		on := req.Action == api.ProjectBatchEnable
		do = func(id string) error {
			_, err := s.settings.UpdateProject(id, settings.ProjectUpdate{Enabled: &on})
			return err
		}
	default:
		e := api.Errorf(api.CodeValidation, "action must be remove, disable or enable")
		e.Details = api.FieldDetail{Field: "action"}
		writeError(w, r, e)
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > maxProjectBatch {
		e := api.Errorf(api.CodeValidation, "ids must name between 1 and %d projects", maxProjectBatch)
		e.Details = api.FieldDetail{Field: "ids"}
		writeError(w, r, e)
		return
	}
	for i, id := range req.IDs {
		if slices.Contains(req.IDs[:i], id) {
			e := api.Errorf(api.CodeValidation, "ids.%d repeats an earlier id", i)
			e.Details = api.FieldDetail{Field: "ids." + strconv.Itoa(i)}
			writeError(w, r, e)
			return
		}
	}
	out := api.ProjectBatchResult{Results: make([]api.ProjectBatchItem, 0, len(req.IDs))}
	for _, id := range req.IDs {
		item := api.ProjectBatchItem{ID: id, Done: true}
		if err := do(id); err != nil {
			item.Done = false
			var e *api.Error
			if !errors.As(err, &e) {
				logf(r, "%s %s: %s: %v", r.Method, r.URL.Path, id, err)
				e = api.Errorf(api.CodeInternal, "internal server error")
			}
			item.Error = e
		}
		out.Results = append(out.Results, item)
	}
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, out)
}

// deleteMissingProjects removes every project whose folder is missing. It is
// refused with in_use, removing nothing, while open tickets use one of them.
func (s *Server) deleteMissingProjects(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("missing") != "true" {
		writeError(w, r, api.Errorf(api.CodeBadRequest, "DELETE /api/projects needs missing=true"))
		return
	}
	var removed []string
	err := s.removal(func() error {
		var used []string
		for _, p := range s.settings.Projects(true) {
			if !p.Missing {
				continue
			}
			nums, err := s.board.OpenTicketsWithProject(r.Context(), p.ID)
			if err != nil {
				return err
			}
			if len(nums) > 0 {
				used = append(used, fmt.Sprintf("%s (%s)", p.ID, ticketList(nums)))
			}
		}
		if len(used) > 0 {
			return api.Errorf(api.CodeInUse, "Open tickets use missing projects: %s. Remove the others one by one. Take a project off each open ticket first (Remove from ticket under Branches, or `omo ai ticket remove-project <N> <project>`), or cancel the tickets; done tickets keep it as their history.", strings.Join(used, "; "))
		}
		return nil
	}, func() (err error) {
		removed, err = s.settings.RemoveMissingProjects()
		return err
	})
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

// settingsResponse is the answer of the general settings routes. The revision is read before the settings: when a write lands in
// between, the answer holds newer settings under the older revision, and a
// write built on it is refused as stale rather than overwriting the newer
// file unseen.
func (s *Server) settingsResponse() api.SettingsResponse {
	rev := s.settings.Revision(settings.SectionGeneral, "")
	return api.SettingsResponse{
		Settings: api.General(s.settings.General()),
		Errors:   s.settings.Errors(),
		Revision: rev,
		Home:     s.opts.Layout.Root,
	}
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsResponse())
}

// putSettings replaces settings.yaml.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var sent api.General
	if err := decodeSettings(w, r, &sent); err != nil {
		writeError(w, r, err)
		return
	}
	_, err = s.settings.UpdateGeneral(rev, func(g *settings.General) error {
		*g = settings.General(sent)
		return nil
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, s.settingsResponse())
}

// generalField is a top-level key of settings.yaml with its place in
// api.General.
type generalField struct {
	index int
	// merge is set for objects: a PATCH value is merged into the current
	// value key by key. Every other value (numbers, durations, lists,
	// agent_env, and maps and lists inside an object such as
	// usage.windows) is replaced whole.
	merge bool
}

// generalFields maps the JSON names of api.General to their fields.
var generalFields = func() map[string]generalField {
	out := map[string]generalField{}
	t := reflect.TypeFor[api.General]()
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			panic("api.General field " + f.Name + " has no JSON name")
		}
		out[name] = generalField{index: i, merge: f.Type.Kind() == reflect.Struct}
	}
	return out
}()

// patchSettings changes only the top-level keys of settings.yaml the body
// names: objects are merged key by key into the current value, everything
// else is replaced. Unknown keys at any depth and null anywhere are refused.
// Reading the current value, merging, validating and writing happen under the store's lock, so the change is all or nothing.
func (s *Server) patchSettings(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	data, err := readSettingsBody(w, r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var top map[string]json.RawMessage
	if err := unmarshalStrict(data, &top, ""); err != nil {
		writeError(w, r, err)
		return
	}
	if top == nil {
		writeError(w, r, api.Errorf(api.CodeBadRequest, "the request body must be a JSON object of settings"))
		return
	}
	if len(top) == 0 {
		writeError(w, r, api.Errorf(api.CodeBadRequest, "the request body names no setting"))
		return
	}
	keys := sortedNames(top)
	for _, key := range keys {
		if _, ok := generalFields[key]; !ok {
			e := api.Errorf(api.CodeBadRequest, "invalid request body: unknown setting %s", strconv.QuoteToASCII(key))
			e.Details = api.FieldDetail{Field: key}
			writeError(w, r, e)
			return
		}
		if at, ok := nullIn(top[key], key); ok {
			e := api.Errorf(api.CodeBadRequest, "invalid request body: %s cannot be null", at)
			e.Details = api.FieldDetail{Field: at}
			writeError(w, r, e)
			return
		}
	}
	_, err = s.settings.UpdateGeneral(rev, func(g *settings.General) error {
		// A shallow copy: objects are merged by value, and lists and maps
		// are decoded into new ones, so the store's maps are never changed.
		next := api.General(*g)
		v := reflect.ValueOf(&next).Elem()
		for _, key := range keys {
			f := generalFields[key]
			field := v.Field(f.index)
			target := reflect.New(field.Type())
			if f.merge {
				target.Elem().Set(field)
				replaceNamed(target.Elem(), top[key])
			}
			if err := unmarshalStrict(top[key], target.Interface(), key); err != nil {
				return err
			}
			field.Set(target.Elem())
		}
		*g = settings.General(next)
		return nil
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, s.settingsResponse())
}

// replaceNamed clears, in the struct v, every map and list the JSON object
// raw names, at any depth of objects, so that decoding raw into v replaces
// them whole: encoding/json would add a map's keys to the current ones.
func replaceNamed(v reflect.Value, raw json.RawMessage) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		// Not an object: the decoding that follows refuses it.
		return
	}
	for key, sub := range obj {
		f, ok := jsonField(v.Type(), key)
		if !ok {
			continue
		}
		field := v.FieldByIndex(f.Index)
		switch field.Kind() {
		case reflect.Map, reflect.Slice:
			field.SetZero()
		case reflect.Struct:
			replaceNamed(field, sub)
		}
	}
}

// nullIn reports the dotted key of the first null in a JSON value, if any.
func nullIn(raw json.RawMessage, field string) (string, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		// Malformed JSON is refused by the decoding that follows.
		return "", false
	}
	return firstNull(v, field)
}

func firstNull(v any, at string) (string, bool) {
	switch v := v.(type) {
	case nil:
		return at, true
	case map[string]any:
		for _, k := range sortedNames(v) {
			if found, ok := firstNull(v[k], at+"."+k); ok {
				return found, true
			}
		}
	case []any:
		for i, e := range v {
			if found, ok := firstNull(e, at+"."+strconv.Itoa(i)); ok {
				return found, true
			}
		}
	}
	return "", false
}

// ---- profiles and models ----

// profileResponse is one profile as answered, with its revision (read before the profile, as in settingsResponse). ok is false
// when the profile is gone.
func (s *Server) profileResponse(name string) (api.Profile, bool) {
	rev := s.settings.Revision(settings.SectionProfiles, name)
	p, ok := s.settings.Profile(name)
	if !ok {
		return api.Profile{}, false
	}
	p.Revision = rev
	return p, true
}

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	errs := []api.FileError{}
	for _, e := range s.settings.Errors() {
		if filepath.Base(filepath.Dir(e.File)) == "profiles" {
			errs = append(errs, e)
		}
	}
	profiles := []api.Profile{}
	for _, p := range s.settings.Profiles() {
		if p, ok := s.profileResponse(p.Name); ok {
			profiles = append(profiles, p)
		}
	}
	writeJSON(w, http.StatusOK, api.ProfilesResponse{Profiles: profiles, Errors: errs})
}

// putProfile creates or replaces a profile. Models the new profile drops are
// refused while open tickets use them. If-Match "0" creates only.
func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var p api.Profile
	if err := decodeSettings(w, r, &p); err != nil {
		writeError(w, r, err)
		return
	}
	if err := bodyNameMatches(p.Name, r.PathValue("name")); err != nil {
		writeError(w, r, err)
		return
	}
	p.Name = r.PathValue("name")
	p.Revision = ""
	err = s.removal(func() error {
		// Read under the lock: the models dropped are those of the
		// profile the write replaces.
		if stored, exists := s.settings.Profile(p.Name); exists {
			return s.modelsInUse(r.Context(), modelIDs(stored, p.Models))
		}
		return nil
	}, func() error { return s.settings.PutProfileIf(rev, p) })
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	out, ok := s.profileResponse(p.Name)
	if !ok {
		writeError(w, r, api.Errorf(api.CodeConflict, "profile %s was removed right after it was written", p.Name))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// bodyNameMatches rejects a body that names another resource than the path:
// an absent name is accepted, a different one is not silently overridden.
func bodyNameMatches(body, path string) error {
	if body != "" && body != path {
		return api.Errorf(api.CodeValidation, "name %q in the body does not match %q in the path", body, path)
	}
	return nil
}

// deleteProfile removes a profile; refused while open tickets use one of its
// models. While roles list its models it is refused with in_use and the
// roles in the details (api.ProfileInUse), unless ?detach=true: then the
// models are taken out of the roles first and the answer (200,
// api.ProfileDeleted) names them; otherwise the answer is 204.
func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	detach := false
	if raw := r.URL.Query().Get("detach"); raw != "" {
		if detach, err = strconv.ParseBool(raw); err != nil {
			writeError(w, r, api.Errorf(api.CodeValidation, "detach must be true or false"))
			return
		}
	}
	name := r.PathValue("name")
	var detached []api.DetachedRole
	err = s.removal(func() error {
		if stored, ok := s.settings.Profile(name); ok {
			return s.modelsInUse(r.Context(), modelIDs(stored, nil))
		}
		return nil
	}, func() error {
		if !detach {
			return s.settings.DeleteProfileIf(rev, name)
		}
		var err error
		detached, err = s.settings.DeleteProfileDetachIf(rev, name)
		return err
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	if detach {
		writeJSON(w, http.StatusOK, api.ProfileDeleted{Detached: detached})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// listModels answers every model to the user and only the selectable ones to
// agents. api.Model carries no command, argument or environment.
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

// roleResponse is one role with its revision (read before the role).
func (s *Server) roleResponse(name string) (api.RoleSettings, bool) {
	rev := s.settings.Revision(settings.SectionRoles, name)
	role, ok := s.settings.Role(name)
	role.Revision = rev
	return role, ok
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	roles := []api.RoleSettings{}
	for _, role := range s.settings.Roles() {
		if role, ok := s.roleResponse(role.Name); ok {
			roles = append(roles, role)
		}
	}
	writeJSON(w, http.StatusOK, roles)
}

func (s *Server) putRole(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var role api.RoleSettings
	if err := decodeSettings(w, r, &role); err != nil {
		writeError(w, r, err)
		return
	}
	if err := bodyNameMatches(role.Name, r.PathValue("role")); err != nil {
		writeError(w, r, err)
		return
	}
	role.Name = r.PathValue("role")
	role.Revision = ""
	if err := s.settings.PutRoleIf(rev, role); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	stored, _ := s.roleResponse(role.Name)
	writeJSON(w, http.StatusOK, stored)
}

// ---- prompts ----

func (s *Server) listPrompts(w http.ResponseWriter, r *http.Request) {
	out := []api.PromptInfo{}
	for _, info := range s.settings.Prompts() {
		p, err := s.promptResponse(info.Name)
		if err != nil {
			continue
		}
		out = append(out, api.PromptInfo{Name: p.Name, Customized: p.Customized, Kind: p.Kind, Revision: p.Revision})
	}
	writeJSON(w, http.StatusOK, out)
}

// promptResponse is one prompt with its kind, omo's default and its revision
// (read before the text).
func (s *Server) promptResponse(name string) (api.Prompt, error) {
	rev := s.settings.Revision(settings.SectionPrompts, name)
	text, customized, ok := s.settings.Prompt(name)
	if !ok {
		return api.Prompt{}, api.Errorf(api.CodeNotFound, "no prompt %s", name)
	}
	return api.Prompt{
		Name:       name,
		Text:       text,
		Customized: customized,
		Kind:       prompts.Kind(name),
		Default:    s.opts.PromptDefaults[name],
		Revision:   rev,
	}, nil
}

func (s *Server) getPrompt(w http.ResponseWriter, r *http.Request) {
	p, err := s.promptResponse(r.PathValue("name"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) putPrompt(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req api.PromptUpdate
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	name := r.PathValue("name")
	if err := s.settings.PutPromptIf(rev, name, req.Text); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	p, err := s.promptResponse(name)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) deletePrompt(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.settings.ResetPromptIf(rev, r.PathValue("name")); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// sortedNames returns the keys of m in order, so the first refusal is always
// the same one.
func sortedNames[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
