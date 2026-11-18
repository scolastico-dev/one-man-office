package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/childenv/childenvtest"
	"github.com/scolastico-dev/one-man-office/internal/testpath"
)

// smoke drives a real omo binary against an isolated home.
type smoke struct {
	t      *testing.T
	binary string
	env    []string
	home   string
}

func (s *smoke) run(args ...string) (string, error) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.binary, args...)
	cmd.Env = s.env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func (s *smoke) must(args ...string) string {
	s.t.Helper()
	out, err := s.run(args...)
	if err != nil {
		// A background start points at its log: show it, the temporary
		// home is gone once the test ends.
		log, _ := os.ReadFile(filepath.Join(s.home, "run", "omo.log"))
		s.t.Fatalf("omo %s: %v\n%s\nomo.log:\n%s", strings.Join(args, " "), err, out, log)
	}
	return out
}

func (s *smoke) mustFail(wantInOutput string, args ...string) {
	s.t.Helper()
	out, err := s.run(args...)
	if err == nil {
		s.t.Fatalf("omo %s succeeded, want failure containing %q:\n%s", strings.Join(args, " "), wantInOutput, out)
	}
	if !strings.Contains(out, wantInOutput) {
		s.t.Fatalf("omo %s output %q lacks %q", strings.Join(args, " "), out, wantInOutput)
	}
}

func environment(home string) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(strings.ToUpper(key), "OMO_") {
			continue
		}
		switch strings.ToUpper(key) {
		case "HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "APPDATA", "USERPROFILE":
			continue
		case "PATH":
			// A first start looks for agent programs on PATH and runs their
			// usage scripts: the real ones of this machine stay out of reach.
			kv = "PATH=" + testpath.Without(strings.SplitN(kv, "=", 2)[1])
		}
		env = append(env, kv)
	}
	return append(env,
		"OMO_HOME="+filepath.Join(home, "omo"),
		"HOME="+home,
		"USERPROFILE="+home,
		"APPDATA="+filepath.Join(home, "appdata"),
		"XDG_CONFIG_HOME="+filepath.Join(home, "xdg-config"),
		"XDG_DATA_HOME="+filepath.Join(home, "xdg-data"),
	)
}

