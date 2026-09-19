package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

func pullrequestSourceDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(filename), "..", "..", "plugins", "pullrequest")
}

func loadPullrequest(t *testing.T, config map[string]any) (*Manager, func()) {
	t.Helper()
	office, database := newPluginOffice(t)
	source := pullrequestSourceDir(t)
	target := filepath.Join(office, Dir, "pullrequest")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plugin.json", "prompt.lua", "create.lua"} {
		raw, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, name), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := LoadConfigured(office, database, map[string]Settings{
		"pullrequest": {Enabled: true, Config: config},
	})
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_ = manager.Close()
		_ = database.Close()
	}
	return manager, cleanup
}

func pullrequestManualHook(t *testing.T, manager *Manager) loadedHook {
	t.Helper()
	for _, hook := range manager.hooks {
		if hook.plugin == "pullrequest" && hook.hook.Event == EventManual && hook.hook.Name == "create" {
			return hook
		}
	}
	t.Fatal("pullrequest create hook not loaded")
	return loadedHook{}
}

func runPullrequestManual(t *testing.T, manager *Manager, data map[string]any) error {
	t.Helper()
	hook := pullrequestManualHook(t, manager)
	_, err := manager.runHook(context.Background(), hook, Event{Name: EventManual, Data: data}, nil)
	return err
}

func pullrequestJobEvent(worktree, remote, branch, base string, jobID int64, args ...string) map[string]any {
	return map[string]any{
		"plugin":      "pullrequest",
		"action":      "create",
		"caller":      "developer-ada",
		"caller_role": "developer",
		"args":        args,
		"request_id":  int64(1),
		"job_id":      jobID,
		"repo":        remote,
		"branch":      branch,
		"base_branch": base,
		"worktree":    worktree,
	}
}

func pullrequestBodyFile(t *testing.T, contents []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "description.md")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func pullrequestJobEventWithBody(t *testing.T, worktree, remote, branch, base string, jobID int64, args ...string) map[string]any {
	t.Helper()
	bodyPath := pullrequestBodyFile(t, pullrequestValidBody())
	data := pullrequestJobEvent(worktree, remote, branch, base, jobID, args...)
	data["args"] = append([]string{"body=" + bodyPath}, args...)
	return data
}

func pullrequestValidBody() []byte {
	return []byte("## Summary\nThis is a concise résumé summary.\n\n## What changed\n- Changed the implementation.\n\n## Why\nThe user-facing problem required this decision.\n\n## How it was verified\n- go test ./internal/plugins\n\n## Risks and follow-ups\nNone.\n\n## Jobs\n- #53 Test job\n")
}

func assertPullrequestUsageGuidance(t *testing.T, err error, fault string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected pullrequest usage error")
	}
	message := err.Error()
	for _, want := range []string{
		fault,
		`omo plugin trigger pullrequest create -- [repo=<key>] body=<absolute-path> "<title>"`,
		"Required sections: ## Summary, ## What changed, ## Why, ## How it was verified",
		"Recommended sections: ## Risks and follow-ups, ## Jobs",
		"plugins/pullrequest/README.md description-file section",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("usage error %q does not contain %q", message, want)
		}
	}
}

func TestPullrequestDescriptionUsageValidationPrecedesSideEffects(t *testing.T) {
	cases := []struct {
		name  string
		args  func(*testing.T) []string
		fault string
	}{
		{name: "missing body", args: func(t *testing.T) []string { return []string{"A title"} }, fault: "body=<absolute-path> is required"},
		{name: "empty body", args: func(t *testing.T) []string { return []string{"body=", "A title"} }, fault: "body= must name an absolute Markdown file"},
		{name: "empty repo", args: func(t *testing.T) []string {
			return []string{"repo=", "body=" + pullrequestBodyFile(t, pullrequestValidBody()), "A title"}
		}, fault: "repo= must name a repository"},
		{name: "relative body", args: func(t *testing.T) []string { return []string{"body=description.md", "A title"} }, fault: "body path must be absolute"},
		{name: "windows drive body", args: func(t *testing.T) []string { return []string{`body=C:\description.md`, "A title"} }, fault: "description file could not be read"},
		{name: "windows UNC body", args: func(t *testing.T) []string { return []string{`body=\\server\share\description.md`, "A title"} }, fault: "description file could not be read"},
		{name: "missing file", args: func(t *testing.T) []string {
			return []string{"body=" + filepath.Join(t.TempDir(), "missing.md"), "A title"}
		}, fault: "description file could not be read"},
		{name: "unreadable file", args: func(t *testing.T) []string { return []string{"body=" + t.TempDir(), "A title"} }, fault: "description file could not be read"},
		{name: "invalid utf8", args: func(t *testing.T) []string {
			return []string{"body=" + pullrequestBodyFile(t, []byte("## Summary\n\xff")), "A title"}
		}, fault: "description file is not valid UTF-8"},
		{name: "empty after trim", args: func(t *testing.T) []string {
			return []string{"body=" + pullrequestBodyFile(t, []byte(" \t\r\n")), "A title"}
		}, fault: "description file is empty after trimming"},
		{name: "too large", args: func(t *testing.T) []string {
			return []string{"body=" + pullrequestBodyFile(t, []byte(strings.Repeat("x", 61441))), "A title"}
		}, fault: "description file exceeds 61440 bytes"},
		{name: "missing headings", args: func(t *testing.T) []string {
			return []string{"body=" + pullrequestBodyFile(t, []byte("## Summary\nsummary\n")), "A title"}
		}, fault: "missing required headings: ## What changed, ## Why, ## How it was verified"},
		{name: "duplicate body", args: func(t *testing.T) []string {
			path := pullrequestBodyFile(t, pullrequestValidBody())
			return []string{"body=" + path, "body=" + path, "A title"}
		}, fault: "body= may be specified only once"},
		{name: "duplicate repo", args: func(t *testing.T) []string {
			path := pullrequestBodyFile(t, pullrequestValidBody())
			return []string{"repo=api", "body=" + path, "repo=web", "A title"}
		}, fault: "repo= may be specified only once"},
		{name: "unknown key", args: func(t *testing.T) []string {
			path := pullrequestBodyFile(t, pullrequestValidBody())
			return []string{"body=" + path, "unknown=value", "A title"}
		}, fault: "unknown key-like argument unknown=value"},
		{name: "malformed body key", args: func(t *testing.T) []string { return []string{"body", "A title"} }, fault: "body must use body=<absolute-path>"},
		{name: "multiple titles", args: func(t *testing.T) []string {
			path := pullrequestBodyFile(t, pullrequestValidBody())
			return []string{"body=" + path, "one", "two"}
		}, fault: "at most one title argument"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("invalid usage reached provider: %s %s", r.Method, r.URL.Path)
			})
			pullrequestFailingGHStub(t)
			commandLog := pullrequestCommandStub(t, "")
			worktree, bare := pullrequestRepo(t, "https://github.com/acme/repo.git")
			manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "api_url": server.URL, "token": "test-token"})
			defer cleanup()
			err := runPullrequestManual(t, manager, pullrequestJobEvent(worktree, "acme/repo", "feature/pullrequest", "main", 53, test.args(t)...))
			assertPullrequestUsageGuidance(t, err, test.fault)
			if requests := capture.snapshot(); len(requests) != 0 {
				t.Fatalf("invalid usage made provider requests: %+v", requests)
			}
			showRef := exec.Command("git", "show-ref", "refs/heads/feature/pullrequest")
			showRef.Dir = bare
			if output, showErr := showRef.CombinedOutput(); showErr == nil {
				t.Fatalf("invalid usage pushed branch: %s", output)
			}
			commandOutput, readErr := os.ReadFile(commandLog)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if strings.Contains(string(commandOutput), " send ") {
				t.Fatalf("invalid usage unexpectedly sent success notification: %q", commandOutput)
			}
		})
	}
}

