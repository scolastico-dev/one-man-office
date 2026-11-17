package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// A project's merge modes: its own (possibly empty) and the ones in
// effect, set key by key through PATCH, and the general defaults through
// the settings.
func TestPatchProjectMergeModes(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	id := addProject(t, s)
	path := "/api/projects/" + id
	merge, squash := api.MergeModeMerge, api.MergeModeSquash

	p := okJSON[api.Project](t, u, 200, "PATCH", path, map[string]any{"merge": map[string]any{"task": squash}})
	if p.Merge != (api.MergeModes{Task: squash}) || p.EffectiveMerge != (api.MergeModes{Story: merge, Task: squash, StoryTask: merge}) {
		t.Fatalf("project %+v", p)
	}
	p = okJSON[api.Project](t, u, 200, "PATCH", path, map[string]any{"merge": map[string]any{"story": squash}})
	if p.Merge != (api.MergeModes{Story: squash, Task: squash}) || p.EffectiveMerge.Story != squash {
		t.Fatalf("a second key changed the first: %+v", p)
	}
	p = okJSON[api.Project](t, u, 200, "PATCH", path, map[string]any{"merge": map[string]any{"task": ""}})
	if p.Merge.Task != "" || p.EffectiveMerge.Task != merge {
		t.Fatalf("back to the default: %+v", p)
	}
	p = okJSON[api.Project](t, u, 200, "PATCH", path, map[string]any{"merge": map[string]any{"story": merge}})
	if p.Merge.Story != merge {
		t.Fatalf("merge.story: %+v", p)
	}

	resp := asUser(newBrowser(t, s), s, "PATCH", path, map[string]any{"merge": map[string]any{"story_task": "rebase"}})
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if env := body[errorEnvelope](t, resp); env.Error.Details.Field != "merge.story_task" {
		t.Fatalf("error %+v", env)
	}
	for _, b := range []map[string]any{
		{"merge": nil},
		{"merge": map[string]any{"task": nil}},
		{"merge": map[string]any{"fast": "x"}},
		// The story's mode has one key only.
		{"story_merge": squash},
	} {
		u.fails(http.StatusBadRequest, api.CodeBadRequest, "PATCH", path, b)
	}

	// The general defaults change what a project without its own mode
	// gets.
	u.fails(http.StatusUnprocessableEntity, api.CodeInvalidSettings, "PATCH", "/api/settings", map[string]any{"merge_defaults": map[string]any{"task": "rebase"}})
	okJSON[api.SettingsResponse](t, u, 200, "PATCH", "/api/settings", map[string]any{"merge_defaults": map[string]any{"story_task": squash}})
	s.waitSettingsEvents(context.Background())
	if g := s.settings.General(); g.MergeDefaults != (api.MergeModes{Story: merge, Task: merge, StoryTask: squash}) {
		t.Fatalf("merge defaults %+v", g.MergeDefaults)
	}
	list := okJSON[[]api.Project](t, u, 200, "GET", "/api/projects?all=true", nil)
	if len(list) != 1 || list[0].Merge.StoryTask != "" || list[0].EffectiveMerge.StoryTask != squash {
		t.Fatalf("listed %+v", list)
	}
}
