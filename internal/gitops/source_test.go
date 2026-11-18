package gitops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidSourceURL(t *testing.T) {
	ok := []string{
		"https://github.com/obra/superpowers.git",
		"https://user:token@example.com/o/r.git",
		"ssh://git@example.com/o/r.git",
		"ssh://example.com:2222/o/r",
		"git@github.com:o/r.git",
		"example.com:o/r",
		"git@[::1]:o/r",
	}
	if filepath.Separator == '/' {
		ok = append(ok, "/abs/path", "/abs/my plugins/repo")
	} else {
		ok = append(ok, `C:\abs\path`)
	}
	for _, u := range ok {
		if err := ValidSourceURL(u); err != nil {
			t.Errorf("ValidSourceURL(%q) = %v, want nil", u, err)
		}
	}
	bad := []string{
		"", "ext::sh -c touch% /tmp/x", "fd::3", "foo::bar", "foo+bar::baz", "a.b-c::x",
		"file:///x", "git://h/x", "http://h/x", "HTTPS://h/x", "ftp://h/x",
		"-oProxyCommand=x", "--upload-pack=x", "-u x",
		"https://h/\u202e", "https://h/x\n", "https://h/x\x00", "https://h/\u200bx",
		"ssh://-oProxyCommand=x/r", "ssh://-u@h/r", "-x@h:r", "u@-h:r",
		"relative/path", "./x", "../x", "h:", "https:///nohost",
	}
	for _, u := range bad {
		err := ValidSourceURL(u)
		if !errors.Is(err, ErrBadSource) {
			t.Errorf("ValidSourceURL(%q) = %v, want ErrBadSource", u, err)
		}
	}
	err := ValidSourceURL("http://user:SECRET@h/x")
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "user") {
		t.Errorf("the refusal repeats the credentials: %v", err)
	}
}

func TestValidRef(t *testing.T) {
	for _, r := range []string{"", "main", "v1.2.0", "feature/x", "omo/1-x", "a-b_c", "0123456789abcdef0123456789abcdef01234567"} {
		if err := ValidRef(r); err != nil {
			t.Errorf("ValidRef(%q) = %v, want nil", r, err)
		}
	}
	for _, r := range []string{
		"-x", "--upload-pack=x", "+main", "a..b", "@{", "a@{1}", "@", "a b", "x.lock", "a/x.lock/b", ".x", "a/.x",
		"a:b", "a^", "a~1", "a?", "a*", "a[", `a\b`, "a/", "/a", "a//b", "a.", "a\x00", "a\tb", "a\u202eb", "a\u200bb",
		"a\x7f", strings.Repeat("a", 256),
	} {
		if err := ValidRef(r); !errors.Is(err, ErrBadRef) {
			t.Errorf("ValidRef(%q) = %v, want ErrBadRef", r, err)
		}
	}
}