func TestPullrequestDescriptionUsageMailsAgentGuidanceButNotUser(t *testing.T) {
	for _, caller := range []string{"developer-ada", "user"} {
		t.Run(caller, func(t *testing.T) {
			pullrequestFailingGHStub(t)
			commandLog := pullrequestCommandStub(t, "")
			worktree, _ := pullrequestRepo(t, "https://github.com/acme/repo.git")
			manager, cleanup := loadPullrequest(t, map[string]any{})
			defer cleanup()
			data := pullrequestJobEvent(worktree, "acme/repo", "feature/pullrequest", "main", 53, "A title")
			data["caller"] = caller
			data["caller_role"] = map[string]string{"user": "user", "developer-ada": "developer"}[caller]
			assertPullrequestUsageGuidance(t, runPullrequestManual(t, manager, data), "body=<absolute-path> is required")
			raw, err := os.ReadFile(commandLog)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			output := string(raw)
			if (caller != "user") != strings.Contains(output, "send -t "+caller) {
				t.Fatalf("caller guidance mail = %q", output)
			}
			if caller == "user" && strings.Contains(output, "send -t user") {
				t.Fatalf("user received usage mail: %q", output)
			}
		})
	}
}

func TestPullrequestDescriptionArgumentOrdersAndTrailingWhitespace(t *testing.T) {
	const wantURL = "https://github.com/acme/repo/pull/53"
	for _, order := range []string{"repo-first", "body-first", "exact-size-boundary"} {
		t.Run(order, func(t *testing.T) {
			description := pullrequestValidBody()
			if order == "exact-size-boundary" {
				description = append(description, bytes.Repeat([]byte("x"), 61440-len(description))...)
			}
			server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, "[]")
					return
				}
				var payload map[string]string
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				wantBody := string(description)
				wantBody = strings.TrimRight(wantBody, " \t\r\n")
				if payload["body"] != wantBody {
					t.Fatalf("body = %q, want %q", payload["body"], wantBody)
				}
				_, _ = io.WriteString(w, `{"html_url":"`+wantURL+`"}`)
			})
			pullrequestFailingGHStub(t)
			pullrequestCommandStub(t, "")
			worktree, _ := pullrequestRepo(t, "https://github.com/acme/repo.git")
			bodyPath := pullrequestBodyFile(t, description)
			manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "api_url": server.URL, "token": "test-token"})
			defer cleanup()
			args := []string{"body=" + bodyPath, "repo=acme/repo", "A title"}
			if order == "repo-first" {
				args = []string{"repo=acme/repo", "body=" + bodyPath, "A title"}
			}
			data := pullrequestJobEvent(worktree, "acme/repo", "feature/pullrequest", "main", 53)
			data["args"] = args
			result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", args, data)
			if err != nil {
				t.Fatal(err)
			}
			if result.Value != "acme/repo: "+wantURL+" (created)" {
				t.Fatalf("result = %q", result.Value)
			}
			if len(capture.snapshot()) != 2 {
				t.Fatalf("requests = %+v", capture.snapshot())
			}
		})
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func pullrequestRepo(t *testing.T, remoteURL string) (worktree, bare string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "remote.git")
	worktree = filepath.Join(root, "worktree")
	runGit(t, root, "init", "--bare", bare)
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "init")
	runGit(t, worktree, "config", "user.email", "pullrequest-test@example.com")
	runGit(t, worktree, "config", "user.name", "pullrequest test")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "README.md")
	runGit(t, worktree, "commit", "-m", "base")
	runGit(t, worktree, "branch", "-M", "main")
	runGit(t, worktree, "checkout", "-b", "feature/pullrequest")
	if err := os.WriteFile(filepath.Join(worktree, "change.txt"), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "change.txt")
	runGit(t, worktree, "commit", "-m", "change")
	runGit(t, worktree, "remote", "add", "origin", remoteURL)
	runGit(t, worktree, "remote", "set-url", "--push", "origin", "file://"+bare)
	return worktree, bare
}

func pullrequestCommandStub(t *testing.T, jobOutput string) (logPath string) {
	t.Helper()
	bin := t.TempDir()
	logPath = filepath.Join(bin, "omo.log")
	script := filepath.Join(bin, "omo")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PULLREQUEST_COMMAND_LOG\"\nif [ \"$1\" = job ] && [ \"$2\" = show ]; then printf '%s' \"$PULLREQUEST_JOB_OUTPUT\"; fi\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULLREQUEST_COMMAND_LOG", logPath)
	t.Setenv("PULLREQUEST_JOB_OUTPUT", jobOutput)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func pullrequestFailingGHStub(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := filepath.Join(bin, "gh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func pullrequestAuthenticatedGHStub(t *testing.T, existingURL string) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(bin, "gh.log")
	script := filepath.Join(bin, "gh")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PULLREQUEST_GH_LOG\"\nif [ \"$1\" = auth ] && [ \"$2\" = status ]; then exit 0; fi\nif [ \"$1\" = pr ] && [ \"$2\" = list ]; then printf '[{\"url\":\"%s\",\"number\":99}]' \"$PULLREQUEST_GH_EXISTING\"; exit 0; fi\nif [ \"$1\" = pr ] && [ \"$2\" = edit ]; then exit 0; fi\nif [ \"$1\" = pr ] && [ \"$2\" = create ]; then printf '%s' \"$PULLREQUEST_GH_EXISTING\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULLREQUEST_GH_LOG", logPath)
	t.Setenv("PULLREQUEST_GH_EXISTING", existingURL)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func pullrequestCreatingGHStub(t *testing.T, createdURL string) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(bin, "gh.log")
	script := filepath.Join(bin, "gh")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PULLREQUEST_GH_LOG\"\nif [ \"$1\" = auth ] && [ \"$2\" = status ]; then exit 0; fi\nif [ \"$1\" = pr ] && [ \"$2\" = list ]; then printf '[]'; exit 0; fi\nif [ \"$1\" = pr ] && [ \"$2\" = create ]; then printf '%s' \"$PULLREQUEST_GH_CREATED\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULLREQUEST_GH_LOG", logPath)
	t.Setenv("PULLREQUEST_GH_CREATED", createdURL)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

type pullrequestCapturedRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Body    []byte
}

type pullrequestCapture struct {
	mu       sync.Mutex
	Requests []pullrequestCapturedRequest
}

func newPullrequestServer(t *testing.T, handler func(http.ResponseWriter, *http.Request)) (*httptest.Server, *pullrequestCapture) {
	t.Helper()
	capture := &pullrequestCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read pullrequest request: %v", err)
			return
		}
		capture.mu.Lock()
		capture.Requests = append(capture.Requests, pullrequestCapturedRequest{Method: r.Method, Path: r.URL.RequestURI(), Headers: r.Header.Clone(), Body: body})
		capture.mu.Unlock()
		r.Body = io.NopCloser(bytes.NewReader(body))
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	return server, capture
}

func (c *pullrequestCapture) snapshot() []pullrequestCapturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pullrequestCapturedRequest(nil), c.Requests...)
}

