package skillsets

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// recordedAnswer has the shape skills.sh's search answers with.
const recordedAnswer = `{"query":"grill","searchType":"fuzzy","searchVersion":"algolia","skills":[
{"id":"mattpocock/skills/grill-me","source":"mattpocock/skills","skillId":"grill-me","name":"grill-me","installs":1565244},
{"id":"vinvcn/mattpocock-skills-zh-cn/grilling","source":"vinvcn/mattpocock-skills-zh-cn","skillId":"grilling","name":"grilling","installs":4794}]}`

// fakeSkillsSh answers every search with status and body and records the
// requests.
func fakeSkillsSh(t *testing.T, status int, body string) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var reqs []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r.Clone(context.Background()))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

func TestSearchReadsTheAnswer(t *testing.T) {
	srv, reqs := fakeSkillsSh(t, http.StatusOK, recordedAnswer)
	got, err := NewSearcher(SearchOptions{Base: srv.URL}).Search(context.Background(), "  grill\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(got), got)
	}
	g := got[0]
	if g.ID != "mattpocock/skills/grill-me" || g.Name != "grill-me" || g.SkillID != "grill-me" || g.Source != "mattpocock/skills" || g.Installs != 1565244 ||
		g.Repository != "https://github.com/mattpocock/skills.git" || g.URL != "https://skills.sh/mattpocock/skills/grill-me" {
		t.Errorf("first result = %+v", g)
	}
	if len(*reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(*reqs))
	}
	r := (*reqs)[0]
	if r.URL.Path != "/api/search" || r.URL.RawQuery != "q=grill" {
		t.Errorf("request %s?%s, want /api/search?q=grill (nothing but the text)", r.URL.Path, r.URL.RawQuery)
	}
	if ua := r.Header.Get("User-Agent"); ua != "omo" {
		t.Errorf("User-Agent %q, want the generic omo", ua)
	}
	if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
		t.Errorf("the request carried credentials: %v", r.Header)
	}
}

func TestSearchEmptyAnswer(t *testing.T) {
	srv, _ := fakeSkillsSh(t, http.StatusOK, `{"query":"zzz","skills":[]}`)
	got, err := NewSearcher(SearchOptions{Base: srv.URL}).Search(context.Background(), "zzz")
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %v, %v; want an empty list", got, err)
	}
}

func TestSearchRefusesWhatItCannotRead(t *testing.T) {
	for name, c := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"garbage":    {http.StatusOK, `<html>hello</html>`, "cannot read"},
		"no skills":  {http.StatusOK, `{"query":"x"}`, "cannot read"},
		"wrong type": {http.StatusOK, `{"skills":"many"}`, "cannot read"},
		"status":     {http.StatusInternalServerError, `{}`, "answered 500"},
		"limited":    {http.StatusTooManyRequests, `{}`, "answered 429"},
		"oversized":  {http.StatusOK, `{"skills":[` + strings.Repeat(`{"id":"a/b/c","source":"a/b","skillId":"c","name":"c","installs":1},`, 10000) + `{}]}`, "larger than"},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := fakeSkillsSh(t, c.status, c.body)
			_, err := NewSearcher(SearchOptions{Base: srv.URL}).Search(context.Background(), "grill")
			if !errors.Is(err, ErrSearch) || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want ErrSearch saying %q", err, c.want)
			}
			if !strings.HasPrefix(err.Error(), "skills.sh could not be reached: ") {
				t.Errorf("err = %q, want it to start with the page's words", err)
			}
		})
	}
}

func TestSearchTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	start := time.Now()
	_, err := NewSearcher(SearchOptions{Base: srv.URL, Timeout: 100 * time.Millisecond}).Search(context.Background(), "grill")
	if !errors.Is(err, ErrSearch) || !strings.Contains(err.Error(), "no answer within 100ms") {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("the search took %s", time.Since(start))
	}
}

func TestSearchUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	_, err := NewSearcher(SearchOptions{Base: base}).Search(context.Background(), "grill")
	if !errors.Is(err, ErrSearch) || !strings.Contains(err.Error(), "connection failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestSearchHostileStrings(t *testing.T) {
	long := strings.Repeat("n", 500)
	body := `{"skills":[
{"source":"evil/repo","skillId":"ok","name":"<img src=x onerror=alert(1)>‮\u0007 two\nlines","installs":-5},
{"source":"evil/repo","skillId":"long","name":"` + long + `","installs":1e300},
{"source":"evil/repo","skillId":"","name":"no id","installs":1},
{"source":"evil/repo","skillId":"../../etc","name":"dots","installs":1},
{"source":"mintlify.com","skillId":"mintlify","name":"well-known","installs":1},
{"source":"a/b/c","skillId":"x","name":"three parts","installs":1},
{"source":"-x/repo","skillId":"x","name":"dash owner","installs":1},
{"source":"x/..","skillId":"x","name":"dot repo","installs":1},
{"source":"x/r.git","skillId":"x","name":"git suffix","installs":1},
{"source":"evil/repo","skillId":"x y","name":"space id","installs":1},
{"source":"evil/repo","skillId":"OK","name":"duplicate in other case","installs":1},
{"source":"evil/repo","skillId":"empty-name","name":"","installs":1}]}`
	srv, _ := fakeSkillsSh(t, http.StatusOK, body)
	got, err := NewSearcher(SearchOptions{Base: srv.URL}).Search(context.Background(), "evil")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, g := range got {
		ids = append(ids, g.SkillID)
	}
	if strings.Join(ids, ",") != "ok,long,empty-name" {
		t.Fatalf("kept %v, want ok,long,empty-name", ids)
	}
	if got[0].Name != "<img src=x onerror=alert(1)> two lines" || got[0].Installs != 0 {
		t.Errorf("first = %+v; want one line without hidden characters, kept as text, installs 0", got[0])
	}
	if n := len([]rune(got[1].Name)); n != maxResultName || got[1].Installs != 0 {
		t.Errorf("long name has %d characters (want %d), installs %d", n, maxResultName, got[1].Installs)
	}
	if got[2].Name != "empty-name" {
		t.Errorf("a result without a name is named %q, want its id", got[2].Name)
	}
}