// TestSmokeEnvironmentReachesNoAgentProgram fails when the environment the
// smoke test hands to the omo binary inherits omo state or finds a real agent.
func TestSmokeEnvironmentReachesNoAgentProgram(t *testing.T) {
	childenvtest.SetOmo(t)
	home := t.TempDir()
	env := environment(home)
	childenvtest.CheckOmo(t, env, map[string]string{"OMO_HOME": filepath.Join(home, "omo")})
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			if found := testpath.Reaches(strings.TrimPrefix(kv, "PATH=")); len(found) != 0 {
				t.Fatalf("the omo binary of the smoke test can reach %v", found)
			}
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestSmokeServerAndAI(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts the real binary")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "bin", "omo")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	s := &smoke{t: t, binary: binary, env: environment(root), home: filepath.Join(root, "omo")}
	// Stop whatever this test started, also when it fails halfway.
	t.Cleanup(func() { _, _ = s.run("stop") })

	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=smoke@example.invalid", "-c", "user.name=smoke", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	port := freePort(t)
	s.must("config", "set", "port", strconv.Itoa(port))

	// Background start waits for readiness and prints the address and token.
	started := s.must("-d")
	if !strings.Contains(started, "omo.localhost:"+strconv.Itoa(port)) {
		t.Fatalf("omo -d did not print the address:\n%s", started)
	}
	tokenBytes, err := os.ReadFile(filepath.Join(s.home, "token"))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if !strings.Contains(started, "#"+token) {
		t.Fatalf("omo -d did not print the login token:\n%s", started)
	}
	// A second start reports the running server.
	if again := s.must(); !strings.Contains(again, "omo is already running") || !strings.Contains(again, "#"+token) {
		t.Fatalf("second start: %s", again)
	}
	if again := s.must("-d"); !strings.Contains(again, "omo is already running") {
		t.Fatalf("second background start: %s", again)
	}

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	api := func(method, path, body string, out any) {
		t.Helper()
		req, err := http.NewRequest(method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode/100 != 2 {
			t.Fatalf("%s %s: %d %s", method, path, resp.StatusCode, data)
		}
		if out != nil {
			if err := json.Unmarshal(data, out); err != nil {
				t.Fatalf("%s %s: %v\n%s", method, path, err, data)
			}
		}
	}

	// The API refuses requests without the token.
	if resp, err := http.Get(base + "/api/server"); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated status %d", resp.StatusCode)
		}
	}

	var project struct {
		ID string `json:"id"`
	}
	body, _ := json.Marshal(map[string]string{"path": repo})
	api("POST", "/api/projects", string(body), &project)
	if project.ID == "" {
		t.Fatal("project has no id")
	}

	if out := s.must("ai", "ticket", "create", "--type", "story", "--title", "Login"); !strings.HasPrefix(out, "#1 ") {
		t.Fatalf("story: %s", out)
	}
	s.mustFail("not a project of story #1", "ai", "ticket", "create", "--type", "task", "--title", "A", "--project", project.ID, "--story", "1")
	s.must("ai", "ticket", "update", "1", "--project", project.ID)
	if out := s.must("ai", "ticket", "create", "--type", "task", "--title", "A", "--project", project.ID, "--story", "1"); !strings.HasPrefix(out, "#2 ") {
		t.Fatalf("task A: %s", out)
	}
	if out := s.must("ai", "ticket", "create", "--type", "task", "--title", "B", "--project", project.ID, "--story", "1"); !strings.HasPrefix(out, "#3 ") {
		t.Fatalf("task B: %s", out)
	}
	s.must("ai", "link", "2", "--depends-on", "3")
	s.mustFail("cycle", "ai", "link", "3", "--depends-on", "2")

	if out := s.must("ai", "comment", "1", "@pm hi"); !strings.Contains(out, "nobody is currently behind @pm on #1; the comment is stored but no agent was notified") {
		t.Fatalf("comment: %s", out)
	}
	s.must("ai", "ticket", "move", "1", "ready")

	// The same data through the API.
	var list struct {
		Tickets []struct {
			Number int    `json:"number"`
			Column string `json:"column"`
		} `json:"tickets"`
	}
	api("GET", "/api/tickets", "", &list)
	columns := map[int]string{}
	for _, tk := range list.Tickets {
		columns[tk.Number] = tk.Column
	}
	if len(columns) != 3 || columns[1] != "ready" || columns[2] != "planning" {
		t.Fatalf("tickets through the API: %v", columns)
	}
	if out := s.must("ai", "ticket", "list", "--json"); !strings.Contains(out, `"number": 3`) && !strings.Contains(out, `"number":3`) {
		t.Fatalf("ticket list: %s", out)
	}
	// A ticket created through the API shows up in `omo ai` and moves there too.
	created, _ := json.Marshal(map[string]any{"type": "task", "title": "C", "projects": []string{project.ID}})
	api("POST", "/api/tickets", string(created), nil)
	if out := s.must("ai", "ticket", "show", "4"); !strings.Contains(out, "C") {
		t.Fatalf("ticket show: %s", out)
	}
	api("POST", "/api/tickets/4/move", `{"column":"ready"}`, nil)

	// The event stream delivers the changes in order.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/api/events?since=0", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var kinds []string
	moved := 0
	var lastID int64
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for moved < 2 && scanner.Scan() {
		line := scanner.Text()
		if rest, ok := strings.CutPrefix(line, "id: "); ok {
			id, _ := strconv.ParseInt(rest, 10, 64)
			if id <= lastID {
				t.Fatalf("event id %d after %d", id, lastID)
			}
			lastID = id
		}
		if kind, ok := strings.CutPrefix(line, "event: "); ok {
			kinds = append(kinds, kind)
			if kind == "ticket.moved" {
				moved++
			}
		}
	}
	if moved < 2 {
		t.Fatalf("stream ended early (%v): %v", scanner.Err(), kinds)
	}
	first := func(kind string) int {
		for i, k := range kinds {
			if k == kind {
				return i
			}
		}
		return -1
	}
	if first("project.changed") < 0 || first("ticket.created") < 0 || first("comment.created") < 0 || first("ticket.moved") < first("ticket.created") {
		t.Fatalf("unexpected event order: %v", kinds)
	}
	cancel()

	if out := s.must("stop"); !strings.Contains(out, "omo stopped.") {
		t.Fatalf("stop: %s", out)
	}
	if out := s.must("stop"); !strings.Contains(out, "omo is not running.") {
		t.Fatalf("second stop: %s", out)
	}
	if _, err := http.Get(base + "/api/server"); err == nil {
		t.Fatal("server still answers after stop")
	}
	entries, err := os.ReadDir(filepath.Join(root, "xdg-config"))
	if err == nil && len(entries) > 0 {
		t.Fatalf("test registered an autostart entry: %v", fmt.Sprint(entries))
	}
}