func pullrequestOutputText(t *testing.T, manager *Manager) string {
	t.Helper()
	var parts []string
	events, err := db.AllEvents(manager.DB)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		parts = append(parts, event.Detail)
	}
	logs, err := db.PluginLogs(manager.DB, "pullrequest")
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range logs {
		parts = append(parts, log.Message)
	}
	return strings.Join(parts, "\n")
}

func TestPullrequestGitHubRESTCreatesAndNotifies(t *testing.T) {
	const token = "github-secret-token"
	server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.Path != "/repos/acme/repo/pulls" || r.URL.Query().Get("state") != "open" || r.URL.Query().Get("head") != "acme:feature/pullrequest" || r.URL.Query().Get("base") != "main" {
				t.Errorf("unexpected GitHub list request: %s", r.URL.RequestURI())
			}
			_, _ = io.WriteString(w, "[]")
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/repos/acme/repo/pulls" {
			t.Errorf("unexpected GitHub create request: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("GitHub authorization = %q", got)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode GitHub payload: %v", err)
		} else {
			if payload["title"] != "Add pull request support" || payload["head"] != "feature/pullrequest" || payload["base"] != "main" {
				t.Errorf("GitHub payload = %#v", payload)
			}
			wantBody := strings.TrimRight(string(pullrequestValidBody()), " \t\r\n")
			if payload["body"] != wantBody {
				t.Errorf("GitHub body = %q, want authored description", payload["body"])
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"html_url":"https://github.com/acme/repo/pull/53"}`)
	})
	pullrequestFailingGHStub(t)
	t.Setenv("PULLREQUEST_UNUSED_TOKEN", "wrong-token")
	commandLog := pullrequestCommandStub(t, "id: 53\ntitle: Add pull request support\nrole: developer\ngoal:\nImplement the pull request flow\nwith a durable notification\n")
	worktree, bare := pullrequestRepo(t, "https://github.com/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{
		"forge": "github", "api_url": server.URL, "token": token, "token_env": "PULLREQUEST_UNUSED_TOKEN",
	})
	defer cleanup()
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", []string{"Add pull request support"}, pullrequestJobEventWithBody(t, worktree, "acme/repo", "feature/pullrequest", "main", 53, "Add pull request support"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "acme/repo: https://github.com/acme/repo/pull/53 (created)" {
		t.Fatalf("created GitHub manual result = %q", result.Value)
	}
	requests := capture.snapshot()
	if len(requests) != 2 {
		t.Fatalf("GitHub request count = %d, want list and create", len(requests))
	}
	if requests[0].Method != http.MethodGet || requests[1].Method != http.MethodPost {
		t.Fatalf("GitHub methods = %s, %s", requests[0].Method, requests[1].Method)
	}
	if !strings.Contains(runGit(t, bare, "show-ref", "refs/heads/feature/pullrequest"), "refs/heads/feature/pullrequest") {
		t.Fatal("pullrequest branch was not pushed to the bare remote")
	}
	commandOutput, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(commandOutput)
	if !strings.Contains(commands, "send -t user") || !strings.Contains(commands, "send -t ceo") || !strings.Contains(commands, "https://github.com/acme/repo/pull/53") || !strings.Contains(commands, "(created)") {
		t.Fatalf("notifications = %q", commands)
	}
	if output := pullrequestOutputText(t, manager); !strings.Contains(output, "acme/repo: https://github.com/acme/repo/pull/53 (created)") {
		t.Fatalf("durable result omitted state: %q", output)
	}
	if strings.Contains(pullrequestOutputText(t, manager), token) {
		t.Fatal("GitHub token leaked into durable plugin output")
	}
}

func TestPullrequestGitHubCLICreatesWithAuthoredBody(t *testing.T) {
	const wantURL = "https://github.com/acme/repo/pull/53"
	ghLog := pullrequestCreatingGHStub(t, wantURL)
	pullrequestCommandStub(t, "")
	worktree, _ := pullrequestRepo(t, "ssh://git@github.com/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github"})
	defer cleanup()
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53, "CLI create"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "repo: "+wantURL+" (created)" {
		t.Fatalf("GitHub CLI result = %q", result.Value)
	}
	raw, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(raw)
	if !strings.Contains(commands, "pr create") || !strings.Contains(commands, "--body") || !strings.Contains(commands, "## Summary") {
		t.Fatalf("GitHub CLI create command = %q", commands)
	}
}

func TestPullrequestPMDefaultSelectorAndAggregateMail(t *testing.T) {
	server, _ := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected PM request: %s %s", r.Method, r.URL.Path)
			return
		}
		urls := map[string]string{
			"/repos/acme/one/pulls": "https://github.com/acme/one/pull/1",
			"/repos/acme/two/pulls": "https://github.com/acme/two/pull/2",
		}
		url, ok := urls[r.URL.Path]
		if !ok {
			t.Errorf("unexpected PM create path: %s", r.URL.Path)
			return
		}
		_, _ = io.WriteString(w, `{"html_url":"`+url+`"}`)
	})
	pullrequestFailingGHStub(t)
	commandLog := pullrequestCommandStub(t, "id: 59\ntitle: PM request\nrole: product_manager\ngoal:\nCoordinate both repositories\n")
	apiWorktree, _ := pullrequestRepo(t, "https://github.com/acme/one.git")
	webWorktree, _ := pullrequestRepo(t, "https://github.com/acme/two.git")
	manager, cleanup := loadPullrequest(t, map[string]any{
		"forge": "github", "api_url": server.URL, "token": "pm-token",
	})
	defer cleanup()
	data := pullrequestJobEventWithBody(t, "", "", "", "", 59, "Release both")
	data["repo"] = nil
	data["branch"] = nil
	data["base_branch"] = nil
	data["worktree"] = nil
	data["caller"] = "pm-59"
	data["caller_role"] = "product_manager"
	bodyArg := data["args"].([]string)[0]
	data["args"] = []string{bodyArg, "Release both"}
	data["integration_branches"] = []map[string]any{
		{"repo": "api", "branch": "feature/pullrequest", "base_branch": "main", "worktree": apiWorktree},
		{"repo": "web", "branch": "feature/pullrequest", "base_branch": "main", "worktree": webWorktree},
	}
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "pm-59", "product_manager", []string{"Release both"}, data)
	if err != nil {
		t.Fatal(err)
	}
	want := "api: https://github.com/acme/one/pull/1 (created)\nweb: https://github.com/acme/two/pull/2 (created)"
	if result.Value != want {
		t.Fatalf("PM aggregate result = %q, want %q", result.Value, want)
	}
	raw, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(raw)
	if strings.Count(commands, "send -t user") != 1 || strings.Count(commands, "send -t ceo") != 1 {
		t.Fatalf("PM aggregate notifications = %q", commands)
	}
	if !strings.Contains(commands, "https://github.com/acme/one/pull/1") || !strings.Contains(commands, "https://github.com/acme/two/pull/2") {
		t.Fatalf("PM aggregate notification omitted URL: %q", commands)
	}

	data["args"] = []string{bodyArg, "repo=web", "Selected"}
	selected, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "pm-59", "product_manager", []string{"repo=web", "Selected"}, data)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Value != "web: https://github.com/acme/two/pull/2 (created)" {
		t.Fatalf("PM selected result = %q", selected.Value)
	}
	data["args"] = []string{bodyArg, "repo=missing"}
	_, err = manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "pm-59", "product_manager", []string{"repo=missing"}, data)
	assertPullrequestUsageGuidance(t, err, "valid keys: api, web")
	raw, err = os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "send -t pm-59") {
		t.Fatalf("invalid selector guidance mail = %q", raw)
	}
}

func TestPullrequestPMPromptRequestsOneAggregateAction(t *testing.T) {
	manager, cleanup := loadPullrequest(t, nil)
	defer cleanup()
	var hook loadedHook
	for _, candidate := range manager.hooks {
		if candidate.plugin == "pullrequest" && candidate.hook.Event == EventPromptRender {
			hook = candidate
			break
		}
	}
	if hook.plugin == "" {
		t.Fatal("pullrequest prompt hook not loaded")
	}
	updated, err := manager.runHook(context.Background(), hook, Event{Mutable: true, Data: map[string]any{
		"role": "product_manager", "branch": "", "merge_target": "automerge", "text": "finish the work",
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := updated.Data["text"].(string)
	if !ok || !strings.Contains(text, "merged child `omo job` results") || !strings.Contains(text, "storage") || !strings.Contains(text, "body=<absolute-path>") || !strings.Contains(text, "## Risks and follow-ups") || !strings.Contains(text, "## Jobs") {
		t.Fatalf("PM prompt = %q", text)
	}
}

func TestPullrequestPromptDescriptionGuidanceIsRoleSpecificAndAppendOnce(t *testing.T) {
	manager, cleanup := loadPullrequest(t, nil)
	defer cleanup()
	for _, test := range []struct {
		role string
		want string
	}{
		{role: "product_manager", want: "storage"},
		{role: "developer", want: "worktree or a temporary path"},
		{role: "freelancer", want: "worktree or a temporary path"},
	} {
		t.Run(test.role, func(t *testing.T) {
			data := map[string]any{
				"role": test.role, "branch": "feature/one", "merge_target": "asis", "text": "base prompt",
			}
			first, err := manager.Emit(context.Background(), Event{Name: EventPromptRender, Mutable: true, Data: data})
			if err != nil {
				t.Fatal(err)
			}
			firstText := first.Data["text"].(string)
			if !strings.Contains(firstText, test.want) || !strings.Contains(firstText, "## Summary") || !strings.Contains(firstText, "## What changed") || !strings.Contains(firstText, "## Why") || !strings.Contains(firstText, "## How it was verified") || !strings.Contains(firstText, "body=<absolute-path>") {
				t.Fatalf("%s prompt = %q", test.role, firstText)
			}
			if strings.Count(firstText, "pullrequest-informative-body-v1") != 1 || len(firstText)-len("base prompt") > 2048 {
				t.Fatalf("%s marker/growth = %d/%d", test.role, strings.Count(firstText, "pullrequest-informative-body-v1"), len(firstText)-len("base prompt"))
			}
			data["text"] = firstText
			second, err := manager.Emit(context.Background(), Event{Name: EventPromptRender, Mutable: true, Data: data})
			if err != nil {
				t.Fatal(err)
			}
			if got := second.Data["text"].(string); got != firstText {
				t.Fatalf("%s guidance appended twice", test.role)
			}
		})
	}
}

func TestPullrequestPMRejectsWhenNoAsIsEntriesAreAvailable(t *testing.T) {
	manager, cleanup := loadPullrequest(t, nil)
	defer cleanup()
	data := pullrequestJobEventWithBody(t, "", "", "", "", 59)
	data["repo"] = nil
	data["branch"] = nil
	data["base_branch"] = nil
	data["worktree"] = nil
	data["integration_branches"] = []map[string]any{}
	if _, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "pm-59", "product_manager", nil, data); err == nil || !strings.Contains(err.Error(), "effective repository policy must be asis") {
		t.Fatalf("empty PM policy error = %v", err)
	}
}

func TestPullrequestManualReturnsCreatedURL(t *testing.T) {
	const wantURL = "https://github.com/acme/repo/pull/53"
	server, _ := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.WriteString(w, `{"html_url":"`+wantURL+`"}`)
	})
	pullrequestFailingGHStub(t)
	pullrequestCommandStub(t, "id: 53\ntitle: Add pull request support\ngoal:\ncreate the request\n")
	worktree, _ := pullrequestRepo(t, "https://github.com/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "api_url": server.URL, "token": "test-token"})
	defer cleanup()
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "acme/repo", "feature/pullrequest", "main", 53))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "acme/repo: "+wantURL+" (created)" {
		t.Fatalf("manual result = %q, want %q", result.Value, wantURL)
	}
}

func TestPullrequestForgeAdapters(t *testing.T) {
	cases := []struct {
		name       string
		forge      string
		remote     string
		path       string
		apiSuffix  string
		token      string
		checkQuery func(*testing.T, *http.Request)
	}{
		{
			name:  "forgejo",
			forge: "forgejo", remote: "https://forge.example/acme/repo.git",
			path: "/api/v1/repos/acme/repo/pulls", apiSuffix: "/api/v1", token: "forgejo-secret",
			checkQuery: func(t *testing.T, r *http.Request) {
				if r.URL.Query().Get("state") != "open" || r.URL.Query().Get("head") != "feature/pullrequest" || r.URL.Query().Get("base") != "main" {
					t.Errorf("Forgejo query = %s", r.URL.RawQuery)
				}
			},
		},
		{
			name:  "gitlab",
			forge: "auto", remote: "https://gitlab.com/group/sub/repo.git",
			path: "/api/v4/projects/group%2Fsub%2Frepo/merge_requests", apiSuffix: "/api/v4", token: "gitlab-secret",
			checkQuery: func(t *testing.T, r *http.Request) {
				if r.URL.Query().Get("state") != "opened" || r.URL.Query().Get("source_branch") != "feature/pullrequest" || r.URL.Query().Get("target_branch") != "main" {
					t.Errorf("GitLab query = %s", r.URL.RawQuery)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if test.forge == "forgejo" {
					if r.URL.Path != test.path || r.Header.Get("Authorization") != "token "+test.token {
						t.Errorf("Forgejo request = %s %s auth=%q", r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"))
					}
				} else if r.URL.EscapedPath() != test.path || r.Header.Get("PRIVATE-TOKEN") != test.token {
					t.Errorf("GitLab request = %s %s token=%q", r.Method, r.URL.RequestURI(), r.Header.Get("PRIVATE-TOKEN"))
				}
				if r.Method == http.MethodGet {
					test.checkQuery(t, r)
					if test.forge == "forgejo" {
						_, _ = io.WriteString(w, "[]")
					} else {
						_, _ = io.WriteString(w, "[]")
					}
					return
				}
				if test.forge == "forgejo" {
					var payload map[string]string
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Errorf("decode Forgejo payload: %v", err)
					} else if payload["head"] != "feature/pullrequest" || payload["base"] != "main" || payload["body"] != strings.TrimRight(string(pullrequestValidBody()), " \t\r\n") {
						t.Errorf("Forgejo payload = %#v", payload)
					}
					_, _ = io.WriteString(w, `{"html_url":"https://forge.example/acme/repo/pulls/53"}`)
				} else {
					form, err := url.ParseQuery(string(mustReadBody(t, r)))
					if err != nil || form.Get("source_branch") != "feature/pullrequest" || form.Get("target_branch") != "main" || form.Get("description") != strings.TrimRight(string(pullrequestValidBody()), " \t\r\n") {
						t.Errorf("GitLab form = %v err=%v", form, err)
					}
					_, _ = io.WriteString(w, `{"web_url":"https://gitlab.com/group/sub/repo/-/merge_requests/53"}`)
				}
			})
			pullrequestFailingGHStub(t)
			pullrequestCommandStub(t, "id: 53\ntitle: Adapter test\ngoal:\nexercise adapters\n")
			worktree, bare := pullrequestRepo(t, test.remote)
			manager, cleanup := loadPullrequest(t, map[string]any{
				"forge": test.forge, "api_url": server.URL, "token": test.token, "remote": "origin",
			})
			defer cleanup()
			result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", []string{"Adapter test"}, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53, "Adapter test"))
			if err != nil {
				t.Fatal(err)
			}
			wantURL := "https://forge.example/acme/repo/pulls/53"
			if test.name == "gitlab" {
				wantURL = "https://gitlab.com/group/sub/repo/-/merge_requests/53"
			}
			if result.Value != "repo: "+wantURL+" (created)" {
				t.Fatalf("adapter manual result = %q, want %q", result.Value, wantURL)
			}
			requests := capture.snapshot()
			if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodPost {
				t.Fatalf("adapter requests = %+v", requests)
			}
			if !strings.Contains(runGit(t, bare, "show-ref", "refs/heads/feature/pullrequest"), "refs/heads/feature/pullrequest") {
				t.Fatal("adapter did not push branch")
			}
		})
	}
}

func TestPullrequestExistingOpenAdaptersReturnURLAndNotify(t *testing.T) {
	cases := []struct {
		name       string
		forge      string
		remote     string
		path       string
		token      string
		existing   string
		checkQuery func(*testing.T, *http.Request)
	}{
		{
			name: "github rest", forge: "github", remote: "https://github.com/acme/repo.git",
			path: "/repos/acme/repo/pulls", token: "github-existing-secret", existing: "https://github.com/acme/repo/pull/99",
			checkQuery: func(t *testing.T, r *http.Request) {
				if r.URL.Query().Get("state") != "open" || r.URL.Query().Get("head") != "acme:feature/pullrequest" || r.URL.Query().Get("base") != "main" {
					t.Errorf("GitHub query = %s", r.URL.RawQuery)
				}
			},
		},
		{
			name: "forgejo", forge: "forgejo", remote: "https://forge.example/acme/repo.git",
			path: "/api/v1/repos/acme/repo/pulls", token: "forgejo-existing-secret", existing: "https://forge.example/acme/repo/pulls/99",
			checkQuery: func(t *testing.T, r *http.Request) {
				if r.URL.Query().Get("state") != "open" || r.URL.Query().Get("head") != "feature/pullrequest" || r.URL.Query().Get("base") != "main" {
					t.Errorf("Forgejo query = %s", r.URL.RawQuery)
				}
			},
		},
		{
			name: "gitea", forge: "gitea", remote: "https://gitea.example/acme/repo.git",
			path: "/api/v1/repos/acme/repo/pulls", token: "gitea-existing-secret", existing: "https://gitea.example/acme/repo/pulls/99",
			checkQuery: func(t *testing.T, r *http.Request) {
				if r.URL.Query().Get("state") != "open" || r.URL.Query().Get("head") != "feature/pullrequest" || r.URL.Query().Get("base") != "main" {
					t.Errorf("Gitea query = %s", r.URL.RawQuery)
				}
			},
		},
		{
			name: "gitlab", forge: "gitlab", remote: "https://gitlab.com/group/sub/repo.git",
			path: "/api/v4/projects/group%2Fsub%2Frepo/merge_requests", token: "gitlab-existing-secret", existing: "https://gitlab.com/group/sub/repo/-/merge_requests/99",
			checkQuery: func(t *testing.T, r *http.Request) {
				if r.URL.Query().Get("state") != "opened" || r.URL.Query().Get("source_branch") != "feature/pullrequest" || r.URL.Query().Get("target_branch") != "main" {
					t.Errorf("GitLab query = %s", r.URL.RawQuery)
				}
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if test.forge == "github" {
					if r.Header.Get("Authorization") != "Bearer "+test.token {
						t.Errorf("GitHub auth = %q", r.Header.Get("Authorization"))
					}
				} else if test.forge == "gitlab" {
					if r.Header.Get("PRIVATE-TOKEN") != test.token {
						t.Errorf("GitLab token = %q", r.Header.Get("PRIVATE-TOKEN"))
					}
				} else if r.Header.Get("Authorization") != "token "+test.token {
					t.Errorf("%s auth = %q", test.name, r.Header.Get("Authorization"))
				}
				if r.Method == http.MethodGet {
					if r.URL.EscapedPath() != test.path {
						t.Errorf("existing %s path = %s, want %s", test.name, r.URL.EscapedPath(), test.path)
					}
					test.checkQuery(t, r)
					if test.forge == "gitlab" {
						_, _ = io.WriteString(w, `[{"iid":99,"web_url":"`+test.existing+`"}]`)
					} else {
						_, _ = io.WriteString(w, `[{"number":99,"html_url":"`+test.existing+`"}]`)
					}
					return
				}
				wantBody := strings.TrimRight(string(pullrequestValidBody()), " \t\r\n")
				if test.forge == "gitlab" {
					if r.Method != http.MethodPut || r.URL.EscapedPath() != test.path+"/99" {
						t.Errorf("GitLab update = %s %s", r.Method, r.URL.EscapedPath())
					}
					form, err := url.ParseQuery(string(mustReadBody(t, r)))
					if err != nil || form.Get("description") != wantBody || form.Get("title") != "Updated title" {
						t.Errorf("GitLab update form = %v err=%v", form, err)
					}
				} else {
					if r.Method != http.MethodPatch || r.URL.EscapedPath() != test.path+"/99" {
						t.Errorf("%s update = %s %s", test.name, r.Method, r.URL.EscapedPath())
					}
					var payload map[string]string
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["body"] != wantBody || payload["title"] != "Updated title" {
						t.Errorf("%s update payload = %#v err=%v", test.name, payload, err)
					}
				}
				_, _ = io.WriteString(w, `{"html_url":"`+test.existing+`"}`)
			})
			if test.forge == "github" {
				pullrequestFailingGHStub(t)
			}
			commandLog := pullrequestCommandStub(t, "id: 53\ntitle: Existing request\ngoal:\nexercise existing lookup\n")
			worktree, bare := pullrequestRepo(t, test.remote)
			manager, cleanup := loadPullrequest(t, map[string]any{"forge": test.forge, "api_url": server.URL, "token": test.token})
			defer cleanup()
			result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53, "Updated title"))
			if err != nil {
				t.Fatal(err)
			}
			if result.Value != "repo: "+test.existing+" (updated)" {
				t.Fatalf("existing %s result = %q, want %q", test.name, result.Value, test.existing)
			}
			requests := capture.snapshot()
			if len(requests) != 2 || requests[0].Method != http.MethodGet {
				t.Fatalf("existing %s requests = %+v, want GET and update", test.name, requests)
			}
			commandOutput, err := os.ReadFile(commandLog)
			if err != nil {
				t.Fatal(err)
			}
			commands := string(commandOutput)
			if !strings.Contains(commands, "send -t user") || !strings.Contains(commands, "send -t ceo") || !strings.Contains(commands, test.existing) {
				t.Fatalf("existing %s notifications = %q", test.name, commands)
			}
			if strings.Contains(pullrequestOutputText(t, manager), test.token) {
				t.Fatalf("existing %s token leaked into durable plugin output", test.name)
			}
			if !strings.Contains(runGit(t, bare, "show-ref", "refs/heads/feature/pullrequest"), "refs/heads/feature/pullrequest") {
				t.Fatalf("existing %s did not push branch", test.name)
			}
		})
	}
}

func TestPullrequestExistingUpdateWithoutTitlePreservesTitle(t *testing.T) {
	const existingURL = "https://github.com/acme/repo/pull/99"
	server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `[{"number":99,"html_url":"`+existingURL+`"}]`)
			return
		}
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/acme/repo/pulls/99" {
			t.Fatalf("update request = %s %s", r.Method, r.URL.Path)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["body"] != strings.TrimRight(string(pullrequestValidBody()), " \t\r\n") {
			t.Fatalf("updated body = %q", payload["body"])
		}
		if _, exists := payload["title"]; exists {
			t.Fatalf("omitted title was overwritten: %#v", payload)
		}
		_, _ = io.WriteString(w, `{"html_url":"`+existingURL+`"}`)
	})
	pullrequestFailingGHStub(t)
	pullrequestCommandStub(t, "")
	worktree, _ := pullrequestRepo(t, "https://github.com/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "api_url": server.URL, "token": "test-token"})
	defer cleanup()
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "repo: "+existingURL+" (updated)" {
		t.Fatalf("result = %q", result.Value)
	}
	if requests := capture.snapshot(); len(requests) != 2 || requests[1].Method != http.MethodPatch {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestPullrequestGitHubCLIExistingIsIdempotent(t *testing.T) {
	const existingURL = "https://github.com/acme/repo/pull/99"
	commandLog := pullrequestCommandStub(t, "id: 53\ntitle: CLI adapter\ngoal:\nexercise gh\n")
	ghLog := pullrequestAuthenticatedGHStub(t, existingURL)
	worktree, bare := pullrequestRepo(t, "ssh://git@github.com/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "token": "unused"})
	defer cleanup()
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", []string{"CLI adapter"}, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53, "CLI adapter"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "repo: "+existingURL+" (updated)" {
		t.Fatalf("GitHub CLI existing result = %q, want %q", result.Value, existingURL)
	}
	ghOutput, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(ghOutput)
	if !strings.Contains(commands, "auth status") || !strings.Contains(commands, "pr list") || !strings.Contains(commands, "--head feature/pullrequest") || !strings.Contains(commands, "--base main") || !strings.Contains(commands, "pr edit") || !strings.Contains(commands, "--body") || !strings.Contains(commands, "--title CLI adapter") {
		t.Fatalf("gh lookup commands = %q", commands)
	}
	if strings.Contains(commands, "pr create") {
		t.Fatalf("gh created a duplicate request: %q", commands)
	}
	commandOutput, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(commandOutput); !strings.Contains(got, "https://github.com/acme/repo/pull/99") || !strings.Contains(got, "send -t user") || !strings.Contains(got, "send -t ceo") {
		t.Fatalf("idempotent notifications = %q", got)
	}
	if !strings.Contains(runGit(t, bare, "show-ref", "refs/heads/feature/pullrequest"), "refs/heads/feature/pullrequest") {
		t.Fatal("CLI path did not push branch")
	}
}

func TestPullrequestAutoDetectsForgejoAndUsesTokenEnv(t *testing.T) {
	const token = "forgejo-env-secret"
	server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			_, _ = io.WriteString(w, `{}`)
			return
		}
		if r.Header.Get("Authorization") != "token "+token {
			t.Errorf("Forgejo token header = %q", r.Header.Get("Authorization"))
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.WriteString(w, `{"html_url":"https://forge.example/acme/repo/pulls/53"}`)
	})
	t.Setenv("PULLREQUEST_FORGE_TOKEN", token)
	pullrequestCommandStub(t, "id: 53\ntitle: Auto forge\ngoal:\nprobe and create\n")
	worktree, _ := pullrequestRepo(t, "https://forge.example/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{
		"forge": "auto", "api_url": server.URL, "token_env": "PULLREQUEST_FORGE_TOKEN",
	})
	defer cleanup()
	if err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53)); err != nil {
		t.Fatal(err)
	}
	requests := capture.snapshot()
	if len(requests) != 3 || requests[0].Path != "/api/v1/version" || requests[1].Method != http.MethodGet || requests[2].Method != http.MethodPost {
		t.Fatalf("auto Forgejo requests = %+v", requests)
	}
	if strings.Contains(pullrequestOutputText(t, manager), token) {
		t.Fatal("token_env secret leaked into durable plugin output")
	}
}

func TestPullrequestProviderFailureRedactsSecret(t *testing.T) {
	const token = "github-provider-secret"
	server, _ := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"invalid token github-provider-secret"}`)
	})
	pullrequestFailingGHStub(t)
	pullrequestCommandStub(t, "id: 53\ntitle: Failure\ngoal:\nredaction\n")
	worktree, _ := pullrequestRepo(t, "https://github.com/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "api_url": server.URL, "token": token})
	defer cleanup()
	err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53))
	if err == nil || strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "GitHub pull request lookup failed") {
		t.Fatalf("provider failure = %v", err)
	}
	if strings.Contains(pullrequestOutputText(t, manager), token) {
		t.Fatal("provider secret leaked into durable plugin output")
	}
}

