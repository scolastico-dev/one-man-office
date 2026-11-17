package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/skillsets"
)

// Searching skills.sh from the Skills page. The server asks skills.sh, never
// the browser: one place for the timeout, the size limit and the errors, and
// no request to a third party from the page. Only the search text is sent.
// Choosing a result fetches its repository once into a temporary folder and
// looks for the skill's folder there; nothing is stored until the user saves
// the skill set.

var (
	// skillsShBase is the address of skills.sh ("": the real one); tests
	// point it at their own server.
	skillsShBase string
	// skillSearchRepository is the git address of a GitHub repository
	// owner/repo; tests map it to a local repository.
	skillSearchRepository = skillsets.SourceRepository
)

const (
	// searchFolderPrefix starts the name of the temporary folder (in the
	// skills folder) a chosen result's repository is fetched into.
	searchFolderPrefix = ".search-"
	// maxResolving is how many chosen results are looked at at once.
	maxResolving = 2
	// resolveBound bounds the fetch of a chosen result's repository.
	resolveBound = 2 * time.Minute
	// maxChoiceName is the most bytes of a chosen result's name.
	maxChoiceName = 512
)

// startSkillSearch makes the searcher and removes temporary folders of
// looks at chosen results that an earlier run left behind.
func (s *Server) startSkillSearch() {
	s.skills.search = skillsets.NewSearcher(skillsets.SearchOptions{Base: skillsShBase})
	s.skills.resolving = make(chan struct{}, maxResolving)
	left, _ := filepath.Glob(filepath.Join(s.opts.Layout.Skills, searchFolderPrefix+"*"))
	for _, dir := range left {
		if err := os.RemoveAll(dir); err != nil {
			s.log.Printf("skill sets: remove %s: %v", dir, err)
		}
	}
}

// searchSkills answers what skills.sh finds for ?q= (GET
// /api/skillsets/search).
func (s *Server) searchSkills(w http.ResponseWriter, r *http.Request) {
	q, err := skillsets.SearchText(r.URL.Query().Get("q"))
	if err != nil {
		e := api.Errorf(api.CodeValidation, "q: %s", err.Error())
		e.Details = api.FieldDetail{Field: "q"}
		writeError(w, r, e)
		return
	}
	if s.skills.search == nil {
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is still starting; try again in a moment"))
		return
	}
	results, err := s.skills.search.Search(r.Context(), q)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writeError(w, r, api.Errorf(api.CodeUnavailable, "%s; try again later", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, api.SkillSearchResponse{Query: q, Results: results})
}

// resolveSkillSearch answers what a new skill set for a chosen result
// starts with (POST /api/skillsets/search/resolve): the repository on
// GitHub, its default branch and the folder of the skill whose name
// matches, found in a fresh fetch of the repository that is removed after.
func (s *Server) resolveSkillSearch(w http.ResponseWriter, r *http.Request) {
	var c api.SkillSearchChoice
	if err := decodeSettings(w, r, &c); err != nil {
		writeError(w, r, err)
		return
	}
	if err := checkChoice(c); err != nil {
		writeError(w, r, err)
		return
	}
	if s.skills.resolving == nil {
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is still starting; try again in a moment"))
		return
	}
	select {
	case s.skills.resolving <- struct{}{}:
		defer func() { <-s.skills.resolving }()
	default:
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is already looking at %d chosen skills; try again in a moment", maxResolving))
		return
	}
	repo := skillSearchRepository(c.Source)
	parent, err := os.MkdirTemp(s.opts.Layout.Skills, searchFolderPrefix)
	if err != nil {
		writeError(w, r, err)
		return
	}
	defer func() {
		if err := os.RemoveAll(parent); err != nil {
			s.log.Printf("skill sets: remove %s: %v", parent, err)
		}
	}()
	ctx, cancel := context.WithTimeout(r.Context(), resolveBound)
	defer cancel()
	dir := filepath.Join(parent, "repository")
	commit, err := s.git.FetchSource(ctx, repo, "", dir)
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		msg := skillsets.Message(err)
		if ctx.Err() != nil {
			msg = "it took longer than " + resolveBound.String()
		}
		writeError(w, r, api.Errorf(api.CodeUnavailable, "the repository %s could not be fetched: %s", c.Source, msg))
		return
	}
	out := api.SkillSearchResolved{Repository: repo, Paths: []string{}, Commit: commit}
	if folder, ok := skillsets.FolderOf(dir, c.SkillID, c.Name); ok {
		out.Found = true
		if folder != "" {
			out.Paths = []string{folder}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// checkChoice refuses a chosen result that is not a GitHub repository and
// a plain skill id, naming the field.
func checkChoice(c api.SkillSearchChoice) error {
	field := func(key, why string) error {
		e := api.Errorf(api.CodeValidation, "%s %s", key, why)
		e.Details = api.FieldDetail{Field: key}
		return e
	}
	switch {
	case !skillsets.ValidSource(c.Source):
		return field("source", "must be a GitHub repository as owner/repo")
	case !skillsets.ValidSkillID(c.SkillID):
		return field("skill_id", "must be a skill's id: letters, digits, '.', '_' and '-'")
	case len(c.Name) > maxChoiceName || strings.ContainsAny(c.Name, "\x00\r\n"):
		return field("name", "must be one line of at most 512 bytes")
	}
	return nil
}
