package server

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
)

func foldersPath(p string) string {
	return "/api/folders?" + url.Values{"path": {p}}.Encode()
}

func TestFoldersRoute(t *testing.T) {
	s := newTestServer(t)
	u := userCaller(t, s)
	// The listing of no path shows the home folder: never the real one.
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	for _, d := range []string{"plain", "repo/.git", ".hidden"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got := okJSON[api.FolderListing](t, u, 200, "GET", foldersPath(dir), nil)
	if got.Path != dir || got.Parent != filepath.Dir(dir) || got.Home == "" || got.More {
		t.Fatalf("listing %+v", got)
	}
	var names []string
	for _, f := range got.Folders {
		names = append(names, f.Name)
		if f.Path != filepath.Join(dir, f.Name) {
			t.Fatalf("entry %+v", f)
		}
		if f.Repository != (f.Name == "repo") || f.Hidden != (f.Name == ".hidden") || f.Link {
			t.Fatalf("entry %+v", f)
		}
	}
	if strings.Join(names, ",") != "plain,repo,.hidden" {
		t.Fatalf("folders %v", names)
	}

	// An empty folder answers an empty list, never null.
	status, data := u.do("GET", foldersPath(filepath.Join(dir, "plain")), nil)
	if status != 200 || !strings.Contains(string(data), `"folders":[]`) {
		t.Fatalf("empty folder: %d %s", status, data)
	}
	// No path lists the home folder.
	home := okJSON[api.FolderListing](t, u, 200, "GET", "/api/folders", nil)
	if home.Path != home.Home {
		t.Fatalf("home listing %+v", home)
	}

	u.fails(404, api.CodeNotFound, "GET", foldersPath(filepath.Join(dir, "nothing")), nil)
	u.fails(422, api.CodeValidation, "GET", foldersPath("relative/path"), nil)
	u.fails(422, api.CodeValidation, "GET", foldersPath(file), nil)
}