func TestPullrequestManualRequiresJobMetadata(t *testing.T) {
	manager, cleanup := loadPullrequest(t, map[string]any{})
	defer cleanup()
	bodyPath := pullrequestBodyFile(t, pullrequestValidBody())
	err := runPullrequestManual(t, manager, map[string]any{
		"caller":      "user",
		"caller_role": "user",
		"args":        []any{"body=" + bodyPath, "title"},
	})
	if err == nil || !strings.Contains(err.Error(), "job metadata") {
		t.Fatalf("missing metadata error = %v", err)
	}
}

func TestPullrequestRemoteURLForms(t *testing.T) {
	server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path != "/repos/acme/repo/pulls" {
			t.Errorf("remote-form request = %s %s", r.Method, r.URL.RequestURI())
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
		} else {
			_, _ = io.WriteString(w, `{"html_url":"https://github.com/acme/repo/pull/53"}`)
		}
	})
	forms := []string{
		"https://github.com/acme/repo.git",
		"ssh://git@github.com/acme/repo.git",
		"git@github.com:acme/repo.git",
	}
	for _, remote := range forms {
		t.Run(remote, func(t *testing.T) {
			pullrequestFailingGHStub(t)
			pullrequestCommandStub(t, "id: 53\ntitle: Remote form\ngoal:\nremote parsing\n")
			worktree, _ := pullrequestRepo(t, remote)
			manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "api_url": server.URL, "token": "remote-token"})
			defer cleanup()
			before := len(capture.snapshot())
			if err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53, "Remote form")); err != nil {
				t.Fatal(err)
			}
			after := capture.snapshot()
			if len(after) != before+2 {
				t.Fatalf("remote form request count grew from %d to %d", before, len(after))
			}
		})
	}
}

