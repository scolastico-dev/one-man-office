package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// useSkillsSh points the server's skills.sh at handler for this test.
func useSkillsSh(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	old := skillsShBase
	skillsShBase = srv.URL
	t.Cleanup(func() { skillsShBase = old })
}

// useSkillRepos maps a chosen result's owner/repo to a local folder below
// root (root/owner/repo) for this test.
func useSkillRepos(t *testing.T, root string) {
	t.Helper()
	old := skillSearchRepository
	skillSearchRepository = func(source string) string { return filepath.Join(root, filepath.FromSlash(source)) }
	t.Cleanup(func() { skillSearchRepository = old })
}

func TestSkillSearchRoute(t *testing.T) {
	var asked atomic.Value
	useSkillsSh(t, func(w http.ResponseWriter, r *http.Request) {
		asked.Store(r.URL.RawQuery)
		_, _ = w.Write([]byte(`{"query":"grill","searchType":"fuzzy","skills":[
{"id":"mattpocock/skills/grill-me","source":"mattpocock/skills","skillId":"grill-me","name":"grill-me","installs":1565244},
{"id":"mintlify.com/mintlify","source":"mintlify.com","skillId":"mintlify","name":"Mintlify","installs":10}]}`))
	})
	s := newTestServer(t)
	b := newBrowser(t, s)

	resp := b.do("GET", "/api/skillsets/search?q="+url.QueryEscape(" grill "), nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got := decodeBody[api.SkillSearchResponse](t, resp.Body)
	if got.Query != "grill" || len(got.Results) != 1 {
		t.Fatalf("answer = %+v, want one GitHub result", got)
	}
	want := api.SkillSearchResult{ID: "mattpocock/skills/grill-me", Name: "grill-me", SkillID: "grill-me", Source: "mattpocock/skills", Installs: 1565244,
		Repository: "https://github.com/mattpocock/skills.git", URL: "https://skills.sh/mattpocock/skills/grill-me"}
	if got.Results[0] != want {
		t.Errorf("result = %+v, want %+v", got.Results[0], want)
	}
	if q, _ := asked.Load().(string); q != "q=grill" {
		t.Errorf("skills.sh was asked %q, want only the search text", q)
	}

	// Too short: refused without asking skills.sh.
	asked.Store("")
	resp = b.do("GET", "/api/skillsets/search?q=g", nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if q, _ := asked.Load().(string); q != "" {
		t.Errorf("a short text was sent: %q", q)
	}
}

func TestSkillSearchRouteSaysWhenSkillsShFails(t *testing.T) {
	useSkillsSh(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>maintenance</html>`))
	})
	s := newTestServer(t)
	b := newBrowser(t, s)
	resp := b.do("GET", "/api/skillsets/search?q=grill", nil, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusServiceUnavailable)
	var env api.ErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != api.CodeUnavailable || !strings.HasPrefix(env.Error.Message, "skills.sh could not be reached: ") {
		t.Fatalf("error = %+v", env.Error)
	}
}

// bareSkillRepo makes root/owner/repo, a bare repository whose main branch
// holds a SKILL.md with the front matter name in each of dirs, and
// answers its commit.
func bareSkillRepo(t *testing.T, root, source string, dirs map[string]string) string {
	t.Helper()
	work := t.TempDir()
	bare := filepath.Join(root, filepath.FromSlash(source))
	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run(work, "init", "-q", "-b", "main")
	for dir, name := range dirs {
		full := filepath.Join(work, filepath.FromSlash(dir))
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "SKILL.md"), []byte("---\nname: "+name+"\n---\n# skill\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run(work, "add", ".")
	run(work, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "skills")
	if err := os.MkdirAll(filepath.Dir(bare), 0o755); err != nil {
		t.Fatal(err)
	}
	run(work, "clone", "-q", "--bare", work, bare)
	return run(bare, "rev-parse", "main")
}

func TestSkillSearchResolve(t *testing.T) {
	isolateGit(t)
	repos := t.TempDir()
	useSkillRepos(t, repos)
	commit := bareSkillRepo(t, repos, "matt/skills", map[string]string{
		"skills/productivity/grill-me": "grill-me",
		"skills/productivity/grilling": "grilling",
	})
	s := newTestServer(t)
	b := newBrowser(t, s)
	resolve := func(c api.SkillSearchChoice) *http.Response {
		return b.do("POST", "/api/skillsets/search/resolve", c, bearer(s))
	}

	resp := resolve(api.SkillSearchChoice{Source: "matt/skills", SkillID: "grill-me", Name: "grill-me"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	got := decodeBody[api.SkillSearchResolved](t, resp.Body)
	if got.Repository != filepath.Join(repos, "matt", "skills") || got.Ref != "" || !got.Found || got.Commit != commit ||
		len(got.Paths) != 1 || got.Paths[0] != "skills/productivity/grill-me" {
		t.Fatalf("resolved = %+v", got)
	}

	// A skill the repository does not have: no path, not found.
	resp = resolve(api.SkillSearchChoice{Source: "matt/skills", SkillID: "gone", Name: "gone"})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := decodeBody[api.SkillSearchResolved](t, resp.Body); got.Found || got.Paths == nil || len(got.Paths) != 0 {
		t.Fatalf("missing skill resolved = %+v", got)
	}

	// Nothing is stored and the temporary checkouts are gone.
	if sets := s.settings.SkillSets(); len(sets) != 0 {
		t.Fatalf("resolving stored %+v", sets)
	}
	left, _ := filepath.Glob(filepath.Join(s.opts.Layout.Skills, ".*"))
	if len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}

	// A repository that cannot be fetched says so.
	resp = resolve(api.SkillSearchChoice{Source: "nobody/nothing", SkillID: "x"})
	wantStatus(t, resp.StatusCode, http.StatusServiceUnavailable)
	var env api.ErrorEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil || !strings.Contains(env.Error.Message, "nobody/nothing could not be fetched") {
		t.Fatalf("error = %+v, %v", env.Error, err)
	}
}

func TestSkillSearchResolveRefusesHostileChoices(t *testing.T) {
	repos := t.TempDir()
	useSkillRepos(t, repos)
	s := newTestServer(t)
	b := newBrowser(t, s)
	for _, c := range []struct {
		choice api.SkillSearchChoice
		field  string
	}{
		{api.SkillSearchChoice{Source: "../../etc", SkillID: "x"}, "source"},
		{api.SkillSearchChoice{Source: "evil.com", SkillID: "x"}, "source"},
		{api.SkillSearchChoice{Source: "a/b/c", SkillID: "x"}, "source"},
		{api.SkillSearchChoice{Source: "-u/x", SkillID: "x"}, "source"},
		{api.SkillSearchChoice{Source: "o/r", SkillID: "../x"}, "skill_id"},
		{api.SkillSearchChoice{Source: "o/r", SkillID: ""}, "skill_id"},
		{api.SkillSearchChoice{Source: "o/r", SkillID: "x", Name: "two\nlines"}, "name"},
	} {
		resp := b.do("POST", "/api/skillsets/search/resolve", c.choice, bearer(s))
		wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
		var env api.ErrorEnvelope
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			t.Fatal(err)
		}
		d, _ := json.Marshal(env.Error.Details)
		if !strings.Contains(string(d), `"`+c.field+`"`) {
			t.Errorf("%+v: details %s, want field %s", c.choice, d, c.field)
		}
	}
	resp := b.do("POST", "/api/skillsets/search/resolve", map[string]any{"source": "o/r", "skill_id": "x", "repository": "file:///etc"}, bearer(s))
	wantStatus(t, resp.StatusCode, http.StatusBadRequest)
}

func TestStartRemovesLeftSearchFolders(t *testing.T) {
	s := newTestServer(t)
	left := filepath.Join(s.opts.Layout.Skills, ".search-123")
	if err := os.MkdirAll(left, 0o700); err != nil {
		t.Fatal(err)
	}
	s.startSkillSearch()
	if _, err := os.Lstat(left); err == nil {
		t.Fatal("a temporary folder of an earlier run is still there")
	}
}
