package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// listHost serves additional plugin lists by path; body changes what a path
// answers, hits counts the requests.
type listHost struct {
	srv   *httptest.Server
	mu    sync.Mutex
	files map[string]string
	hits  atomic.Int32
}

func (h *listHost) set(path, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.files[path] = text
}

func newListHost(t *testing.T, files map[string]string) *listHost {
	t.Helper()
	h := &listHost{files: files}
	release := make(chan struct{})
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits.Add(1)
		if r.URL.Path == "/slow.yaml" {
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		h.mu.Lock()
		text, ok := h.files[r.URL.Path]
		h.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(text))
	}))
	t.Cleanup(func() { close(release); h.srv.Close() })
	return h
}

func (h *listHost) url(path string) string { return h.srv.URL + path }

// setLists writes plugin_lists.
func setLists(t *testing.T, s *Server, lists ...string) {
	t.Helper()
	if _, err := s.settings.UpdateGeneral("", func(g *settings.General) error {
		g.PluginLists = lists
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPluginCatalogWithAdditionalLists(t *testing.T) {
	s, b, r, src := listServer(t)
	third := pluginRepo(t, recordManifest("third", plugins.EventTicketMoved))
	host := newListHost(t, map[string]string{
		"/good.yaml": `plugins:
  - name: third
    tag: official
    description: Claims to be official.
    source: ` + third + `
  - name: off
    tag: official
    description: Takes the name of omo's official plugin.
    source: ` + third + `
    path: x
  - name: relative
    tag: community
    description: Has no source.
    path: plugins/relative
  - name: copy-of-rec
    tag: recommended
    description: Same place as omo's recommended plugin.
    source: ` + src["rec"] + `
`,
		"/broken.yaml": "plugins: [\n",
	})
	good, broken, missing := host.url("/good.yaml"), host.url("/broken.yaml"), host.url("/missing.yaml")
	setLists(t, s, broken, good, missing)

	got := readCatalogRoute(t, s, b, "")
	if !got.Available || len(got.Lists) != 3 {
		t.Fatalf("catalog %+v", got)
	}
	origin := strings.TrimPrefix(host.srv.URL, "http://")
	var names []string
	for _, p := range got.Plugins {
		names = append(names, p.Name+"/"+p.Tag+"/"+p.Origin)
	}
	want := []string{"off/official/", "rec/recommended/", "com/community/", "third/community/" + origin}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("plugins %v, want %v", names, want)
	}
	if p := got.Plugins[3]; p.List != good || p.Source != third {
		t.Fatalf("third %+v", p)
	}
	// omo's own official plugin stays as it was.
	if p := got.Plugins[0]; p.Source != r.bare || p.List != "" {
		t.Fatalf("off %+v", p)
	}

	st := map[string]api.PluginListState{}
	for _, l := range got.Lists {
		st[l.URL] = l
	}
	if l := st[broken]; l.Available || !strings.Contains(l.Message, "not valid") || l.Origin != origin || l.FetchedAt == nil {
		t.Fatalf("broken list %+v", l)
	}
	if l := st[missing]; l.Available || !strings.Contains(l.Message, "404") {
		t.Fatalf("missing list %+v", l)
	}
	if l := st[good]; !l.Available || l.Message != "" || len(l.Notes) != 3 ||
		!strings.Contains(l.Notes[0], "source is missing") ||
		!strings.Contains(l.Notes[1], `"off" is not shown: omo's own list has a plugin of this name`) ||
		!strings.Contains(l.Notes[2], `"copy-of-rec" is not shown: omo's own list has a plugin with the same source`) {
		t.Fatalf("good list %+v", l)
	}
	if got.Lists[0].URL != broken || got.Lists[1].URL != good || got.Lists[2].URL != missing {
		t.Fatalf("lists out of order: %+v", got.Lists)
	}

	// Kept for a while; refresh reads every list again.
	hits := host.hits.Load()
	readCatalogRoute(t, s, b, "")
	if host.hits.Load() != hits {
		t.Fatal("the lists were fetched again within their time")
	}
	host.set("/broken.yaml", "plugins: []\n")
	fresh := readCatalogRoute(t, s, b, "?refresh=true")
	if !fresh.Lists[0].Available {
		t.Fatalf("refresh: %+v", fresh.Lists[0])
	}

	// omo's own list unreadable: the additional lists still answer.
	s.opts.PluginCatalog.Source = t.TempDir() + "/gone.git"
	without := readCatalogRoute(t, s, b, "?refresh=true")
	if without.Available || len(without.Lists) != 3 || !without.Lists[1].Available {
		t.Fatalf("without omo's list: %+v", without)
	}
	// Then nothing of omo's list is known, and the entry named like omo's
	// official plugin is shown, attributed and as community.
	for _, p := range without.Plugins {
		if p.List == "" || p.Tag != api.PluginCommunity {
			t.Fatalf("plugin %+v", p)
		}
	}

	// A removed list is not answered any more.
	setLists(t, s, good)
	if one := readCatalogRoute(t, s, b, ""); len(one.Lists) != 1 || one.Lists[0].URL != good {
		t.Fatalf("after removal: %+v", one.Lists)
	}
}

func TestPluginCatalogSlowListDoesNotHoldTheOthers(t *testing.T) {
	s, b, _, _ := listServer(t)
	slowStarted, goodServed := make(chan struct{}), make(chan struct{})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow.yaml" {
			close(slowStarted)
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte("plugins: []\n"))
		close(goodServed)
	}))
	t.Cleanup(host.Close)
	slow := host.URL + "/slow.yaml"
	setLists(t, s, slow, host.URL+"/good.yaml")
	cutSlow := make(chan context.CancelFunc, 1)
	s.plugins.listContext = func(parent context.Context, address string) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(parent)
		if address == slow {
			cutSlow <- cancel
		}
		return ctx, cancel
	}
	answered := make(chan api.PluginCatalog, 1)
	go func() { answered <- readCatalogRoute(t, s, b, "") }()
	cancel := receive(t, cutSlow, "the slow list's deadline")
	defer cancel()
	receive(t, slowStarted, "the slow HTTP fetch")
	// The good list must answer while the first is still held. Expire the
	// held fetch only after this barrier, without a deadline on good work.
	receive(t, goodServed, "the good list while the slow list is held")
	cancel()
	got := receive(t, answered, "the catalog after the slow list expires")
	if !got.Available || len(got.Lists) != 2 || got.Lists[0].Available || !strings.Contains(got.Lists[0].Message, "took too long") || !got.Lists[1].Available {
		t.Fatalf("catalog %+v", got)
	}
}

