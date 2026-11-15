package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// The batch handles each project as the single routes would and answers
// per id; one refusal stops nothing.
func TestProjectBatch(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "work")
	gitInit(t, filepath.Join(root, "found"))
	l := scanHome(t, base)
	writeRoots(t, l, root)
	s := openScanServer(t, l, nil)
	waitScanned(t, s, scanStart)
	found, ok := projectByPath(s, filepath.Join(root, "found"))
	if !ok {
		t.Fatal("the scan found nothing")
	}
	free := addProject(t, s)
	used := addProject(t, s)
	u := userCaller(t, s)
	tk := okJSON[api.Ticket](t, u, http.StatusCreated, "POST", "/api/tickets", api.CreateTicket{Type: api.TypeTask, Title: "work", Projects: []string{used}})

	batch := func(action string, ids ...string) []api.ProjectBatchItem {
		t.Helper()
		return okJSON[api.ProjectBatchResult](t, u, http.StatusOK, "POST", "/api/projects/batch", api.ProjectBatch{IDs: ids, Action: action}).Results
	}
	type want struct {
		done bool
		code string
	}
	check := func(got []api.ProjectBatchItem, ids []string, wants []want) {
		t.Helper()
		if len(got) != len(ids) {
			t.Fatalf("results %+v", got)
		}
		for i, w := range wants {
			g := got[i]
			code := ""
			if g.Error != nil {
				code = g.Error.Code
				if g.Error.Message == "" {
					t.Errorf("%s: refusal without a message", g.ID)
				}
			}
			if g.ID != ids[i] || g.Done != w.done || code != w.code {
				t.Errorf("result %d = %+v (error %+v), want %s done=%v code=%q", i, g, g.Error, ids[i], w.done, w.code)
			}
		}
	}

	ids := []string{used, found.ID, "nope", free}
	got := batch(api.ProjectBatchDisable, ids...)
	check(got, ids, []want{{true, ""}, {true, ""}, {false, api.CodeNotFound}, {true, ""}})
	for _, id := range []string{used, found.ID, free} {
		if p, _ := s.settings.Project(id); p.Enabled {
			t.Errorf("%s is still on", id)
		}
	}
	got = batch(api.ProjectBatchEnable, used, found.ID)
	check(got, []string{used, found.ID}, []want{{true, ""}, {true, ""}})
	if p, _ := s.settings.Project(used); !p.Enabled {
		t.Error("used is still off")
	}

	got = batch(api.ProjectBatchRemove, ids...)
	check(got, ids, []want{{false, api.CodeInUse}, {false, api.CodeValidation}, {false, api.CodeNotFound}, {true, ""}})
	if !strings.Contains(got[0].Error.Message, "#"+strconv.Itoa(tk.Number)) || !strings.Contains(got[1].Error.Message, "workspace root") {
		t.Errorf("refusals %+v / %+v", got[0].Error, got[1].Error)
	}
	for id, present := range map[string]bool{used: true, found.ID: true, free: false} {
		if _, ok := s.settings.Project(id); ok != present {
			t.Errorf("%s present = %v, want %v", id, ok, present)
		}
	}

	for _, c := range []struct {
		body  any
		field string
	}{
		{map[string]any{"ids": []string{used}, "action": "delete"}, "action"},
		{map[string]any{"ids": []string{}, "action": "remove"}, "ids"},
		{map[string]any{"action": "remove"}, "ids"},
		{map[string]any{"ids": []string{used, used}, "action": "disable"}, "ids.1"},
	} {
		status, data := u.do("POST", "/api/projects/batch", c.body)
		var env errorEnvelope
		_ = json.Unmarshal(data, &env)
		if status != http.StatusUnprocessableEntity || env.Error.Code != api.CodeValidation || env.Error.Details.Field != c.field {
			t.Errorf("%v: status %d %s", c.body, status, data)
		}
	}
	u.fails(http.StatusBadRequest, api.CodeBadRequest, "POST", "/api/projects/batch", map[string]any{"ids": []string{used}, "action": "remove", "force": true})
}