func TestPullrequestRejectsMultipleTitleArguments(t *testing.T) {
	manager, cleanup := loadPullrequest(t, map[string]any{})
	defer cleanup()
	err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, "/tmp/worktree", "repo", "branch", "main", 53, "one", "two"))
	if err == nil || !strings.Contains(err.Error(), "at most one title") {
		t.Fatalf("multiple title error = %v", err)
	}
}

func mustReadBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestPullrequestManifestDeclaresContract(t *testing.T) {
	manifest, err := ReadManifest(pullrequestSourceDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Name != "pullrequest" || manifest.Version != "1.0.0" || manifest.Description == "" {
		t.Fatalf("manifest identity = %+v", manifest)
	}
	wantDefaults := map[string]string{
		"remote":    "origin",
		"forge":     "auto",
		"api_url":   "",
		"token":     "",
		"token_env": "",
		"instruct":  "true",
	}
	for key, want := range wantDefaults {
		if got := fmt.Sprint(manifest.DefaultConfig[key]); got != want {
			t.Fatalf("default_config[%q] = %#v, want %#v", key, manifest.DefaultConfig[key], want)
		}
	}
	hosts, ok := manifest.DefaultConfig["gitlab_hosts"].([]any)
	if !ok || len(hosts) != 0 {
		t.Fatalf("gitlab_hosts default = %#v", manifest.DefaultConfig["gitlab_hosts"])
	}
	servers, ok := manifest.DefaultConfig["servers"].([]any)
	if !ok || len(servers) != 0 {
		t.Fatalf("servers default = %#v", manifest.DefaultConfig["servers"])
	}
	if len(manifest.Hooks) != 2 {
		t.Fatalf("hooks = %+v", manifest.Hooks)
	}
	if manifest.Hooks[0].Event != EventPromptRender || manifest.Hooks[0].Lua != "prompt.lua" {
		t.Fatalf("prompt hook = %+v", manifest.Hooks[0])
	}
	create := manifest.Hooks[1]
	if create.Event != EventManual || create.Name != "create" || create.Lua != "create.lua" || !create.ManualArgs {
		t.Fatalf("manual hook = %+v", create)
	}
	if strings.Join(create.Roles, ",") != "user,ceo,product_manager,developer,freelancer" {
		t.Fatalf("manual roles = %v", create.Roles)
	}
}

func TestPullrequestServerConfigValidation(t *testing.T) {
	cases := []struct {
		name  string
		entry any
		field string
	}{
		{name: "empty host", entry: map[string]any{"host": "   "}, field: "servers[1].host"},
		{name: "duplicate host", entry: []any{
			map[string]any{"host": "Forge.Example"},
			map[string]any{"host": " forge.example "},
		}, field: "servers[2].host"},
		{name: "invalid forge", entry: map[string]any{"host": "forge.example", "forge": "bitbucket"}, field: "servers[1].forge"},
		{name: "non-string host", entry: map[string]any{"host": 123}, field: "servers[1].host"},
		{name: "non-string forge", entry: map[string]any{"host": "forge.example", "forge": true}, field: "servers[1].forge"},
		{name: "non-string api url", entry: map[string]any{"host": "forge.example", "api_url": 42}, field: "servers[1].api_url"},
		{name: "non-string token", entry: map[string]any{"host": "forge.example", "token": []any{}}, field: "servers[1].token"},
		{name: "non-string token env", entry: map[string]any{"host": "forge.example", "token_env": map[string]any{}}, field: "servers[1].token_env"},
		{name: "non-string remote", entry: map[string]any{"host": "forge.example", "remote": false}, field: "servers[1].remote"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("invalid server config reached provider: %s %s", r.Method, r.URL.Path)
			})
			pullrequestFailingGHStub(t)
			commandLog := pullrequestCommandStub(t, "")
			worktree, bare := pullrequestRepo(t, "https://github.com/acme/repo.git")
			servers := test.entry
			if entries, ok := test.entry.([]any); ok {
				servers = entries
			} else {
				servers = []any{test.entry}
			}
			manager, cleanup := loadPullrequest(t, map[string]any{
				"forge": "github", "api_url": server.URL, "token": "flat-secret", "token_env": "PULLREQUEST_SERVER_TOKEN",
				"servers": servers,
			})
			defer cleanup()
			data := pullrequestJobEventWithBody(t, worktree, "acme/repo", "feature/pullrequest", "main", 53, "Validation")
			err := runPullrequestManual(t, manager, data)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("validation error = %v, want indexed field %s", err, test.field)
			}
			if requests := capture.snapshot(); len(requests) != 0 {
				t.Fatalf("invalid server config made provider requests: %+v", requests)
			}
			showRef := exec.Command("git", "show-ref", "refs/heads/feature/pullrequest")
			showRef.Dir = bare
			if output, showErr := showRef.CombinedOutput(); showErr == nil {
				t.Fatalf("invalid server config pushed branch: %s", output)
			}
			commandOutput, readErr := os.ReadFile(commandLog)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			if strings.Contains(string(commandOutput), "send -t ") {
				t.Fatalf("invalid server config sent notification: %q", commandOutput)
			}
		})
	}
}

func TestPullrequestServerMatchAndTokenPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		serverTok string
		envTok    string
	}{
		{name: "token env", envTok: "server-env-token"},
		{name: "token wins", serverTok: "server-token", envTok: "ignored-env-token"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "token "+map[bool]string{true: test.serverTok, false: test.envTok}[test.serverTok != ""] {
					t.Errorf("server token = %q", got)
				}
				if r.Method == http.MethodGet {
					_, _ = io.WriteString(w, "[]")
					return
				}
				_, _ = io.WriteString(w, `{"html_url":"https://forge.example/acme/repo/pulls/53"}`)
			})
			if test.envTok != "" {
				t.Setenv("PULLREQUEST_SERVER_TOKEN", test.envTok)
			}
			pullrequestFailingGHStub(t)
			pullrequestCommandStub(t, "")
			worktree, _ := pullrequestRepo(t, "ssh://git@FORGE.EXAMPLE:2222/acme/repo.git")
			entry := map[string]any{
				"host": "  forge.example ", "forge": "forgejo", "api_url": server.URL,
				"token": test.serverTok, "token_env": "PULLREQUEST_SERVER_TOKEN",
			}
			manager, cleanup := loadPullrequest(t, map[string]any{
				"forge": "github", "api_url": "https://flat.invalid", "token": "flat-secret",
				"servers": []any{entry},
			})
			defer cleanup()
			if err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53, "Server match")); err != nil {
				t.Fatal(err)
			}
			if requests := capture.snapshot(); len(requests) != 2 {
				t.Fatalf("server requests = %+v", requests)
			}
		})
	}
}

