package discover

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// mkRepo creates rel below root as a folder holding a .git folder.
func mkRepo(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel), ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func mkDir(t *testing.T, root, rel string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o700); err != nil {
		t.Fatal(err)
	}
}

// tempRoot is a temporary folder with its links resolved, so Repo.Path can
// be compared directly.
func tempRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func rels(res Result) []string {
	out := []string{}
	for _, r := range res.Repos {
		out = append(out, r.Rel)
	}
	return out
}

func TestWalkFindsReposWithinDepth(t *testing.T) {
	root := tempRoot(t)
	mkRepo(t, root, "a/one")
	mkRepo(t, root, "b/two")
	mkRepo(t, root, "c")
	mkDir(t, root, "d/e/f")

	res := Walk(context.Background(), Root{Path: root, MaxDepth: 2}, Options{})
	if res.State != StateOK || res.Message != "" {
		t.Fatalf("state = %q %q", res.State, res.Message)
	}
	if got, want := rels(res), []string{"a/one", "b/two", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("repos = %v, want %v", got, want)
	}
	if res.Repos[0].Path != filepath.Join(root, "a", "one") {
		t.Fatalf("path = %q", res.Repos[0].Path)
	}
	// root, a, b, c, d, a/one, b/two, d/e
	if res.Visited != 8 {
		t.Fatalf("visited = %d, want 8", res.Visited)
	}

	res = Walk(context.Background(), Root{Path: root, MaxDepth: 1}, Options{})
	if got, want := rels(res), []string{"c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("depth 1 repos = %v, want %v", got, want)
	}
}

func TestWalkDefaultAndCappedDepth(t *testing.T) {
	root := tempRoot(t)
	mkRepo(t, root, "1/2/3/4")
	mkRepo(t, root, "a/b/c/d/e")
	res := Walk(context.Background(), Root{Path: root}, Options{})
	if got, want := rels(res), []string{"1/2/3/4"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("default depth repos = %v, want %v", got, want)
	}
	deep := strings.Repeat("x/", 17) + "r"
	mkRepo(t, root, deep)
	res = Walk(context.Background(), Root{Path: root, MaxDepth: 99}, Options{})
	for _, r := range res.Repos {
		if r.Rel == deep {
			t.Fatal("found a repository deeper than the maximum depth")
		}
	}
}

func TestWalkRootIsRepo(t *testing.T) {
	root := tempRoot(t)
	mkRepo(t, root, ".")
	mkRepo(t, root, "inner")
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 4}, Options{})
	if res.State != StateOK || len(res.Repos) != 1 || res.Repos[0] != (Repo{Path: root, Rel: ""}) {
		t.Fatalf("result = %+v", res)
	}
	if res.Visited != 1 {
		t.Fatalf("visited = %d", res.Visited)
	}
}

func TestWalkDoesNotDescendIntoRepos(t *testing.T) {
	root := tempRoot(t)
	mkRepo(t, root, "repo")
	mkRepo(t, root, "repo/inner")
	mkRepo(t, root, "repo/sub/deeper")
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 8}, Options{})
	if got, want := rels(res), []string{"repo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("repos = %v, want %v", got, want)
	}
}

func TestWalkSkipsWorktreesAndSubmodules(t *testing.T) {
	root := tempRoot(t)
	// A linked worktree or a submodule: .git is a file.
	mkDir(t, root, "wt")
	if err := os.WriteFile(filepath.Join(root, "wt", ".git"), []byte("gitdir: /elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mkRepo(t, root, "wt/nested")
	mkRepo(t, root, "real")
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 4}, Options{})
	if got, want := rels(res), []string{"real"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("repos = %v, want %v", got, want)
	}
	// The root itself as a worktree: nothing.
	wt := filepath.Join(root, "wt")
	res = Walk(context.Background(), Root{Path: wt, MaxDepth: 4}, Options{})
	if res.State != StateOK || len(res.Repos) != 0 {
		t.Fatalf("worktree root = %+v", res)
	}
}

