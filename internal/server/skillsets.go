package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/settings"
	"github.com/scolastico-dev/one-man-office/internal/skillsets"
)

// skillSetFeature holds the state of the skill set routes and checkouts.
type skillSetFeature struct {
	m *skillsets.Manager
	// search asks skills.sh (skillsets_search.go); resolving bounds the
	// chosen results looked at at once.
	search    *skillsets.Searcher
	resolving chan struct{}
}

// registerSkillSets registers the /api/skillsets routes. They are the
// user's only: a skill set steers every agent.
func (s *Server) registerSkillSets(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"GET /api/skillsets", s.listSkillSets, userOnly},
		{"PUT /api/skillsets/{name}", s.putSkillSet, userOnly},
		{"DELETE /api/skillsets/{name}", s.deleteSkillSet, userOnly},
		{"POST /api/skillsets/{name}/update", s.updateSkillSet, userOnly},
		{"GET /api/skillsets/search", s.searchSkills, userOnly},
		{"POST /api/skillsets/search/resolve", s.resolveSkillSearch, userOnly},
	})
}

// skillSetPoll is how often the skill set manager compares the settings
// with the checkouts by itself (0: skillsets.DefaultPoll). Changes that go
// through the settings feed reach it at once (skillSetSettingsChanged);
// the poll is a fallback. Tests change it.
var skillSetPoll time.Duration

// startSkillSets starts the skill set checkouts. It runs after the board and
// the settings exist and before the engine starts. Fetches run in the
// background: neither the start nor an agent's start waits for them.
func (s *Server) startSkillSets(ctx context.Context) {
	s.skills.m = skillsets.New(skillsets.Options{
		Dir: s.opts.Layout.Skills, Git: s.git, Store: s.settings, Log: s.log,
		OnChange: s.skillSetChanged, Poll: skillSetPoll,
	})
	s.skills.m.Start(ctx)
	s.startSkillSearch()
}

// stopSkillSets stops the skill set work before the database is closed: it
// cancels running fetches and waits for them.
func (s *Server) stopSkillSets() {
	if s.skills.m != nil {
		s.skills.m.Stop()
	}
}

// skillSetSettingsChanged lets the checkouts follow a change of a skill
// set's file, also one made by hand.
func (s *Server) skillSetSettingsChanged(c settings.Change) {
	if c.Section == settings.SectionSkillSets && s.skills.m != nil {
		s.skills.m.Reconcile()
	}
}

// skillSetChanged records and announces skillset.changed.
func (s *Server) skillSetChanged(st api.SkillSetState) {
	if err := s.appendSystemEvent(api.KindSkillSetChanged, st); err != nil {
		s.log.Printf("record %s event: %v", api.KindSkillSetChanged, err)
		return
	}
	s.events.Wake()
}

// skillSetText is the skill set blocks of role, as the engine appends them
// to the role's instructions.
func (s *Server) skillSetText(role string) string {
	return skillsets.Text(role, s.settings.SkillSets(), skillsets.CheckoutIn(s.opts.Layout.Skills))
}

func (s *Server) listSkillSets(w http.ResponseWriter, r *http.Request) {
	errs := []api.FileError{}
	for _, e := range s.settings.Errors() {
		if filepath.Base(filepath.Dir(e.File)) == "skillsets" {
			errs = append(errs, e)
		}
	}
	sets := s.settings.SkillSets()
	if sets == nil {
		sets = []api.SkillSet{}
	}
	states := []api.SkillSetState{}
	if s.skills.m != nil {
		states = s.skills.m.States()
	}
	writeJSON(w, http.StatusOK, api.SkillSetsResponse{SkillSets: sets, States: states, Errors: errs})
}

// putSkillSet creates or replaces a skill set. A new set, or a changed
// repository or ref of a set that updates at start, is fetched in the
// background; the answer does not wait for it.
func (s *Server) putSkillSet(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var k api.SkillSet
	if err := decodeSettings(w, r, &k); err != nil {
		writeError(w, r, err)
		return
	}
	name := r.PathValue("name")
	if err := bodyNameMatches(k.Name, name); err != nil {
		writeError(w, r, err)
		return
	}
	k.Name, k.Revision = name, ""
	if err := s.checkSkillSetPaths(k); err != nil {
		writeError(w, r, err)
		return
	}
	if err := s.settings.PutSkillSetIf(rev, k); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	if s.skills.m != nil {
		s.skills.m.Reconcile()
	}
	out, ok := s.settings.SkillSet(name)
	if !ok {
		writeError(w, r, api.Errorf(api.CodeConflict, "skill set %s was removed right after it was written", name))
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// checkSkillSetPaths checks k as the store does, then refuses a path of k that the set's checkout does not
// have, when that checkout is of the same repository and ref as k (a new
// source is fetched after the write; its state then names the paths it
// does not have). The error names the field paths.N.
func (s *Server) checkSkillSetPaths(k api.SkillSet) error {
	if err := settings.ValidateSkillSet(k); err != nil {
		return err
	}
	old, ok := s.settings.SkillSet(k.Name)
	if !ok || s.skills.m == nil || old.Repository != k.Repository || old.Ref != k.Ref {
		return nil
	}
	path, ok := s.skills.m.Checkout(k.Name)
	if !ok {
		return nil
	}
	missing := skillsets.Missing(path, k.Paths)
	if len(missing) == 0 {
		return nil
	}
	i := slices.Index(k.Paths, missing[0])
	st := s.skills.m.State(k.Name)
	at := ""
	if len(st.Commit) >= 7 {
		at = " at " + st.Commit[:7]
	}
	key := fmt.Sprintf("paths.%d", i)
	e := api.Errorf(api.CodeValidation, "%s is not a folder or file of the checkout%s; check its spelling (relative to the repository's root) or update the skill set first", key, at)
	e.Details = api.FieldDetail{Field: key}
	return e
}

// deleteSkillSet removes a skill set's file and then its checkout.
func (s *Server) deleteSkillSet(w http.ResponseWriter, r *http.Request) {
	rev, err := ifMatch(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	name := r.PathValue("name")
	if err := s.settings.DeleteSkillSetIf(rev, name); err != nil {
		writeError(w, r, err)
		return
	}
	s.waitSettingsEvents(r.Context())
	if s.skills.m != nil {
		s.skills.m.Removed(name)
	}
	w.WriteHeader(http.StatusNoContent)
}

// updateSkillSet fetches a skill set now and answers its state once the
// fetch is over (git bounds it to 5 minutes) or the request ends.
func (s *Server) updateSkillSet(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if err := decodeSettings(w, r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	name := r.PathValue("name")
	if _, ok := s.settings.SkillSet(name); !ok || s.skills.m == nil {
		writeError(w, r, api.Errorf(api.CodeNotFound, "no skill set %q", name))
		return
	}
	writeJSON(w, http.StatusOK, s.skills.m.Update(r.Context(), name))
}
