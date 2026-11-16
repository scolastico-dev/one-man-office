package pluginmanager

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

var testLocation = CatalogLocation{Source: "https://example.invalid/omo.git", Ref: "release"}

func TestParseCatalogResolvesSources(t *testing.T) {
	raw := `
plugins:
  - name: pushover
    tag: official
    description: Push notifications.
    path: plugins/pushover/
  - name: ticket-log
    tag: recommended
    description: Logs tickets.
    source: https://example.org/me/ticket-log.git
    ref: v1.0.0
  - name: Notes
    tag: community
    description: "Notes: <b>not markup</b> & more"
    source: git@example.org:me/notes.git
    path: plugin
`
	got, err := ParseCatalog([]byte(raw), testLocation)
	if err != nil {
		t.Fatal(err)
	}
	want := []api.CatalogPlugin{
		{Name: "pushover", Description: "Push notifications.", Tag: api.PluginOfficial, Source: testLocation.Source, Ref: "release", Subpath: "plugins/pushover"},
		{Name: "ticket-log", Description: "Logs tickets.", Tag: api.PluginRecommended, Source: "https://example.org/me/ticket-log.git", Ref: "v1.0.0"},
		{Name: "Notes", Description: "Notes: <b>not markup</b> & more", Tag: api.PluginCommunity, Source: "git@example.org:me/notes.git", Subpath: "plugin"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	c := Catalog{Location: testLocation, Plugins: got}
	for _, tc := range []struct{ source, sub, ref, tag string }{
		{testLocation.Source, "plugins/pushover", "release", api.PluginOfficial},
		{testLocation.Source, "./plugins/pushover/", "release", api.PluginOfficial},
		{testLocation.Source, "plugins/pushover", "main", api.PluginCommunity},
		{testLocation.Source, "plugins/other", "release", api.PluginCommunity},
		{"https://example.invalid/omo", "plugins/pushover", "release", api.PluginCommunity},
		{"https://example.org/me/ticket-log.git", "", "v1.0.0", api.PluginRecommended},
		{"https://example.org/me/ticket-log.git", "", "", api.PluginCommunity},
		{"/somewhere/else", "", "", api.PluginCommunity},
		{testLocation.Source, "../x", "release", api.PluginCommunity},
	} {
		if got := c.TagOf(tc.source, tc.sub, tc.ref); got != tc.tag {
			t.Errorf("TagOf(%q, %q, %q) = %q, want %q", tc.source, tc.sub, tc.ref, got, tc.tag)
		}
	}
	if (Catalog{}).TagOf(testLocation.Source, "plugins/pushover", "release") != api.PluginCommunity {
		t.Error("an empty list made a source official")
	}
}

func TestParseCatalogIsStrict(t *testing.T) {
	entry := func(lines ...string) string { return "plugins:\n  - " + strings.Join(lines, "\n    ") + "\n" }
	official := func(extra ...string) string {
		return entry(append([]string{"name: p", "tag: official", "description: d", "path: plugins/p"}, extra...)...)
	}
	community := func(extra ...string) string {
		return entry(append([]string{"name: p", "tag: community", "description: d", "source: https://example.org/p.git"}, extra...)...)
	}
	many := "plugins:\n"
	for i := range MaxCatalogPlugins + 1 {
		many += "  - {name: p" + strconv.Itoa(i) + ", tag: community, description: d, source: /x/p" + strconv.Itoa(i) + "}\n"
	}
	cases := map[string]string{
		"empty":               "",
		"not a list":          "plugins: x\n",
		"unknown key":         official("homepage: https://x"),
		"unknown top key":     "plugins: []\nversion: 2\n",
		"two documents":       official() + "---\nplugins: []\n",
		"duplicate key":       entry("name: p", "name: q", "tag: official", "description: d", "path: x"),
		"unknown tag":         entry("name: p", "tag: trusted", "description: d", "source: https://example.org/p.git"),
		"no tag":              entry("name: p", "description: d", "source: https://example.org/p.git"),
		"bad name":            entry("name: ../p", "tag: official", "description: d", "path: x"),
		"dot name":            entry("name: .cache", "tag: official", "description: d", "path: x"),
		"no description":      entry("name: p", "tag: official", "path: x"),
		"long description":    entry("name: p", "tag: official", "description: "+strings.Repeat("d", MaxCatalogDescription+1), "path: x"),
		"two line text":       entry("name: p", "tag: official", `description: "a\nb"`, "path: x"),
		"hidden text":         entry("name: p", "tag: official", "description: \"a‮b\"", "path: x"),
		"official source":     official("source: https://example.org/p.git"),
		"official ref":        official("ref: main"),
		"official no path":    entry("name: p", "tag: official", "description: d"),
		"escaping path":       official()[:len(official())-len("plugins/p\n")] + "../p\n",
		"git path":            community("path: x/.git"),
		"no source":           entry("name: p", "tag: community", "description: d"),
		"file source":         community()[:len(community())-len("https://example.org/p.git\n")] + "file:///srv/p\n",
		"http source":         community()[:len(community())-len("https://example.org/p.git\n")] + "http://example.org/p.git\n",
		"ext source":          community()[:len(community())-len("https://example.org/p.git\n")] + "\"ext::sh -c x\"\n",
		"option ref":          community("ref: --upload-pack=x"),
		"duplicate name":      "plugins:\n  - {name: p, tag: community, description: d, source: /a}\n  - {name: P, tag: community, description: d, source: /b}\n",
		"duplicate place":     "plugins:\n  - {name: p, tag: community, description: d, source: /a}\n  - {name: q, tag: recommended, description: d, source: /a}\n",
		"too many":            many,
		"too large":           "# " + strings.Repeat("x", MaxCatalogBytes) + "\n" + official(),
		"not utf-8":           official() + "# \xff\n",
		"recursive alias":     "plugins: &a\n  - *a\n",
		"description as list": entry("name: p", "tag: official", "description: [a]", "path: x"),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if got, err := ParseCatalog([]byte(raw), testLocation); err == nil {
				t.Fatalf("accepted: %+v", got)
			}
		})
	}
	// The cases are wrong for one reason each: the base entries pass.
	for _, raw := range []string{official(), community(), community("ref: v1", "path: a/b")} {
		if _, err := ParseCatalog([]byte(raw), testLocation); err != nil {
			t.Fatalf("%q: %v", raw, err)
		}
	}
}