func TestWalkIgnorePatterns(t *testing.T) {
	root := tempRoot(t)
	mkRepo(t, root, "node_modules/x")
	mkRepo(t, root, "app/node_modules/y")
	mkRepo(t, root, ".cache/z")
	mkRepo(t, root, "vendor/lib")
	mkRepo(t, root, "vendor")
	mkRepo(t, root, "other/vendor/kept")
	mkRepo(t, root, "a/one")
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 4, Ignore: []string{"node_modules", ".*", "vendor/*"}}, Options{})
	// "vendor" itself is a repository and not ignored ("vendor/*" matches
	// only below it); "other/vendor/kept" is not matched by the slash
	// pattern, which is anchored at the root.
	if got, want := rels(res), []string{"a/one", "other/vendor/kept", "vendor"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("repos = %v, want %v", got, want)
	}

	if !Ignored("a/node_modules", "node_modules", []string{"node_modules"}) {
		t.Error("base name pattern did not match")
	}
	if Ignored("a/vendor/x", "x", []string{"vendor/*"}) {
		t.Error("slash pattern matched below another folder")
	}
	if Ignored("x", "x", []string{"["}) {
		t.Error("invalid pattern matched")
	}

	for _, ok := range []string{"node_modules", ".*", "vendor/*", "build-?", "[a-c]*"} {
		if err := ValidPattern(ok); err != nil {
			t.Errorf("ValidPattern(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "**", "a/**/b", "[", "a[", "/abs", "trail/", `\`} {
		if err := ValidPattern(bad); err == nil {
			t.Errorf("ValidPattern(%q) accepted", bad)
		}
	}
}

func TestReachable(t *testing.T) {
	r := Root{Path: "/ws", MaxDepth: 2, Ignore: []string{"node_modules", "vendor/*"}}
	for rel, want := range map[string]bool{
		"":                  true,
		"a":                 true,
		"a/b":               true,
		"a/b/c":             false, // too deep
		"node_modules":      false,
		"a/node_modules":    false,
		"node_modules/x":    false, // below an ignored folder
		"vendor":            true,
		"vendor/lib":        false,
		"../outside":        false,
		"a/../b":            false,
		"/abs":              false,
		"other/vendor":      true,
		"other/vendor/deep": false, // too deep
	} {
		if got := Reachable(r, rel); got != want {
			t.Errorf("Reachable(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestWalkSkipsOmoHome(t *testing.T) {
	root := tempRoot(t)
	home := filepath.Join(root, "omo-home")
	mkRepo(t, home, "worktrees/demo/1")
	mkRepo(t, home, "repo")
	mkRepo(t, root, "kept")
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 6}, Options{Skip: []string{home + string(filepath.Separator)}})
	if got, want := rels(res), []string{"kept"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("repos = %v, want %v", got, want)
	}
	// A root inside a skipped folder is not searched at all.
	res = Walk(context.Background(), Root{Path: filepath.Join(home, "worktrees"), MaxDepth: 6}, Options{Skip: []string{home}})
	if res.State != StateFailed || len(res.Repos) != 0 || res.Visited != 0 {
		t.Fatalf("root inside skip = %+v", res)
	}
	// A skipped root itself.
	res = Walk(context.Background(), Root{Path: home, MaxDepth: 6}, Options{Skip: []string{home}})
	if res.State != StateFailed || len(res.Repos) != 0 {
		t.Fatalf("skipped root = %+v", res)
	}
}

func TestWalkBounds(t *testing.T) {
	root := tempRoot(t)
	for i := range 30 {
		mkRepo(t, root, filepath.ToSlash(filepath.Join("d"+string(rune('a'+i%26))+string(rune('a'+i/26)), "r")))
	}
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 4}, Options{MaxVisited: 10})
	if res.State != StateCut || res.Message == "" {
		t.Fatalf("state = %q %q", res.State, res.Message)
	}
	if res.Visited > 10 {
		t.Fatalf("visited = %d, want at most 10", res.Visited)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res = Walk(ctx, Root{Path: root, MaxDepth: 4}, Options{})
	if res.State != StateCut {
		t.Fatalf("cancelled: state = %q %q", res.State, res.Message)
	}

	// The deadline stops a walk whose reads are slow.
	slow := func(dir string) ([]os.DirEntry, error) {
		time.Sleep(20 * time.Millisecond)
		return os.ReadDir(dir)
	}
	res = Walk(context.Background(), Root{Path: root, MaxDepth: 4}, Options{Deadline: 50 * time.Millisecond, ReadDir: slow})
	if res.State != StateCut || !strings.Contains(res.Message, "time limit") {
		t.Fatalf("deadline: state = %q %q", res.State, res.Message)
	}
}

func TestWalkHungFolder(t *testing.T) {
	root := tempRoot(t)
	mkRepo(t, root, "a/one")
	mkRepo(t, root, "hung/inside")
	mkRepo(t, root, "z/two")
	hung := filepath.Join(root, "hung")
	release := make(chan struct{})
	defer close(release)
	read := func(dir string) ([]os.DirEntry, error) {
		if dir == hung {
			<-release
		}
		return os.ReadDir(dir)
	}
	start := time.Now()
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 4}, Options{ReadTimeout: 50 * time.Millisecond, ReadDir: read})
	if time.Since(start) > 5*time.Second {
		t.Fatal("walk waited for the hung folder")
	}
	if res.State != StateOK {
		t.Fatalf("state = %q %q", res.State, res.Message)
	}
	if got, want := rels(res), []string{"a/one", "z/two"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("repos = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(res.Skipped, []string{hung}) {
		t.Fatalf("skipped = %v", res.Skipped)
	}
}

func TestWalkSkippedListIsCapped(t *testing.T) {
	root := tempRoot(t)
	for i := range MaxSkipped + 5 {
		mkDir(t, root, "h"+string(rune('a'+i)))
	}
	release := make(chan struct{})
	defer close(release)
	read := func(dir string) ([]os.DirEntry, error) {
		if dir != root {
			<-release
		}
		return os.ReadDir(dir)
	}
	res := Walk(context.Background(), Root{Path: root, MaxDepth: 2}, Options{ReadTimeout: 10 * time.Millisecond, ReadDir: read})
	if len(res.Skipped) != MaxSkipped || res.Visited != MaxSkipped+6 {
		t.Fatalf("skipped = %d, visited = %d", len(res.Skipped), res.Visited)
	}
}

func TestWalkMissingRoot(t *testing.T) {
	root := tempRoot(t)
	res := Walk(context.Background(), Root{Path: filepath.Join(root, "absent"), MaxDepth: 4}, Options{})
	if res.State != StateMissing || res.Message == "" || len(res.Repos) != 0 {
		t.Fatalf("missing = %+v", res)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res = Walk(context.Background(), Root{Path: file, MaxDepth: 4}, Options{})
	if res.State != StateNotFolder {
		t.Fatalf("file root = %+v", res)
	}
	res = Walk(context.Background(), Root{Path: "relative", MaxDepth: 4}, Options{})
	if res.State != StateFailed {
		t.Fatalf("relative root = %+v", res)
	}
	if res.Repos == nil || res.Skipped == nil {
		t.Fatal("lists must be empty, not nil")
	}
}
