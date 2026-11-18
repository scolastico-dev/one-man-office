package pluginmanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

// serveList serves body at every path; the handler records the User-Agent.
func serveList(t *testing.T, body string) (*httptest.Server, <-chan string) {
	t.Helper()
	agent := make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent <- r.UserAgent()
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, agent
}

const goodList = `plugins:
  - name: notes
    tag: community
    description: Notes <b>someone</b> keeps.
    source: https://example.org/me/notes.git
    ref: v1.0.0
  - name: claims-official
    tag: official
    description: Says it is official.
    source: https://example.org/me/claims.git
    path: plugin
  - name: claims-recommended
    tag: recommended
    description: Says it is recommended.
    source: git@example.org:me/rec.git
`

func TestFetchListReadsEntriesAsCommunity(t *testing.T) {
	srv, agent := serveList(t, goodList)
	addr := srv.URL + "/omo/list.yaml"
	l, err := FetchList(t.Context(), addr)
	if err != nil {
		t.Fatal(err)
	}
	origin := strings.TrimPrefix(srv.URL, "http://")
	if l.URL != addr || l.Origin != origin || len(l.Notes) != 0 {
		t.Fatalf("list %+v", l)
	}
	want := []api.CatalogPlugin{
		{Name: "notes", Description: "Notes <b>someone</b> keeps.", Tag: api.PluginCommunity, Source: "https://example.org/me/notes.git", Ref: "v1.0.0", List: addr, Origin: origin},
		{Name: "claims-official", Description: "Says it is official.", Tag: api.PluginCommunity, Source: "https://example.org/me/claims.git", Subpath: "plugin", List: addr, Origin: origin},
		{Name: "claims-recommended", Description: "Says it is recommended.", Tag: api.PluginCommunity, Source: "git@example.org:me/rec.git", List: addr, Origin: origin},
	}
	if !reflect.DeepEqual(l.Plugins, want) {
		t.Fatalf("plugins %+v\nwant %+v", l.Plugins, want)
	}
	if got := <-agent; got != listUserAgent || strings.Contains(got, "Go-http-client") {
		t.Fatalf("user agent %q", got)
	}
}

// TestAdditionalListNeverGrantsTrust is the guard: whatever an additional
// list claims, its plugins are community for TrustTag, for TagOf (the
// install routes) and for Acknowledged.
func TestAdditionalListNeverGrantsTrust(t *testing.T) {
	l, err := ParseList([]byte(goodList), "https://lists.example/omo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range l.Plugins {
		if p.Tag != api.PluginCommunity || TrustTag(p) != api.PluginCommunity {
			t.Fatalf("%s: tag %q, trust %q", p.Name, p.Tag, TrustTag(p))
		}
		// Even an entry whose stored tag says official.
		p.Tag = api.PluginOfficial
		if TrustTag(p) != api.PluginCommunity || Acknowledged(TrustTag(p), "") || Acknowledged(TrustTag(p), api.PluginRecommended) {
			t.Fatalf("%s: an additional list's official claim was trusted", p.Name)
		}
		c := Catalog{Plugins: []api.CatalogPlugin{p}}
		if got := c.TagOf(p.Source, p.Subpath, p.Ref); got != api.PluginCommunity {
			t.Fatalf("%s: TagOf = %q", p.Name, got)
		}
	}
	own := api.CatalogPlugin{Name: "pushover", Tag: api.PluginOfficial, Source: "https://example.invalid/omo.git", Ref: "release", Subpath: "plugins/pushover"}
	if TrustTag(own) != api.PluginOfficial {
		t.Fatal("omo's own official plugin lost its tag")
	}
}

func TestParseListNotesBadEntriesAndKeepsTheRest(t *testing.T) {
	raw := `plugins:
  - name: relative
    tag: official
    description: Only a path, as in omo's own list.
    path: plugins/relative
  - name: fine
    tag: community
    description: Fine.
    source: https://example.org/fine.git
  - name: "-bad name"
    tag: community
    description: Bad name.
    source: https://example.org/x.git
  - name: plain-http
    tag: community
    description: Plain http source.
    source: http://example.org/x.git
  - name: gold
    tag: gold
    description: Unknown tag.
    source: https://example.org/gold.git
  - name: FINE
    tag: community
    description: Same name in another case.
    source: https://example.org/other.git
  - name: same-place
    tag: community
    description: Same source as fine.
    source: https://example.org/fine.git
  - name: long
    tag: community
    description: "` + strings.Repeat("x", 201) + `"
    source: https://example.org/long.git
`
	l, err := ParseList([]byte(raw), "https://lists.example/omo.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Plugins) != 1 || l.Plugins[0].Name != "fine" {
		t.Fatalf("plugins %+v", l.Plugins)
	}
	wants := []string{
		"plugin 1 of the list is not shown: source is missing",
		"plugin 3 of the list is not shown: name",
		"plugin 4 of the list is not shown: source must be",
		"plugin 5 of the list is not shown: tag must be",
		`plugin 6 of the list is not shown: the name "FINE" is listed twice`,
		"plugin 7 of the list is not shown: its source, folder and ref are listed twice",
		"plugin 8 of the list is not shown: description must be",
	}
	if len(l.Notes) != len(wants) {
		t.Fatalf("notes %q", l.Notes)
	}
	for i, w := range wants {
		if !strings.HasPrefix(l.Notes[i], w) {
			t.Errorf("note %d = %q, want prefix %q", i, l.Notes[i], w)
		}
	}
}