func TestAcknowledgements(t *testing.T) {
	for _, c := range []struct {
		tag, ack string
		ok       bool
	}{
		{api.PluginOfficial, "", true},
		{api.PluginOfficial, api.PluginCommunity, true},
		{api.PluginRecommended, "", false},
		{api.PluginRecommended, api.PluginRecommended, true},
		{api.PluginRecommended, api.PluginCommunity, true},
		{api.PluginCommunity, "", false},
		{api.PluginCommunity, api.PluginRecommended, false},
		{api.PluginCommunity, api.PluginCommunity, true},
		{"", api.PluginCommunity, true},
		{"", api.PluginRecommended, false},
	} {
		if got := Acknowledged(c.tag, c.ack); got != c.ok {
			t.Errorf("Acknowledged(%q, %q) = %v", c.tag, c.ack, got)
		}
	}
	for ack, ok := range map[string]bool{"": true, "recommended": true, "community": true, "official": false, "yes": false} {
		if ValidAcknowledgement(ack) != ok {
			t.Errorf("ValidAcknowledgement(%q) != %v", ack, ok)
		}
	}
}

func TestCatalogFromEnv(t *testing.T) {
	for in, want := range map[string]CatalogLocation{
		"":                       DefaultCatalog(),
		"/srv/omo":               {Source: "/srv/omo"},
		"/srv/omo#rework":        {Source: "/srv/omo", Ref: "rework"},
		"https://x.org/o.git#v1": {Source: "https://x.org/o.git", Ref: "v1"},
	} {
		got, err := CatalogFromEnv(in)
		if err != nil || got != want {
			t.Errorf("CatalogFromEnv(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"relative", "file:///srv/omo", "/srv/omo#-x", "http://x.org/o.git"} {
		if got, err := CatalogFromEnv(in); err == nil {
			t.Errorf("CatalogFromEnv(%q) = %+v", in, got)
		}
	}
	if d := DefaultCatalog(); d.Source != "https://github.com/scolastico-dev/one-man-office.git" || d.Ref != "release" {
		t.Fatalf("default = %+v", d)
	}
}

// TestFetchCatalogFromRepository reads the list from a local repository
// laid out as the release branch and installs its official plugins from
// where the list says.
func TestFetchCatalogFromRepository(t *testing.T) {
	e := newEnv(t)
	repo := shippedRepo(t)
	loc := CatalogLocation{Source: repo.bare, Ref: "main"}
	c, err := FetchCatalog(context.Background(), e.o, loc)
	if err != nil {
		t.Fatal(err)
	}
	if c.Location != loc || !commitID.MatchString(c.Commit) || len(c.Plugins) == 0 {
		t.Fatalf("catalog %+v", c)
	}
	for _, p := range c.Plugins {
		if p.Tag != api.PluginOfficial {
			continue
		}
		if p.Source != repo.bare || p.Ref != "main" {
			t.Fatalf("%s: official plugin from %s at %s", p.Name, p.Source, p.Ref)
		}
		got, _, err := e.install(api.PluginSettings{Name: p.Name, Source: p.Source, Subpath: p.Subpath, Ref: p.Ref})
		if err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
		if got.Subpath != p.Subpath || c.TagOf(got.Source, got.Subpath, got.Ref) != api.PluginOfficial {
			t.Fatalf("%s: installed %+v", p.Name, got)
		}
	}
	// The checkout is gone again.
	if _, err := os.Lstat(filepath.Join(e.root, catalogFolder)); !os.IsNotExist(err) {
		t.Fatalf("the list's checkout stayed: %v", err)
	}

	// A repository without the file, a broken file and no list at all.
	other := newUpstream(t)
	other.publish(map[string]string{"README.md": "x"})
	if _, err := FetchCatalog(context.Background(), e.o, CatalogLocation{Source: other.bare}); err == nil || !strings.Contains(err.Error(), CatalogFile) {
		t.Fatalf("no file: %v", err)
	}
	other.publish(map[string]string{CatalogFile: "plugins:\n  - name: x\n    tag: gold\n    description: d\n    source: /x\n"})
	if _, err := FetchCatalog(context.Background(), e.o, CatalogLocation{Source: other.bare}); err == nil || !strings.Contains(err.Error(), "tag") {
		t.Fatalf("broken file: %v", err)
	}
	if _, err := FetchCatalog(context.Background(), e.o, CatalogLocation{}); err == nil {
		t.Fatal("no list gave a list")
	}
	if _, err := FetchCatalog(context.Background(), e.o, CatalogLocation{Source: filepath.Join(t.TempDir(), "none.git")}); err == nil {
		t.Fatal("a missing repository gave a list")
	}
}

// TestShippedCatalog checks the repository's own list, the file the release
// workflow copies to the release branch: it parses, and every official
// plugin's folder is a plugin of that name whose manifest the runtime takes.
func TestShippedCatalog(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(shippedDir, "index.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	list, err := ParseCatalog(raw, DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range list {
		seen[p.Name] = true
		if p.Tag != api.PluginOfficial {
			continue
		}
		folder, ok := strings.CutPrefix(p.Subpath, "plugins/")
		if !ok {
			t.Fatalf("%s: official folder %q is not below plugins/", p.Name, p.Subpath)
		}
		m, err := plugins.ReadManifest(filepath.Join(shippedDir, folder))
		if err != nil {
			t.Fatalf("%s: %v", p.Name, err)
		}
		if m.Name != p.Name || m.Version == "" || m.Description == "" {
			t.Fatalf("%s: manifest name %q, version %q, description %q", p.Name, m.Name, m.Version, m.Description)
		}
	}
	for _, name := range []string{"pushover", "discord"} {
		if !seen[name] {
			t.Errorf("%s is not listed", name)
		}
	}
}