func TestPullrequestFlatFallbackForUnmatchedServer(t *testing.T) {
	const flatToken = "flat-fallback-token"
	server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+flatToken {
			t.Errorf("flat fallback token = %q", got)
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.WriteString(w, `{"html_url":"https://github.example/acme/repo/pull/53"}`)
	})
	pullrequestFailingGHStub(t)
	pullrequestCommandStub(t, "")
	worktree, _ := pullrequestRepo(t, "https://github.example/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{
		"forge": "github", "api_url": server.URL, "token": flatToken,
		"servers": []any{map[string]any{"host": "other.example", "forge": "forgejo", "token": "server-token"}},
	})
	defer cleanup()
	if err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53, "Flat fallback")); err != nil {
		t.Fatal(err)
	}
	if requests := capture.snapshot(); len(requests) != 2 {
		t.Fatalf("flat fallback requests = %+v", requests)
	}
}

func TestPullrequestMatchedServerDoesNotInheritFlatToken(t *testing.T) {
	server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("matched server without token reached provider: %s %s", r.Method, r.URL.Path)
	})
	pullrequestFailingGHStub(t)
	commandLog := pullrequestCommandStub(t, "")
	worktree, _ := pullrequestRepo(t, "https://github.example/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{
		"forge": "github", "api_url": server.URL, "token": "flat-secret",
		"servers": []any{map[string]any{"host": "github.example", "forge": "github", "api_url": server.URL}},
	})
	defer cleanup()
	err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53, "No inherited token"))
	if err == nil || !strings.Contains(err.Error(), "GitHub token is not configured") {
		t.Fatalf("matched empty credentials error = %v", err)
	}
	if len(capture.snapshot()) != 0 {
		t.Fatal("matched empty credentials made provider requests")
	}
	if raw, readErr := os.ReadFile(commandLog); readErr == nil && strings.Contains(string(raw), "flat-secret") {
		t.Fatal("flat token appeared in command capture")
	}
}

