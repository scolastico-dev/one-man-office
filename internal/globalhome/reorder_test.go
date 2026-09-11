package globalhome

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestReorderPersistsRequestedOrderAfterReopen(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	a, b, c := filepath.Join(root, "a"), filepath.Join(root, "b"), filepath.Join(root, "c")
	for _, path := range []string{a, b, c} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("trusted_offices:\n  - "+a+"\n  - "+b+"\n  - "+c+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err != nil {
		t.Fatal(err)
	}
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{c, a, b}
	if err := h.Reorder(want); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.Config.TrustedOffices, want) {
		t.Fatalf("in-memory order = %v, want %v", h.Config.TrustedOffices, want)
	}
	reopened, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reopened.Config.TrustedOffices, want) {
		t.Fatalf("reopened order = %v, want %v", reopened.Config.TrustedOffices, want)
	}
}

func TestReorderRejectsCountMismatch(t *testing.T) {
	h, paths := openReorderHome(t, 2)
	before := readReorderConfig(t, h)
	if err := h.Reorder(paths[:1]); err == nil {
		t.Fatal("count mismatch accepted")
	}
	assertReorderConfigUnchanged(t, h, before)
}

func TestReorderRejectsMissingAndExtraPath(t *testing.T) {
	h, paths := openReorderHome(t, 2)
	before := readReorderConfig(t, h)
	want := []string{paths[1], filepath.Join(h.Dir, "extra")}
	if err := h.Reorder(want); err == nil {
		t.Fatal("missing and extra path accepted")
	}
	assertReorderConfigUnchanged(t, h, before)
}

func TestReorderRejectsDuplicatePath(t *testing.T) {
	h, paths := openReorderHome(t, 2)
	before := readReorderConfig(t, h)
	if err := h.Reorder([]string{paths[0], paths[0]}); err == nil {
		t.Fatal("duplicate path accepted")
	}
	assertReorderConfigUnchanged(t, h, before)
}

func TestReorderPreservesCommentsAndPluginSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	paths := []string{filepath.Join(root, "a"), filepath.Join(root, "b")}
	raw := "# keep this global comment\ntrusted_offices:\n  # keep this list comment\n  - " + paths[0] + "\n  - " + paths[1] + "\nplugins:\n  update_on_start: false\n  installed:\n    example:\n      source: https://example.test/plugin.git\n      enabled: true\n      config: {message: hello}\n"
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err != nil {
		t.Fatal(err)
	}
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Reorder([]string{paths[1], paths[0]}); err != nil {
		t.Fatal(err)
	}
	data := string(readReorderConfig(t, h))
	for _, want := range []string{"# keep this global comment", "# keep this list comment", "https://example.test/plugin.git", "message: hello"} {
		if !strings.Contains(data, want) {
			t.Fatalf("reordered config lost %q:\n%s", want, data)
		}
	}
	first, second := strings.Index(data, "- "+paths[1]), strings.Index(data, "- "+paths[0])
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("reordered sequence not persisted:\n%s", data)
	}
	reopened, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Config.Plugins.UpdateOnStart || reopened.Config.Plugins.Installed["example"].Config["message"] != "hello" {
		t.Fatalf("plugin settings changed: %+v", reopened.Config.Plugins)
	}
}

func TestReorderInvalidInputDoesNotRewriteConfig(t *testing.T) {
	h, paths := openReorderHome(t, 2)
	before := readReorderConfig(t, h)
	if err := h.Reorder([]string{paths[0], paths[0]}); err == nil {
		t.Fatal("invalid reorder accepted")
	}
	after := readReorderConfig(t, h)
	if !bytes.Equal(after, before) {
		t.Fatalf("invalid reorder rewrote config: before %q, after %q", before, after)
	}
	if !slices.Equal(h.Config.TrustedOffices, paths) {
		t.Fatalf("invalid reorder changed in-memory order: %v", h.Config.TrustedOffices)
	}
}

func TestReorderSerializesWithConcurrentTrust(t *testing.T) {
	h, paths := openReorderHome(t, 2)
	other, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(h.Dir, "new")
	if err := os.Mkdir(newPath, 0700); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var reorderErr, trustErr error
	wg.Go(func() { reorderErr = h.Reorder([]string{paths[1], paths[0]}) })
	wg.Go(func() { trustErr = other.Trust(newPath) })
	wg.Wait()
	if trustErr != nil {
		t.Fatal(trustErr)
	}
	if reorderErr != nil && !strings.Contains(reorderErr.Error(), "permutation") {
		t.Fatalf("unexpected reorder error: %v", reorderErr)
	}
	final, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(final.Config.TrustedOffices, newPath) || len(final.Config.TrustedOffices) != 3 {
		t.Fatalf("concurrent trust was lost: %v", final.Config.TrustedOffices)
	}
}

func openReorderHome(t *testing.T, count int) (*Home, []string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("OMO_HOME", root)
	paths := make([]string, count)
	for i := range paths {
		paths[i] = filepath.Join(root, string(rune('a'+i)))
		if err := os.Mkdir(paths[i], 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("trusted_offices:\n  - "+strings.Join(paths, "\n  - ")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(); err != nil {
		t.Fatal(err)
	}
	h, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	return h, paths
}

func readReorderConfig(t *testing.T, h *Home) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(h.Dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertReorderConfigUnchanged(t *testing.T, h *Home, before []byte) {
	t.Helper()
	after := readReorderConfig(t, h)
	if !bytes.Equal(after, before) {
		t.Fatalf("invalid reorder rewrote config: before %q, after %q", before, after)
	}
}
