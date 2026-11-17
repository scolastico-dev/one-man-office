//go:build !windows

// The planted programs are shell commands, which Windows cannot run.
package gitops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// gitWithGlobal returns a Git whose environment uses a global config file
// with content: the user's own settings, which omo keeps.
func gitWithGlobal(t *testing.T, content string, extra ...string) *Git {
	t.Helper()
	requireGit(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	write(t, global, content)
	g := New()
	env := append(isolatedEnv(t), "GIT_CONFIG_GLOBAL="+global)
	g.SetEnvironment(append(env, extra...))
	return g
}

func TestFetchSourceRunsNoHooks(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	hook := "#!/bin/sh\ntouch '" + marker + "'\n"
	// The source's own hooks and the user's template and hooks folder.
	src, commit := sourceRepo(t, map[string]string{"a.txt": "one\n"})
	for _, h := range []string{"post-checkout", "reference-transaction", "post-index-change"} {
		write(t, filepath.Join(src, ".git", "hooks", h), hook)
		_ = os.Chmod(filepath.Join(src, ".git", "hooks", h), 0o755)
	}
	tmpl := t.TempDir()
	hooks := t.TempDir()
	for _, d := range []string{filepath.Join(tmpl, "hooks"), hooks} {
		for _, h := range []string{"post-checkout", "reference-transaction", "post-index-change"} {
			write(t, filepath.Join(d, h), hook)
			_ = os.Chmod(filepath.Join(d, h), 0o755)
		}
	}
	g := gitWithGlobal(t, "[init]\n\ttemplateDir = "+tmpl+"\n[core]\n\thooksPath = "+hooks+"\n")
	dir := filepath.Join(t.TempDir(), "c")
	got, err := g.FetchSource(context.Background(), src, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != commit {
		t.Fatalf("commit %s, want %s", got, commit)
	}
	noMarker(t, marker)
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks")); err == nil {
		t.Error("the checkout got a hooks folder from a template")
	}
}

func TestFetchSourceRunsNoFilter(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	cmd := "touch '" + marker + "'; cat"
	// A filter the user configured (as git-lfs is), selected by the
	// repository's attributes, with a macro and a process filter too.
	g := gitWithGlobal(t, "[filter \"x\"]\n\tsmudge = "+cmd+"\n\tclean = "+cmd+"\n\trequired = true\n"+
		"[filter \"p\"]\n\tprocess = "+cmd+"\n\trequired = true\n")
	src, _ := sourceRepo(t, map[string]string{
		"a.txt":          "one\n",
		"b.bin":          "two\n",
		".gitattributes": "[attr]m filter=p\n*.txt filter=x\n*.bin m\n",
	})
	dir := filepath.Join(t.TempDir(), "c")
	if _, err := g.FetchSource(context.Background(), src, "", dir); err != nil {
		t.Fatal(err)
	}
	noMarker(t, marker)
	if read(t, filepath.Join(dir, "a.txt")) != "one\n" || read(t, filepath.Join(dir, "b.bin")) != "two\n" {
		t.Error("the files were not checked out as committed")
	}
}

func TestFetchSourceChecksOutNoLinks(t *testing.T) {
	g := testGit(t)
	src, _ := sourceRepo(t, map[string]string{"a.txt": "one\n"})
	secret := filepath.Join(t.TempDir(), "secret")
	write(t, secret, "do not read\n")
	if err := os.Symlink(secret, filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/", filepath.Join(src, "root")); err != nil {
		t.Fatal(err)
	}
	tg(t, src, "add", "link", "root")
	tg(t, src, "commit", "-q", "-m", "links")
	dir := filepath.Join(t.TempDir(), "c")
	if _, err := g.FetchSource(context.Background(), src, "", dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"link", "root"} {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !fi.Mode().IsRegular() {
			t.Errorf("%s was checked out as %v, want a regular file", name, fi.Mode())
		}
	}
	if read(t, filepath.Join(dir, "link")) != secret {
		t.Error("the link's file holds something else than its target")
	}
}

func TestFetchSourceNoSubmodules(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	sub, _ := sourceRepo(t, map[string]string{"s.txt": "sub\n"})
	write(t, filepath.Join(sub, ".git", "hooks", "post-checkout"), "#!/bin/sh\ntouch '"+marker+"'\n")
	_ = os.Chmod(filepath.Join(sub, ".git", "hooks", "post-checkout"), 0o755)
	src, _ := sourceRepo(t, map[string]string{"a.txt": "one\n"})
	tg(t, src, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "sub")
	tg(t, src, "commit", "-q", "-m", "sub")
	// The user asks for submodules everywhere; a source still gets none.
	g := gitWithGlobal(t, "[submodule]\n\trecurse = true\n[fetch]\n\trecurseSubmodules = true\n[protocol \"file\"]\n\tallow = always\n")
	dir := filepath.Join(t.TempDir(), "c")
	if _, err := g.FetchSource(context.Background(), src, "", dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "s.txt")); err == nil {
		t.Error("the submodule was checked out")
	}
	noMarker(t, marker)
}

func TestFetchSourceRefusesExtTransportInConfig(t *testing.T) {
	// Even if the user's config rewrote an address to a command transport,
	// git refuses it (GIT_ALLOW_PROTOCOL).
	marker := filepath.Join(t.TempDir(), "ran")
	g := gitWithGlobal(t, "[url \"ext::sh -c touch% "+marker+"% #\"]\n\tinsteadOf = https://rewritten.invalid/\n[protocol \"ext\"]\n\tallow = always\n")
	_, err := g.FetchSource(context.Background(), "https://rewritten.invalid/x", "", filepath.Join(t.TempDir(), "c"))
	if err == nil {
		t.Fatal("the fetch succeeded")
	}
	noMarker(t, marker)
}
