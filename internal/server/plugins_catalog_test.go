package server

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/pluginmanager"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/testpath"
)

// listRepo is a local repository that stands in for the release branch of
// omo's repository: a plugin list and plugin folders.
type listRepo struct {
	t          *testing.T
	work, bare string
}

func newListRepo(t *testing.T) *listRepo {
	t.Helper()
	r := &listRepo{t: t, work: t.TempDir(), bare: filepath.Join(t.TempDir(), "omo.git")}
	r.git(r.work, "init", "-q", "-b", "main")
	r.git(filepath.Dir(r.bare), "init", "-q", "--bare", "-b", "main", r.bare)
	return r
}

func (r *listRepo) git(dir string, args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// publish writes files and pushes them to main.
func (r *listRepo) publish(files map[string]string) {
	r.t.Helper()
	for rel, text := range files {
		p := filepath.Join(r.work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			r.t.Fatal(err)
		}
	}
	r.git(r.work, "add", "-A")
	r.git(r.work, "commit", "-q", "--allow-empty", "-m", "list")
	r.git(r.work, "push", "-q", r.bare, "main")
}

// plugin writes the recorder plugin named name into folder of the work tree.
func (r *listRepo) plugin(folder, name string) map[string]string {
	raw, err := json.Marshal(recordManifest(name, plugins.EventTicketMoved))
	if err != nil {
		r.t.Fatal(err)
	}
	return map[string]string{
		folder + "/plugin.json": string(raw),
		folder + "/hello.lua":   "return 'hello'\n",
		folder + "/ping.lua":    "return { message = 'pong' }\n",
	}
}

// listServer is a test server whose plugin list is read from a listRepo
// with an official plugin "off" (in the list's repository), a recommended
// "rec" and a community "com" (each in a repository of its own).
func listServer(t *testing.T) (*Server, *browser, *listRepo, map[string]string) {
	t.Helper()
	isolateGit(t)
	s := newTestServer(t)
	b := newBrowser(t, s)
	rec := pluginRepo(t, recordManifest("rec", plugins.EventTicketMoved))
	com := pluginRepo(t, recordManifest("com", plugins.EventTicketMoved))
	r := newListRepo(t)
	files := r.plugin("plugins/off", "off")
	files["plugins/index.yaml"] = `plugins:
  - name: off
    tag: official
    description: "Official <b>plugin</b>"
    path: plugins/off
  - name: rec
    tag: recommended
    description: A recommended plugin.
    source: ` + rec + `
  - name: com
    tag: community
    description: A community plugin.
    source: ` + com + `
`
	r.publish(files)
	s.opts.PluginCatalog = pluginmanager.CatalogLocation{Source: r.bare, Ref: "main"}
	return s, b, r, map[string]string{"rec": rec, "com": com}
}

func readCatalogRoute(t *testing.T, s *Server, b *browser, query string) api.PluginCatalog {
	t.Helper()
	resp := asUser(b, s, "GET", "/api/plugins/catalog"+query, nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	return body[api.PluginCatalog](t, resp)
}

func TestPluginCatalogRoute(t *testing.T) {
	s, b, r, src := listServer(t)
	got := readCatalogRoute(t, s, b, "")
	if !got.Available || got.Message != "" || got.Source != r.bare || got.Ref != "main" || got.FetchedAt == nil || len(got.Plugins) != 3 {
		t.Fatalf("catalog %+v", got)
	}
	want := []api.CatalogPlugin{
		{Name: "off", Description: "Official <b>plugin</b>", Tag: api.PluginOfficial, Source: r.bare, Ref: "main", Subpath: "plugins/off"},
		{Name: "rec", Description: "A recommended plugin.", Tag: api.PluginRecommended, Source: src["rec"]},
		{Name: "com", Description: "A community plugin.", Tag: api.PluginCommunity, Source: src["com"]},
	}
	for i, p := range want {
		if got.Plugins[i] != p {
			t.Errorf("plugin %d = %+v, want %+v", i, got.Plugins[i], p)
		}
	}

	// The answer is kept for a while; refresh reads the list again.
	r.publish(map[string]string{"plugins/index.yaml": "plugins: []\n"})
	if again := readCatalogRoute(t, s, b, ""); len(again.Plugins) != 3 || !again.FetchedAt.Equal(*got.FetchedAt) {
		t.Fatalf("not cached: %+v", again)
	}
	if fresh := readCatalogRoute(t, s, b, "?refresh=true"); !fresh.Available || len(fresh.Plugins) != 0 {
		t.Fatalf("refresh: %+v", fresh)
	}

	// A broken list is a clear state, not an error of the route.
	r.publish(map[string]string{"plugins/index.yaml": "plugins:\n  - name: x\n    tag: gold\n    description: d\n    source: " + testpath.Abs("/x") + "\n"})
	broken := readCatalogRoute(t, s, b, "?refresh=true")
	if broken.Available || !strings.Contains(broken.Message, "tag must be") || len(broken.Plugins) != 0 || broken.FetchedAt == nil {
		t.Fatalf("broken list: %+v", broken)
	}
	// The installed plugins still read.
	resp := asUser(b, s, "GET", "/api/plugins", nil)
	wantStatus(t, resp.StatusCode, http.StatusOK)

	// Without a list (the zero location).
	s.opts.PluginCatalog = pluginmanager.CatalogLocation{}
	none := readCatalogRoute(t, s, b, "?refresh=true")
	if none.Available || none.Message == "" || none.Plugins == nil {
		t.Fatalf("no list: %+v", none)
	}
}

// confirmation is the tag of a refusal that needs an acknowledgement, or ""
// for another answer.
func confirmation(t *testing.T, resp *http.Response) string {
	t.Helper()
	text := respText(t, resp)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		return ""
	}
	var env struct {
		Error struct {
			Details api.PluginConfirmation `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Details.Field != "acknowledge" {
		return ""
	}
	return env.Error.Details.Tag
}

func install(name, source, subpath, ref, ack string) api.PluginInstall {
	return api.PluginInstall{PluginSettings: api.PluginSettings{Name: name, Source: source, Subpath: subpath, Ref: ref}, Acknowledge: ack}
}

// TestPluginInstallTagDecidedByServer: the server takes the tag from its own
// list, an official plugin installs at once, the others only with an
// acknowledgement that covers their tag, and a source the list does not
// name counts as community.
func TestPluginInstallTagDecidedByServer(t *testing.T) {
	s, b, r, src := listServer(t)

	// Official: no question.
	resp := asUser(b, s, "POST", "/api/plugins", install("off", r.bare, "plugins/off", "main", ""))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("official: %d %s", resp.StatusCode, respText(t, resp))
	}

	// Recommended: recommended or community acknowledges it.
	resp = asUser(b, s, "POST", "/api/plugins", install("rec", src["rec"], "", "", ""))
	if tag := confirmation(t, resp); tag != api.PluginRecommended {
		t.Fatalf("recommended without acknowledgement: tag %q", tag)
	}
	resp = asUser(b, s, "POST", "/api/plugins", install("rec", src["rec"], "", "", api.PluginRecommended))
	wantStatus(t, resp.StatusCode, http.StatusCreated)

	// Community: only community acknowledges it.
	for _, ack := range []string{"", api.PluginRecommended} {
		resp = asUser(b, s, "POST", "/api/plugins", install("com", src["com"], "", "", ack))
		if tag := confirmation(t, resp); tag != api.PluginCommunity {
			t.Fatalf("community with %q: tag %q", ack, tag)
		}
	}
	if _, ok := s.settings.Plugin("com"); ok {
		t.Fatal("a refused install wrote settings")
	}
	if _, err := os.Lstat(filepath.Join(s.opts.Layout.Plugins, "com")); err == nil {
		t.Fatal("a refused install fetched the plugin")
	}
	resp = asUser(b, s, "POST", "/api/plugins", install("com", src["com"], "", "", api.PluginCommunity))
	wantStatus(t, resp.StatusCode, http.StatusCreated)

	// The official plugin's folder at another ref is a typed source:
	// community.
	resp = asUser(b, s, "POST", "/api/plugins", install("off2", r.bare, "plugins/off", "", ""))
	if tag := confirmation(t, resp); tag != api.PluginCommunity {
		t.Fatalf("official folder at another ref: tag %q", tag)
	}
	// An unknown acknowledgement is refused as such.
	resp = asUser(b, s, "POST", "/api/plugins", install("x", src["com"], "", "", api.PluginOfficial))
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if !strings.Contains(respText(t, resp), `"field":"acknowledge"`) {
		t.Fatal("an unknown acknowledgement was not refused by field")
	}
	// A bad field is named before any acknowledgement is asked for.
	resp = asUser(b, s, "POST", "/api/plugins", install("bad", "ext::sh -c x", "", "", ""))
	wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
	if !strings.Contains(respText(t, resp), `"field":"source"`) {
		t.Fatal("a bad source was not refused by field")
	}

	// While the list cannot be read, even the official source is community.
	s.opts.PluginCatalog = pluginmanager.CatalogLocation{Source: filepath.Join(t.TempDir(), "gone.git")}
	if _, _, err := s.readCatalog(t.Context(), true); err == nil {
		t.Fatal("a missing list was read")
	}
	resp = asUser(b, s, "POST", "/api/plugins", install("off3", r.bare, "plugins/off", "main", ""))
	if tag := confirmation(t, resp); tag != api.PluginCommunity {
		t.Fatalf("official without a list: tag %q", tag)
	}
}

// TestPluginUpdateAcknowledgement: an update from the installed source needs
// nothing; a source changed by hand, or by PUT, is asked about again.
func TestPluginUpdateAcknowledgement(t *testing.T) {
	s, b, r, src := listServer(t)
	resp := asUser(b, s, "POST", "/api/plugins", install("com", src["com"], "", "", api.PluginCommunity))
	wantStatus(t, resp.StatusCode, http.StatusCreated)
	resp = asUser(b, s, "POST", "/api/plugins/com/update", api.PluginUpdate{})
	wantStatus(t, resp.StatusCode, http.StatusOK)

	// The settings file now names another source (a hand edit).
	other := pluginRepo(t, recordManifest("com", plugins.EventTicketMoved))
	cur, _ := s.settings.Plugin("com")
	cur.Source = other
	if err := s.settings.PutPluginIf(cur.Revision, cur); err != nil {
		t.Fatal(err)
	}
	resp = asUser(b, s, "POST", "/api/plugins/com/update", api.PluginUpdate{})
	if tag := confirmation(t, resp); tag != api.PluginCommunity {
		t.Fatalf("update from a changed source: tag %q", tag)
	}
	resp = asUser(b, s, "POST", "/api/plugins/com/update", api.PluginUpdate{Acknowledge: api.PluginCommunity})
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if inst, err := pluginmanager.Installed(s.plugins.o, "com"); err != nil || inst.Source != other {
		t.Fatalf("installed %+v, %v", inst, err)
	}

	// A PUT that moves a plugin to an official source needs nothing; one to
	// a community source needs the acknowledgement.
	cur, _ = s.settings.Plugin("com")
	next := cur
	next.Source, next.Subpath, next.Ref = r.bare, "plugins/off", "main"
	h := bearer(s)
	h["If-Match"] = `"` + cur.Revision + `"`
	resp = b.do("PUT", "/api/plugins/com", api.PluginInstall{PluginSettings: next}, h)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT to official: %d %s", resp.StatusCode, respText(t, resp))
	}
	cur, _ = s.settings.Plugin("com")
	next = cur
	next.Source, next.Subpath, next.Ref = src["com"], "", ""
	h["If-Match"] = `"` + cur.Revision + `"`
	resp = b.do("PUT", "/api/plugins/com", api.PluginInstall{PluginSettings: next}, h)
	if tag := confirmation(t, resp); tag != api.PluginCommunity {
		t.Fatalf("PUT to community: tag %q", tag)
	}
	// A PUT that keeps the source (the configuration only) needs nothing.
	next = cur
	next.Enabled = false
	resp = b.do("PUT", "/api/plugins/com", api.PluginInstall{PluginSettings: next}, h)
	wantStatus(t, resp.StatusCode, http.StatusOK)
}