// TestPluginInstallFromAdditionalList: a plugin of an additional list that
// claims to be official needs the community acknowledgement; the server
// decides that, not the page.
func TestPluginInstallFromAdditionalList(t *testing.T) {
	s, b, _, _ := listServer(t)
	third := pluginRepo(t, recordManifest("third", plugins.EventTicketMoved))
	host := newListHost(t, map[string]string{"/list.yaml": `plugins:
  - name: third
    tag: official
    description: Claims to be official.
    source: ` + third + `
`})
	setLists(t, s, host.url("/list.yaml"))
	if got := readCatalogRoute(t, s, b, ""); len(got.Plugins) != 4 || got.Plugins[3].Tag != api.PluginCommunity {
		t.Fatalf("catalog %+v", got.Plugins)
	}
	for _, ack := range []string{"", api.PluginRecommended} {
		resp := asUser(b, s, "POST", "/api/plugins", install("third", third, "", "", ack))
		if tag := confirmation(t, resp); tag != api.PluginCommunity {
			t.Fatalf("with %q: tag %q", ack, tag)
		}
	}
	if _, ok := s.settings.Plugin("third"); ok {
		t.Fatal("a refused install wrote settings")
	}
	resp := asUser(b, s, "POST", "/api/plugins", install("third", third, "", "", api.PluginCommunity))
	wantStatus(t, resp.StatusCode, http.StatusCreated)
}

func TestPluginListsSettingRefusesOtherSchemes(t *testing.T) {
	s, b, _, _ := listServer(t)
	for _, addr := range []string{"file:///etc/passwd", "ftp://example.org/x", "/home/me/list.yaml", "git@example.org:a/b.git"} {
		h := bearer(s)
		resp := b.do("PATCH", "/api/settings", map[string]any{"plugin_lists": []string{"https://ok.example/list.yaml", addr}}, h)
		wantStatus(t, resp.StatusCode, http.StatusUnprocessableEntity)
		if text := respText(t, resp); !strings.Contains(text, `"field":"plugin_lists.1"`) {
			t.Fatalf("%s: %s", addr, text)
		}
	}
	h := bearer(s)
	resp := b.do("PATCH", "/api/settings", map[string]any{"plugin_lists": []string{"https://ok.example/list.yaml"}}, h)
	wantStatus(t, resp.StatusCode, http.StatusOK)
	if got := s.settings.General().PluginLists; len(got) != 1 {
		t.Fatalf("stored %v", got)
	}
}