func TestSearchKeepsAtMostMaxResults(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"skills":[`)
	for i := range MaxResults + 10 {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"source":"o/r","skillId":"s` + strings.Repeat("x", i) + `","name":"n","installs":1}`)
	}
	b.WriteString(`]}`)
	srv, _ := fakeSkillsSh(t, http.StatusOK, b.String())
	got, err := NewSearcher(SearchOptions{Base: srv.URL}).Search(context.Background(), "many")
	if err != nil || len(got) != MaxResults {
		t.Fatalf("got %d results, %v; want %d", len(got), err, MaxResults)
	}
}

func TestSearchText(t *testing.T) {
	for q, ok := range map[string]bool{"": false, " a ": false, "ab": true, strings.Repeat("a", MaxSearch): true, strings.Repeat("a", MaxSearch+1): false, "​​a": false} {
		if _, err := SearchText(q); (err == nil) != ok {
			t.Errorf("SearchText(%q) err = %v, want ok %v", q, err, ok)
		}
	}
}

func TestSearchCachesTheSameTextForAMinute(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_, _ = w.Write([]byte(recordedAnswer))
	}))
	t.Cleanup(srv.Close)
	now := time.Unix(1000, 0)
	s := NewSearcher(SearchOptions{Base: srv.URL, Now: func() time.Time { return now }})
	ctx := context.Background()
	for _, q := range []string{"grill", "Grill", " grill "} {
		if _, err := s.Search(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if n.Load() != 1 {
		t.Fatalf("%d requests for the same text, want 1", n.Load())
	}
	if _, err := s.Search(ctx, "other"); err != nil || n.Load() != 2 {
		t.Fatalf("another text: %d requests, %v", n.Load(), err)
	}
	now = now.Add(searchFresh)
	if _, err := s.Search(ctx, "grill"); err != nil || n.Load() != 3 {
		t.Fatalf("after a minute: %d requests, %v; want a new one", n.Load(), err)
	}
}

func TestSearchDoesNotCacheFailures(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(recordedAnswer))
	}))
	t.Cleanup(srv.Close)
	s := NewSearcher(SearchOptions{Base: srv.URL})
	if _, err := s.Search(context.Background(), "grill"); err == nil {
		t.Fatal("the first search should fail")
	}
	if got, err := s.Search(context.Background(), "grill"); err != nil || len(got) != 2 {
		t.Fatalf("the second search: %v, %v", got, err)
	}
}

// writeSkill writes dir/SKILL.md (below root) with the front matter name.
func writeSkill(t *testing.T, root, dir, name string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(dir))
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "# skill\n"
	if name != "" {
		text = "---\nname: " + name + "\ndescription: d\n---\n" + text
	}
	if err := os.WriteFile(filepath.Join(full, skillFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFolderOf(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "skills/productivity/grill-me", "grill-me")
	writeSkill(t, root, "skills/productivity/grilling", "grilling")
	writeSkill(t, root, "copies/grill-me", "Grill Me Copy")
	writeSkill(t, root, "other/react_native", "React Native")
	writeSkill(t, root, "bare/plain-folder", "")
	writeSkill(t, root, ".hidden/secret", "secret")
	for _, c := range []struct {
		id, name, want string
		ok             bool
	}{
		{"grill-me", "grill-me", "skills/productivity/grill-me", true},
		{"grilling", "grilling", "skills/productivity/grilling", true},
		{"react-native", "React Native", "other/react_native", true},
		{"plain-folder", "plain-folder", "bare/plain-folder", true},
		{"secret", "secret", "", false},
		{"missing", "missing", "", false},
	} {
		got, ok := FolderOf(root, c.id, c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("FolderOf(%q, %q) = %q, %v; want %q, %v", c.id, c.name, got, ok, c.want, c.ok)
		}
	}
}

func TestFolderOfTheRootSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, ".", "solo")
	if got, ok := FolderOf(root, "solo", "solo"); got != "" || !ok {
		t.Fatalf("FolderOf = %q, %v; want the root", got, ok)
	}
}

func TestFolderOfFollowsNoLinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeSkill(t, outside, "away", "away")
	if err := os.Symlink(filepath.Join(outside, "away"), filepath.Join(root, "away")); err != nil {
		t.Skip("no links here:", err)
	}
	if got, ok := FolderOf(root, "away", "away"); ok {
		t.Fatalf("FolderOf followed a link to %q", got)
	}
}