func TestFetchListFailures(t *testing.T) {
	big := "plugins: []\n#" + strings.Repeat("x", MaxCatalogBytes)
	cases := map[string]struct {
		handler http.HandlerFunc
		want    string
	}{
		"invalid YAML": {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("plugins: [\n")) }, "the plugin list is not valid"},
		"unknown key":  {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("plugins: []\nextra: 1\n")) }, "the plugin list is not valid"},
		"HTML page":    {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html><body>hi</body></html>")) }, "the plugin list is not valid"},
		"oversized":    {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(big)) }, "larger than 64 KiB"},
		"oversized, announced": {func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "1000000")
			_, _ = w.Write([]byte(big))
		}, "larger than 64 KiB"},
		"not found":     {func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "<b>gone</b>", http.StatusNotFound) }, "answered 404 Not Found"},
		"redirect loop": {func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, r.URL.Path, http.StatusFound) }, "more than 5 redirects"},
		"redirect to another scheme": {func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "ftp://example.org/list.yaml", http.StatusFound)
		}, "redirected to an address that is not http:// or https://"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(c.handler)
			defer srv.Close()
			_, err := FetchList(t.Context(), srv.URL+"/list.yaml?token=secret")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "<b>") {
				t.Fatalf("the error repeats the address or the answer: %v", err)
			}
		})
	}
}

func TestFetchListFollowsAFewRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a":
			http.Redirect(w, r, "/b", http.StatusFound)
		case "/b":
			http.Redirect(w, r, "/list.yaml", http.StatusMovedPermanently)
		default:
			_, _ = w.Write([]byte(goodList))
		}
	}))
	defer srv.Close()
	l, err := FetchList(t.Context(), srv.URL+"/a")
	if err != nil || len(l.Plugins) != 3 || l.Plugins[0].List != srv.URL+"/a" {
		t.Fatalf("list %+v, err %v", l, err)
	}
}

func TestFetchListIsBoundedInTime(t *testing.T) {
	for _, tc := range []struct {
		name                string
		callerTimeout, want time.Duration
	}{
		{"own deadline", 0, 15 * time.Second},
		{"earlier caller deadline", 200 * time.Millisecond, 200 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				previous := listClient
				listClient = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
					<-r.Context().Done()
					return nil, r.Context().Err()
				})}
				t.Cleanup(func() { listClient = previous })
				ctx := t.Context()
				if tc.callerTimeout != 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.callerTimeout)
					defer cancel()
				}
				start := time.Now()
				_, err := FetchList(ctx, "https://example.invalid/slow.yaml")
				if err == nil || !strings.Contains(err.Error(), "took too long") {
					t.Fatalf("err = %v", err)
				}
				if elapsed := time.Since(start); elapsed != tc.want {
					t.Fatalf("deadline after %s of virtual time, want %s", elapsed, tc.want)
				}
			})
		})
	}
}

func TestFetchListRefusesOtherSchemes(t *testing.T) {
	for _, addr := range []string{"file:///etc/passwd", "ftp://example.org/x", "/etc/passwd", "git@example.org:a/b.git", "https://user:pw@example.org/x"} {
		if _, err := FetchList(t.Context(), addr); err == nil {
			t.Errorf("%s was fetched", addr)
		}
	}
}

func TestCombineKeepsOmosOwnEntries(t *testing.T) {
	own := []api.CatalogPlugin{
		{Name: "pushover", Tag: api.PluginOfficial, Source: "https://example.invalid/omo.git", Ref: "release", Subpath: "plugins/pushover"},
	}
	a, err := ParseList([]byte(`plugins:
  - name: Pushover
    tag: official
    description: Takes the name of an official plugin.
    source: https://evil.example/pushover.git
  - name: other
    tag: official
    description: Takes the place of an official plugin.
    source: https://example.invalid/omo.git
    ref: release
    path: plugins/pushover
  - name: shared
    tag: community
    description: In two lists.
    source: https://example.org/shared.git
`), "https://a.example/list.yaml")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseList([]byte(`plugins:
  - name: shared
    tag: community
    description: In two lists.
    source: https://example.org/shared-b.git
  - name: own-b
    tag: community
    description: Only here.
    source: https://example.org/b.git
`), "https://b.example/list.yaml")
	if err != nil {
		t.Fatal(err)
	}
	got, notes := Combine(own, []List{a, b})
	var names []string
	for _, p := range got {
		names = append(names, p.Name+"@"+p.Origin)
	}
	if want := []string{"pushover@", "shared@a.example", "own-b@b.example"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("plugins %v, want %v", names, want)
	}
	if len(notes) != 2 || len(notes[0]) != 2 || len(notes[1]) != 1 {
		t.Fatalf("notes %q", notes)
	}
	for _, c := range []struct {
		note string
		want string
	}{
		{notes[0][0], `"Pushover" is not shown: omo's own list has a plugin of this name`},
		{notes[0][1], `"other" is not shown: omo's own list has a plugin with the same source, folder and ref`},
		{notes[1][0], `"shared" is not shown: the list from a.example has a plugin of this name`},
	} {
		if c.note != c.want {
			t.Errorf("note %q, want %q", c.note, c.want)
		}
	}
	if len(a.Notes) != 0 || len(b.Notes) != 0 {
		t.Fatal("Combine changed the lists it was given")
	}
}

func TestFetchListRedirectDoesNotDiscloseSourceQuery(t *testing.T) {
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Referer() != "" {
			t.Error("source query disclosed through Referer")
		}
		w.Write([]byte(goodList))
	}))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, http.StatusFound) }))
	defer source.Close()
	if _, err := FetchList(t.Context(), source.URL+"/?token=source-secret"); err != nil {
		t.Fatal(err)
	}
}