func TestHideCredentials(t *testing.T) {
	cases := map[string]string{
		"https://u:SECRET@h/x":                       "https://h/x",
		"fatal: https://u:SECRET@h:443/x not found":  "fatal: https://h:443/x not found",
		"to 'https://tok@h/x' and ssh://git@h/y":     "to 'https://h/x' and ssh://h/y",
		"https://u:p@ss@h/x":                         "https://h/x",
		"two https://a:1@h/x https://b:2@k/y":        "two https://h/x https://k/y",
		"no url here: git@github.com:o/r.git, a@b.c": "no url here: git@github.com:o/r.git, a@b.c",
	}
	for in, want := range cases {
		if got := HideCredentials(in); got != want {
			t.Errorf("HideCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}

// sourceRepo creates a repository with one commit holding files and
// returns its folder and the commit.
func sourceRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	tg(t, dir, "init", "-q", "-b", "main")
	for p, c := range files {
		write(t, filepath.Join(dir, p), c)
	}
	tg(t, dir, "add", "-A")
	tg(t, dir, "commit", "-q", "-m", "one")
	return dir, rev(t, dir, "HEAD")
}

// noSiblings fails when the folder holding dir has anything but dir.
func noSiblings(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(dir) {
			t.Errorf("left behind next to the checkout: %s", e.Name())
		}
	}
}

func TestFetchSourceSwaps(t *testing.T) {
	g := testGit(t)
	src, first := sourceRepo(t, map[string]string{"a.txt": "one\n"})
	dir := filepath.Join(t.TempDir(), "skills", "set")
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	got, err := g.FetchSource(ctx, src, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != first || read(t, filepath.Join(dir, "a.txt")) != "one\n" {
		t.Fatalf("first fetch: commit %s, want %s", got, first)
	}
	if c, err := g.SourceCommit(ctx, dir); err != nil || c != first {
		t.Fatalf("SourceCommit = %s, %v; want %s", c, err, first)
	}
	write(t, filepath.Join(dir, "stale.txt"), "left by someone\n")

	write(t, filepath.Join(src, "a.txt"), "two\n")
	tg(t, src, "commit", "-q", "-am", "two")
	second := rev(t, src, "HEAD")
	got, err = g.FetchSource(ctx, src, "main", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != second || read(t, filepath.Join(dir, "a.txt")) != "two\n" {
		t.Fatalf("second fetch: commit %s, want %s", got, second)
	}
	if _, err := os.Stat(filepath.Join(dir, "stale.txt")); err == nil {
		t.Error("the old checkout's files are still there")
	}
	if c, err := g.SourceCommit(ctx, dir); err != nil || c != second {
		t.Fatalf("SourceCommit = %s, %v; want %s", c, err, second)
	}
	noSiblings(t, dir)
}

// The renames that put a new checkout in place run inside the caller's
// gate, once, and nothing else of the fetch does: a caller that reads the
// checkout keeps its readers out for exactly that moment.
func TestFetchSourceGated(t *testing.T) {
	g := testGit(t)
	src, _ := sourceRepo(t, map[string]string{"a.txt": "one\n"})
	dir := filepath.Join(t.TempDir(), "skills", "set")
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := g.FetchSource(ctx, src, "", dir); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "a.txt"), "two\n")
	tg(t, src, "commit", "-q", "-am", "two")
	second := rev(t, src, "HEAD")

	// A gate that refuses leaves the old checkout and nothing next to it.
	refused := errors.New("the gate refused")
	if _, err := g.FetchSourceGated(ctx, src, "main", dir, func(func() error) error { return refused }); !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the gate's", err)
	}
	if got := read(t, filepath.Join(dir, "a.txt")); got != "one\n" {
		t.Fatalf("a refused swap changed the checkout: %q", got)
	}
	noSiblings(t, dir)

	calls := 0
	got, err := g.FetchSourceGated(ctx, src, "main", dir, func(swap func() error) error {
		calls++
		if got := read(t, filepath.Join(dir, "a.txt")); got != "one\n" {
			t.Errorf("before the swap the checkout holds %q, want the old one", got)
		}
		err := swap()
		if got := read(t, filepath.Join(dir, "a.txt")); got != "two\n" {
			t.Errorf("after the swap the checkout holds %q, want the new one", got)
		}
		return err
	})
	if err != nil || got != second || calls != 1 {
		t.Fatalf("gated fetch: commit %s, %v, %d gate calls; want %s, one call", got, err, calls, second)
	}
	noSiblings(t, dir)
}

func TestFetchSourceTag(t *testing.T) {
	g := testGit(t)
	src, first := sourceRepo(t, map[string]string{"a.txt": "one\n"})
	tg(t, src, "tag", "-a", "v1.0.0", "-m", "release")
	tg(t, src, "tag", "light", first)
	write(t, filepath.Join(src, "a.txt"), "two\n")
	tg(t, src, "commit", "-q", "-am", "two")
	for _, tag := range []string{"v1.0.0", "light"} {
		dir := filepath.Join(t.TempDir(), "c")
		got, err := g.FetchSource(context.Background(), src, tag, dir)
		if err != nil {
			t.Fatalf("%s: %v", tag, err)
		}
		if got != first || read(t, filepath.Join(dir, "a.txt")) != "one\n" {
			t.Errorf("%s: commit %s, want %s", tag, got, first)
		}
	}
}

func TestFetchSourcePinnedCommit(t *testing.T) {
	g := testGit(t)
	src, first := sourceRepo(t, map[string]string{"a.txt": "one\n"})
	write(t, filepath.Join(src, "a.txt"), "two\n")
	tg(t, src, "commit", "-q", "-am", "two")
	dir := filepath.Join(t.TempDir(), "c")
	got, err := g.FetchSource(context.Background(), src, first, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != first || read(t, filepath.Join(dir, "a.txt")) != "one\n" {
		t.Fatalf("commit %s, want %s", got, first)
	}
	missing := strings.Repeat("ab", 20)
	if _, err := g.FetchSource(context.Background(), src, missing, dir); err == nil {
		t.Fatal("a commit the remote does not have was fetched")
	}
	if c, _ := g.SourceCommit(context.Background(), dir); c != first {
		t.Errorf("a failed fetch changed the checkout: %s", c)
	}
	noSiblings(t, dir)
}

func TestFetchSourceRefusesBadInput(t *testing.T) {
	g := testGit(t)
	src, _ := sourceRepo(t, map[string]string{"a.txt": "one\n"})
	dir := filepath.Join(t.TempDir(), "c")
	ctx := context.Background()
	if _, err := g.FetchSource(ctx, "ext::sh -c id", "", dir); !errors.Is(err, ErrBadSource) {
		t.Errorf("ext:: address: %v", err)
	}
	if _, err := g.FetchSource(ctx, "--upload-pack=touch x", "", dir); !errors.Is(err, ErrBadSource) {
		t.Errorf("option-like address: %v", err)
	}
	if _, err := g.FetchSource(ctx, src, "--upload-pack=x", dir); !errors.Is(err, ErrBadRef) {
		t.Errorf("option-like ref: %v", err)
	}
	if _, err := g.FetchSource(ctx, src, "", "relative"); err == nil {
		t.Error("a relative folder was accepted")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a refused fetch made the folder")
	}
}

func TestFetchSourceRefusesAgentFolder(t *testing.T) {
	g := testGit(t)
	src, _ := sourceRepo(t, map[string]string{"a.txt": "one\n"})
	agents := t.TempDir()
	g.SetAgentFolders(agents)
	_, err := g.FetchSource(context.Background(), src, "", filepath.Join(agents, "c"))
	if !errors.Is(err, ErrAgentFolder) {
		t.Fatalf("err = %v, want ErrAgentFolder", err)
	}
}

func TestFetchSourceTooLarge(t *testing.T) {
	g := testGit(t)
	big := strings.Repeat("0123456789abcdef", 64<<10) // 1 MiB, poorly compressible below
	var b strings.Builder
	for i := 0; i < len(big); i += 16 {
		b.WriteString(big[i : i+16])
		b.WriteByte(byte('a' + i%23))
	}
	src, _ := sourceRepo(t, map[string]string{"big.txt": b.String()})
	old := MaxSourceBytes
	MaxSourceBytes = 64 << 10
	t.Cleanup(func() { MaxSourceBytes = old })
	dir := filepath.Join(t.TempDir(), "c")
	_, err := g.FetchSource(context.Background(), src, "", dir)
	if !errors.Is(err, ErrSourceTooLarge) {
		t.Fatalf("err = %v, want ErrSourceTooLarge", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("an oversized checkout was put in place")
	}
	entries, _ := os.ReadDir(filepath.Dir(dir))
	if len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestFetchSourceHidesCredentials(t *testing.T) {
	g := testGit(t)
	_, err := g.FetchSource(context.Background(), "https://user:SECRET@127.0.0.1:1/x.git", "", filepath.Join(t.TempDir(), "c"))
	if err == nil {
		t.Fatal("fetching from a closed port succeeded")
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "user:") {
		t.Fatalf("the error repeats the credentials: %v", err)
	}
}

func TestSourceCommitRefusesNonCheckout(t *testing.T) {
	g := testGit(t)
	dir := t.TempDir()
	if _, err := g.SourceCommit(context.Background(), dir); !errors.Is(err, ErrNoSourceCommit) {
		t.Errorf("empty folder: %v", err)
	}
	write(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
	if _, err := g.SourceCommit(context.Background(), dir); !errors.Is(err, ErrNoSourceCommit) {
		t.Errorf("attached HEAD: %v", err)
	}
}