func TestPullrequestServerTokenRedaction(t *testing.T) {
	const token = "server-redaction-secret"
	server, _ := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"invalid token `+token+`"}`)
	})
	pullrequestFailingGHStub(t)
	commandLog := pullrequestCommandStub(t, "")
	worktree, _ := pullrequestRepo(t, "https://forge.example/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{
		"forge": "github", "api_url": "https://flat.invalid", "token": "flat-secret",
		"servers": []any{map[string]any{"host": "forge.example", "forge": "github", "api_url": server.URL, "token": token}},
	})
	defer cleanup()
	err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53, "Redaction"))
	if err == nil || strings.Contains(err.Error(), token) {
		t.Fatalf("server token appeared in returned error: %v", err)
	}
	for name, output := range map[string]string{
		"durable output": pullrequestOutputText(t, manager),
		"command/mail capture": func() string {
			raw, readErr := os.ReadFile(commandLog)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			return string(raw)
		}(),
	} {
		if strings.Contains(output, token) {
			t.Fatalf("server token leaked into %s: %q", name, output)
		}
	}
}

func TestPullrequestPMDifferentServers(t *testing.T) {
	const firstToken = "first-server-token"
	const secondToken = "second-server-token"
	firstServer, firstCapture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+firstToken {
			t.Errorf("first server token = %q", r.Header.Get("Authorization"))
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.WriteString(w, `{"html_url":"https://first.example/acme/one/pull/1"}`)
	})
	secondServer, secondCapture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secondToken {
			t.Errorf("second server token = %q", r.Header.Get("Authorization"))
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.WriteString(w, `{"html_url":"https://second.example/acme/two/pull/2"}`)
	})
	pullrequestFailingGHStub(t)
	commandLog := pullrequestCommandStub(t, "")
	firstWorktree, _ := pullrequestRepo(t, "https://first.example/acme/one.git")
	secondWorktree, _ := pullrequestRepo(t, "https://second.example/acme/two.git")
	manager, cleanup := loadPullrequest(t, map[string]any{
		"forge": "github", "api_url": "https://flat.invalid", "token": "flat-secret",
		"servers": []any{
			map[string]any{"host": "first.example", "forge": "github", "api_url": firstServer.URL, "token": firstToken},
			map[string]any{"host": "second.example", "forge": "github", "api_url": secondServer.URL, "token": secondToken},
		},
	})
	defer cleanup()
	data := pullrequestJobEventWithBody(t, "", "", "", "", 59, "Release both")
	data["repo"], data["branch"], data["base_branch"], data["worktree"] = nil, nil, nil, nil
	data["caller"], data["caller_role"] = "pm-59", "product_manager"
	data["integration_branches"] = []map[string]any{
		{"repo": "api", "branch": "feature/pullrequest", "base_branch": "main", "worktree": firstWorktree},
		{"repo": "web", "branch": "feature/pullrequest", "base_branch": "main", "worktree": secondWorktree},
	}
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "pm-59", "product_manager", []string{"Release both"}, data)
	if err != nil {
		t.Fatal(err)
	}
	want := "api: https://first.example/acme/one/pull/1 (created)\nweb: https://second.example/acme/two/pull/2 (created)"
	if result.Value != want {
		t.Fatalf("PM multi-server result = %q, want %q", result.Value, want)
	}
	if len(firstCapture.snapshot()) != 2 || len(secondCapture.snapshot()) != 2 {
		t.Fatalf("PM multi-server requests = first=%+v second=%+v", firstCapture.snapshot(), secondCapture.snapshot())
	}
	raw, readErr := os.ReadFile(commandLog)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(raw), want) {
		t.Fatalf("PM multi-server notifications = %q", raw)
	}
}

func TestPullrequestServerRemoteOverride(t *testing.T) {
	server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/override-owner/override-repo/pulls" {
			t.Errorf("remote override path = %s", r.URL.Path)
		}
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.WriteString(w, `{"html_url":"https://github.example/override-owner/override-repo/pull/53"}`)
	})
	pullrequestFailingGHStub(t)
	pullrequestCommandStub(t, "")
	worktree, bare := pullrequestRepo(t, "ssh://git@SOURCE.EXAMPLE:2222/acme/source.git")
	runGit(t, worktree, "remote", "add", "target", "https://github.example/override-owner/override-repo.git")
	runGit(t, worktree, "remote", "set-url", "--push", "target", "file://"+bare)
	manager, cleanup := loadPullrequest(t, map[string]any{
		"forge": "github", "api_url": "https://flat.invalid", "token": "flat-secret",
		"servers": []any{
			map[string]any{"host": "source.example", "forge": "github", "api_url": server.URL, "token": "server-token", "remote": "target"},
			map[string]any{"host": "other.example", "remote": "missing"},
		},
	})
	defer cleanup()
	if err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 53, "Remote override")); err != nil {
		t.Fatal(err)
	}
	if requests := capture.snapshot(); len(requests) != 2 {
		t.Fatalf("remote override requests = %+v", requests)
	}
	if !strings.Contains(runGit(t, bare, "show-ref", "refs/heads/feature/pullrequest"), "refs/heads/feature/pullrequest") {
		t.Fatal("remote override did not push the named remote")
	}
}

func TestPullrequestPromptInstructionGates(t *testing.T) {
	cases := []struct {
		name        string
		role        string
		mergeTarget string
		branch      string
		instruct    bool
		wantNote    bool
	}{
		{name: "developer asis", role: "developer", mergeTarget: "asis", branch: "feature/one", instruct: true, wantNote: true},
		{name: "product manager asis", role: "product_manager", mergeTarget: "asis", branch: "pm/one", instruct: true, wantNote: true},
		{name: "freelancer asis", role: "freelancer", mergeTarget: "asis", branch: "research/one", instruct: true, wantNote: true},
		{name: "automerge", role: "developer", mergeTarget: "automerge", branch: "feature/one", instruct: true},
		{name: "disabled", role: "developer", mergeTarget: "asis", branch: "feature/one", instruct: false},
		{name: "unsupported role", role: "ceo", mergeTarget: "asis", branch: "feature/one", instruct: true},
		{name: "missing branch", role: "developer", mergeTarget: "asis", branch: "", instruct: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			manager, cleanup := loadPullrequest(t, map[string]any{"instruct": test.instruct})
			defer cleanup()
			result, err := manager.Emit(context.Background(), Event{
				Name: EventPromptRender, Mutable: true,
				Data: map[string]any{
					"role":         test.role,
					"agent":        test.role + "-ada",
					"job_id":       int64(53),
					"text":         "base prompt",
					"merge_target": test.mergeTarget,
					"repo":         "one-man-office-tui",
					"branch":       test.branch,
					"base_branch":  "main",
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			text := result.Data["text"].(string)
			if strings.Contains(text, "omo plugin trigger pullrequest create") != test.wantNote {
				t.Fatalf("instruction presence = %v, want %v; text=%q", strings.Contains(text, "omo plugin trigger pullrequest create"), test.wantNote, text)
			}
			if test.wantNote && len(text)-len("base prompt") > 2048 {
				t.Fatalf("instruction grew prompt by %d bytes", len(text)-len("base prompt"))
			}
		})
	}
}
