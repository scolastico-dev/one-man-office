package cli

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

func writePNG(t *testing.T, name string) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

var refLine = regexp.MustCompile(`!\[[^\]]*\]\(omo-image:([0-9a-f-]{36})\)`)

// An agent adds an image with the CLI, pastes the reference, and every
// agent-facing reading of descriptions and comments shows the local path.
func TestAIImageAddAndLocalPaths(t *testing.T) {
	e := startAI(t)
	e.story("one")
	e.must("", "ticket", "create", "--type", "task", "--title", "work", "--project", e.project, "--story", "1")
	e.must("", "ticket", "create", "--type", "task", "--title", "other", "--project", e.project)
	home := os.Getenv("OMO_HOME")

	// The user adds an image to #3, which the developer of #2 may not read.
	out := e.must("", "image", "add", "3", writePNG(t, "secret plan.png"))
	m := refLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no reference printed: %q", out)
	}
	foreign := m[1]
	contains(t, out, "    ![secret plan.png](omo-image:"+foreign+")")
	contains(t, out, "local file: "+filepath.Join(home, "images", foreign+".png"))

	sess, err := e.board.CreateSession(context.Background(), board.NewSession{Role: api.RoleDeveloper, Ticket: 2, Name: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OMO_AGENT_ID", sess.ID)
	out = e.must("", "image", "add", "2", writePNG(t, "screen.png"))
	m = refLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no reference printed: %q", out)
	}
	own := m[1]
	path := filepath.Join(home, "images", own+".png")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stored file: %v", err)
	}
	if _, err := e.run("", "image", "add", "3", writePNG(t, "x.png")); err == nil || !strings.Contains(err.Error(), "(forbidden)") {
		t.Fatalf("an upload outside the agent's scope: %v", err)
	}
	notImage := filepath.Join(t.TempDir(), "x.png")
	os.WriteFile(notImage, []byte("<svg/>"), 0o600)
	if _, err := e.run("", "image", "add", "2", notImage); err == nil || !strings.Contains(err.Error(), "(validation)") {
		t.Fatalf("an SVG: %v", err)
	}

	body := "Look: ![screen.png](omo-image:" + own + ")\nand ![leaked](omo-image:" + foreign + ")"
	e.must(body, "comment", "2", "-")
	// The developer may not edit the description; the user writes it.
	asUser(t)
	e.must(body, "ticket", "update", "2", "--description-file", "-")
	t.Setenv("OMO_AGENT_ID", sess.ID)

	note := "![screen.png](omo-image:" + own + ") (local file: " + path + ")"
	for _, args := range [][]string{{"ticket", "show", "2"}, {"comments", "2"}} {
		out := e.must("", args...)
		contains(t, out, note)
		// The image of a ticket the agent may not read gives no path.
		contains(t, out, "![leaked](omo-image:"+foreign+") (image missing)")
		if strings.Contains(out, foreign+".png") {
			t.Fatalf("%v shows the path of an image outside the scope:\n%s", args, out)
		}
	}
	// --json is the server's answer, unchanged.
	raw := e.must("", "ticket", "show", "2", "--json")
	if strings.Contains(raw, "local file") {
		t.Fatalf("--json was rewritten: %s", raw)
	}

	// An expired image says so instead of a path.
	if err := e.board.ExpireImage(context.Background(), board.System, own); err != nil {
		t.Fatal(err)
	}
	contains(t, e.must("", "comments", "2"), "![screen.png](omo-image:"+own+") (image expired)")
}

func TestAIImageAddRefusesLocally(t *testing.T) {
	e := startAI(t)
	e.story("one")
	if _, err := e.run("", "image", "add", "1", t.TempDir()); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("a folder: %v", err)
	}
	big := filepath.Join(t.TempDir(), "big.png")
	f, _ := os.Create(big)
	f.Truncate(10<<20 + 1)
	f.Close()
	if _, err := e.run("", "image", "add", "1", big); err == nil || !strings.Contains(err.Error(), "at most 10 MiB") {
		t.Fatalf("a large file: %v", err)
	}
}
