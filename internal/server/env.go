package server

import (
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// boardEnv gives the board the settings and git access it needs.
type boardEnv struct {
	st  *settings.Store
	git *gitops.Git
}

func (e boardEnv) Policy() board.Policy {
	g := e.st.General()
	return board.Policy{
		CEOEnabled:              g.CEO.Enabled,
		CEOMayApproveStories:    g.CEO.MayApproveStories,
		TasksRequireUserRelease: g.Tasks.RequireUserRelease,
		ReviewStory:             g.ReviewDefaults.Story,
		ReviewTask:              g.ReviewDefaults.Task,
		WhenApprovedStory:       g.WhenApprovedDefaults.Story,
		WhenApprovedTask:        g.WhenApprovedDefaults.Task,
	}
}

func (e boardEnv) Project(id string) (board.ProjectInfo, bool) {
	p, ok := e.st.Project(id)
	if !ok {
		return board.ProjectInfo{}, false
	}
	return board.ProjectInfo{ID: p.ID, Path: p.Path, Enabled: p.Enabled, Missing: p.Missing}, true
}

func (e boardEnv) Model(id string) (board.ModelInfo, bool) {
	m, ok := e.st.Model(id)
	if !ok {
		return board.ModelInfo{}, false
	}
	return board.ModelInfo{ID: m.ID, Selectable: m.Selectable}, true
}

func (e boardEnv) CurrentBranch(path string) (string, error) {
	return e.git.CurrentBranch(path)
}
