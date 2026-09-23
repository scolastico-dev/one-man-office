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
			if len(capture.snapshot()) != 3 {
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

func pullrequestGitHubSHAListStub(t *testing.T, list string) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(bin, "gh.log")
	script := filepath.Join(bin, "gh")
	contents := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$PULLREQUEST_GH_LOG\"\n" +
		"if [ \"$1\" = auth ] && [ \"$2\" = status ]; then exit 0; fi\n" +
		"if [ \"$1\" = pr ] && [ \"$2\" = list ]; then printf '%s' \"$PULLREQUEST_GH_LIST\"; exit 0; fi\n" +
		"printf 'unexpected gh command\\n' >&2; exit 1\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULLREQUEST_GH_LOG", logPath)
	t.Setenv("PULLREQUEST_GH_LIST", list)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func pullrequestGitHubSHAListsStub(t *testing.T, first, second string) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(bin, "gh.log")
	statePath := filepath.Join(bin, "gh.state")
	script := filepath.Join(bin, "gh")
	contents := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> \"$PULLREQUEST_GH_LOG\"\n" +
		"if [ \"$1\" = auth ] && [ \"$2\" = status ]; then exit 0; fi\n" +
		"if [ \"$1\" = pr ] && [ \"$2\" = list ]; then if [ -f \"$PULLREQUEST_GH_STATE\" ]; then printf '%s' \"$PULLREQUEST_GH_SECOND\"; else : > \"$PULLREQUEST_GH_STATE\"; printf '%s' \"$PULLREQUEST_GH_FIRST\"; fi; exit 0; fi\n" +
		"printf 'unexpected gh command\\n' >&2; exit 1\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULLREQUEST_GH_LOG", logPath)
	t.Setenv("PULLREQUEST_GH_STATE", statePath)
	t.Setenv("PULLREQUEST_GH_FIRST", first)
	t.Setenv("PULLREQUEST_GH_SECOND", second)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func pullrequestGitHubNoopStub(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(bin, "gh.log")
	script := filepath.Join(bin, "gh")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PULLREQUEST_GH_LOG\"\nprintf 'provider should not be called\\n' >&2\nexit 1\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULLREQUEST_GH_LOG", logPath)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func pullrequestGitHubListFailureStub(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(bin, "gh.log")
	script := filepath.Join(bin, "gh")
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PULLREQUEST_GH_LOG\"\nif [ \"$1\" = auth ] && [ \"$2\" = status ]; then exit 0; fi\nif [ \"$1\" = pr ] && [ \"$2\" = list ]; then printf '%s\\n' \"${PULLREQUEST_GH_FAILURE:-list failed}\" >&2; exit 1; fi\nexit 1\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULLREQUEST_GH_LOG", logPath)
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

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
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
			if r.URL.Path != "/repos/acme/repo/pulls" || r.URL.Query().Get("state") != "open" {
				t.Errorf("unexpected GitHub list request: %s", r.URL.RequestURI())
			}
			if r.URL.Query().Get("head") != "" && (r.URL.Query().Get("head") != "acme:feature/pullrequest" || r.URL.Query().Get("base") != "main") {
				t.Errorf("unexpected GitHub named-branch query: %s", r.URL.RequestURI())
			}
			if r.URL.Query().Get("head") == "" && r.URL.Query().Get("base") != "" {
				t.Errorf("unexpected GitHub SHA query: %s", r.URL.RequestURI())
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
	if len(requests) != 3 {
		t.Fatalf("GitHub request count = %d, want branch list, SHA list, and create", len(requests))
	}
	if requests[0].Method != http.MethodGet || requests[1].Method != http.MethodGet || requests[2].Method != http.MethodPost {
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
					if test.forge == "forgejo" && r.URL.Query().Get("head") == "" {
						if r.URL.Query().Get("state") != "open" || r.URL.Query().Get("base") != "" {
							t.Errorf("Forgejo SHA query = %s", r.URL.RawQuery)
						}
					} else {
						test.checkQuery(t, r)
					}
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
			wantRequests := 2
			if test.forge == "forgejo" {
				wantRequests = 3
			}
			if len(requests) != wantRequests || requests[0].Method != http.MethodGet || (test.forge == "forgejo" && requests[1].Method != http.MethodGet) || requests[len(requests)-1].Method != http.MethodPost {
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

func TestPullrequestGitHubCLIExistingHeadCommitOnAnotherBranchIsExistingWithoutSideEffects(t *testing.T) {
	const existingURL = "https://github.com/acme/repo/pull/167"
	worktree, bare := pullrequestRepo(t, "ssh://git@github.com/acme/repo.git")
	head := runGit(t, worktree, "rev-parse", "HEAD")
	runGit(t, worktree, "branch", "fix/firefighter-coordinate-before-resolve")
	ghLog := pullrequestGitHubSHAListStub(t, fmt.Sprintf(`[{"url":%q,"number":167,"headRefOid":%q,"headRefName":"omo/job-pm-92-fix/firefighter-done-after-resolve"}]`, existingURL, head))
	pullrequestCommandStub(t, "")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "token": "unused"})
	defer cleanup()

	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "repo", "fix/firefighter-coordinate-before-resolve", "main", 127))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "repo: "+existingURL+" (existing)" {
		t.Fatalf("existing content match result = %#v", result.Value)
	}
	if output := string(mustReadFile(t, ghLog)); strings.Contains(output, "pr edit") || strings.Contains(output, "pr create") {
		t.Fatalf("content match changed another branch's request: %q", output)
	}
	if _, err := os.Stat(filepath.Join(bare, "refs", "heads", "fix", "firefighter-coordinate-before-resolve")); !os.IsNotExist(err) {
		t.Fatalf("content match pushed a duplicate branch: %v", err)
	}
}

func TestPullrequestNoChangesSucceedsWithoutPushOrProvider(t *testing.T) {
	worktree, bare := pullrequestRepo(t, "https://github.com/acme/repo.git")
	runGit(t, worktree, "reset", "--hard", "main")
	ghLog := pullrequestGitHubNoopStub(t)
	pullrequestCommandStub(t, "")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github"})
	defer cleanup()

	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 127))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "repo: no changes on feature/pullrequest; nothing to open" {
		t.Fatalf("no-change result = %#v", result.Value)
	}
	if _, err := os.Stat(filepath.Join(bare, "refs", "heads", "feature", "pullrequest")); !os.IsNotExist(err) {
		t.Fatalf("no-change branch was pushed: %v", err)
	}
	output, readErr := os.ReadFile(ghLog)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	if string(output) != "" {
		t.Fatalf("no-change invoked provider: %q", output)
	}
}

func TestPullrequestMixedResultsKeepNoChangeLineAndFilterStructuredRecords(t *testing.T) {
	server, _ := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.WriteString(w, `{"html_url":"https://github.com/acme/changed/pull/127"}`)
	})
	pullrequestFailingGHStub(t)
	pullrequestCommandStub(t, "")
	changedWorktree, _ := pullrequestRepo(t, "https://github.com/acme/changed.git")
	noChangeWorktree, _ := pullrequestRepo(t, "https://github.com/acme/unchanged.git")
	runGit(t, noChangeWorktree, "reset", "--hard", "main")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "api_url": server.URL, "token": "test-token"})
	defer cleanup()

	bodyPath := pullrequestBodyFile(t, pullrequestValidBody())
	data := map[string]any{
		"plugin": "pullrequest", "action": "create", "caller": "pm-127", "caller_role": "product_manager",
		"args": []any{"body=" + bodyPath, "Mixed"}, "job_id": int64(127),
		"integration_branches": []map[string]any{
			{"repo": "changed", "branch": "feature/pullrequest", "base_branch": "main", "worktree": changedWorktree},
			{"repo": "unchanged", "branch": "feature/pullrequest", "base_branch": "main", "worktree": noChangeWorktree},
		},
	}
	updated, _, err := manager.runHookResult(context.Background(), pullrequestManualHook(t, manager), Event{Name: EventManual, Data: data, Mutable: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Data["result"]; got != "changed: https://github.com/acme/changed/pull/127 (created)\nunchanged: no changes on feature/pullrequest; nothing to open" {
		t.Fatalf("mixed result = %#v", got)
	}
	items, ok := updated.Data["_omo_pull_requests"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("structured pull requests = %#v", updated.Data["_omo_pull_requests"])
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["repo"] != "changed" || item["url"] != "https://github.com/acme/changed/pull/127" || item["state"] != "created" || item["branch"] != "feature/pullrequest" || item["base_branch"] != "main" || item["title"] != "Mixed" {
		t.Fatalf("structured pull request item = %#v", items[0])
	}
}

func TestPullrequestPushesNamedIntegrationBranchWhenWorktreeDiffers(t *testing.T) {
	server, _ := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "[]")
			return
		}
		_, _ = io.WriteString(w, `{"html_url":"https://github.com/acme/repo/pull/127"}`)
	})
	pullrequestFailingGHStub(t)
	pullrequestCommandStub(t, "")
	worktree, bare := pullrequestRepo(t, "https://github.com/acme/repo.git")
	runGit(t, worktree, "branch", "integration/coordinate", "main")
	runGit(t, worktree, "checkout", "integration/coordinate")
	if err := os.WriteFile(filepath.Join(worktree, "integration.txt"), []byte("integration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "add", "integration.txt")
	runGit(t, worktree, "commit", "-m", "integration change")
	runGit(t, worktree, "checkout", "feature/pullrequest")
	head := runGit(t, worktree, "rev-parse", "HEAD")
	integrationHead := runGit(t, worktree, "rev-parse", "integration/coordinate")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "api_url": server.URL, "token": "test-token"})
	defer cleanup()

	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "repo", "integration/coordinate", "main", 127))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "repo: https://github.com/acme/repo/pull/127 (created)" {
		t.Fatalf("integration branch result = %#v", result.Value)
	}
	if got := runGit(t, bare, "rev-parse", "refs/heads/integration/coordinate"); got != integrationHead || got == head {
		t.Fatalf("integration branch points at %s, want named branch %s and not current HEAD %s", got, integrationHead, head)
	}
}

func TestPullrequestGitDiagnosticsIncludeCommandAndCapturedOutput(t *testing.T) {
	pullrequestCommandStub(t, "")
	worktree, _ := pullrequestRepo(t, "https://github.com/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{"remote": "missing"})
	defer cleanup()

	err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 127))
	if err == nil || !strings.Contains(err.Error(), "git -C "+worktree+" remote get-url missing") || !strings.Contains(err.Error(), "No such remote") {
		t.Fatalf("Git diagnostic = %v", err)
	}
}

func TestPullrequestGitHubCLIDiagnosticsIncludeCommandAndCapturedOutput(t *testing.T) {
	ghLog := pullrequestGitHubListFailureStub(t)
	const secret = "gh-secret-token"
	t.Setenv("PULLREQUEST_GH_FAILURE", "list failed: Authorization: Bearer "+secret+" https://user:"+secret+"@github.example/repo")
	pullrequestCommandStub(t, "")
	worktree, _ := pullrequestRepo(t, "ssh://git@github.com/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "token": secret})
	defer cleanup()

	err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 127))
	if err == nil || !strings.Contains(err.Error(), "gh pr list") || !strings.Contains(err.Error(), "list failed") || strings.Contains(err.Error(), secret) {
		t.Fatalf("GitHub CLI diagnostic = %v (commands %q)", err, mustReadFile(t, ghLog))
	}
	if output := pullrequestOutputText(t, manager); strings.Contains(output, secret) {
		t.Fatalf("GitHub CLI secret leaked into durable output: %q", output)
	}
}

func TestPullrequestCommandDiagnosticsRedactMixedCaseAuthorizationHeaders(t *testing.T) {
	ghLog := pullrequestGitHubListFailureStub(t)
	const basicSecret = "basic-secret-127"
	const arbitrarySecret = "weird-secret-127"
	const urlSecret = "url-secret-127"
	const tokenSecret = "token-secret-127"
	t.Setenv("PULLREQUEST_GH_FAILURE", "list failed: keep-stderr aUtHoRiZaTiOn: bAsIc "+basicSecret+" and AUTHORIZATION: Weird "+arbitrarySecret+" https://user:"+urlSecret+"@example.test/repo token: "+tokenSecret+" trailing-stderr")
	pullrequestCommandStub(t, "")
	worktree, _ := pullrequestRepo(t, "ssh://git@github.com/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github"})
	defer cleanup()

	err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 127))
	if err == nil || !strings.Contains(err.Error(), "gh pr list") || !strings.Contains(err.Error(), "keep-stderr") || !strings.Contains(err.Error(), "trailing-stderr") {
		t.Fatalf("mixed-case authorization diagnostic = %v (commands %q)", err, mustReadFile(t, ghLog))
	}
	for _, secret := range []string{basicSecret, arbitrarySecret, urlSecret, tokenSecret} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("secret %q leaked into mixed-case authorization diagnostic: %v", secret, err)
		}
		if output := pullrequestOutputText(t, manager); strings.Contains(output, secret) {
			t.Fatalf("secret %q leaked into durable output: %q", secret, output)
		}
	}
}

func TestPullrequestGitDiagnosticsRedactRemoteCredentials(t *testing.T) {
	const secret = "git-secret-token"
	pullrequestCommandStub(t, "")
	worktree, _ := pullrequestRepo(t, "https://user:"+secret+"@github.example/acme/repo.git")
	bin := t.TempDir()
	gitStub := filepath.Join(bin, "git")
	if err := os.WriteFile(gitStub, []byte("#!/bin/sh\nprintf '%s\\n' 'fatal: https://user:"+secret+"@github.example/acme/repo.git' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	manager, cleanup := loadPullrequest(t, map[string]any{"remote": "origin"})
	defer cleanup()

	err := runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 127))
	if err == nil || !strings.Contains(err.Error(), "git -C "+worktree+" remote get-url origin") || !strings.Contains(err.Error(), "fatal") || strings.Contains(err.Error(), secret) {
		t.Fatalf("Git credential diagnostic = %v", err)
	}
	if output := pullrequestOutputText(t, manager); strings.Contains(output, secret) {
		t.Fatalf("Git credential leaked into durable output: %q", output)
	}
}

func TestPullrequestPushDiagnosticsRedactCredentialsAndRetainContext(t *testing.T) {
	const secret = "git-push-secret"
	const basicSecret = "git-push-basic-secret"
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	pullrequestCommandStub(t, "")
	worktree, _ := pullrequestRepo(t, "https://user:"+secret+"@github.example/acme/repo.git")
	bin := t.TempDir()
	gitStub := filepath.Join(bin, "git")
	contents := "#!/bin/sh\nif [ \"$1\" = -C ] && [ \"$3\" = push ]; then printf '%s\\n' 'remote: https://user:" + secret + "@github.example/acme/repo.git: permission denied; aUtHoRiZaTiOn: bAsIc " + basicSecret + "' >&2; exit 1; fi\nexec \"$PULLREQUEST_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(gitStub, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULLREQUEST_REAL_GIT", gitPath)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	server, _ := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected request after push failure: %s %s", r.Method, r.URL.RequestURI())
		}
		_, _ = io.WriteString(w, "[]")
	})
	pullrequestFailingGHStub(t)
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "api_url": server.URL, "token": "api-token"})
	defer cleanup()

	actualHead := runGit(t, worktree, "rev-parse", "HEAD")
	integrationHead := runGit(t, worktree, "rev-parse", "feature/pullrequest")
	err = runPullrequestManual(t, manager, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 127))
	if err == nil || !strings.Contains(err.Error(), "git -C "+worktree+" push -u origin feature/pullrequest:feature/pullrequest") || !strings.Contains(err.Error(), "permission denied") || !strings.Contains(err.Error(), "HEAD "+actualHead) || !strings.Contains(err.Error(), "integration branch feature/pullrequest ("+integrationHead+")") || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), basicSecret) {
		t.Fatalf("Git push diagnostic = %v", err)
	}
	if output := pullrequestOutputText(t, manager); strings.Contains(output, secret) || strings.Contains(output, basicSecret) {
		t.Fatalf("Git push credential leaked into durable output: %q", output)
	}
}

func TestPullrequestRESTHeadCommitMatchOnAnotherBranchIsExistingWithoutEdit(t *testing.T) {
	for _, test := range []struct {
		name   string
		forge  string
		remote string
		path   string
		token  string
		url    string
	}{
		{name: "github", forge: "github", remote: "https://github.com/acme/repo.git", path: "/repos/acme/repo/pulls", token: "github-token", url: "https://github.com/acme/repo/pull/167"},
		{name: "forgejo", forge: "forgejo", remote: "https://forge.example/acme/repo.git", path: "/api/v1/repos/acme/repo/pulls", token: "forgejo-token", url: "https://forge.example/acme/repo/pulls/167"},
	} {
		t.Run(test.name, func(t *testing.T) {
			worktree, bare := pullrequestRepo(t, test.remote)
			head := runGit(t, worktree, "rev-parse", "HEAD")
			runGit(t, worktree, "branch", "integration/coordinate")
			server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != test.path {
					t.Fatalf("unexpected REST request: %s %s", r.Method, r.URL.RequestURI())
				}
				if r.URL.Query().Get("head") != "" {
					if r.URL.Query().Get("base") != "main" {
						t.Errorf("named-branch lookup query = %s", r.URL.RawQuery)
					}
					_, _ = io.WriteString(w, "[]")
					return
				}
				if r.URL.Query().Get("base") != "" || r.URL.Query().Get("state") != "open" {
					t.Errorf("SHA lookup query = %s", r.URL.RawQuery)
				}
				_, _ = io.WriteString(w, `[{"number":166,"html_url":"`+strings.TrimSuffix(test.url, "167")+`166","head":{"user":{"login":"unrelated-head-user"},"repo":{"id":41,"owner":{"login":"unrelated-owner"}},"ref":"other-branch","sha":"unrelated-sha"}},{"number":167,"html_url":"`+test.url+`","user":{"login":"nested-before-head"},"base":{"ref":"other-base"},"head":{"user":{"login":"nested-head-user"},"repo":{"id":42,"owner":{"login":"nested-owner"}},"ref":"other-branch","sha":"`+head+`"}}]`)
			})
			pullrequestFailingGHStub(t)
			pullrequestCommandStub(t, "")
			manager, cleanup := loadPullrequest(t, map[string]any{"forge": test.forge, "api_url": server.URL, "token": test.token})
			defer cleanup()

			result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "repo", "integration/coordinate", "main", 127))
			if err != nil {
				t.Fatal(err)
			}
			if result.Value != "repo: "+test.url+" (existing)" {
				t.Fatalf("REST existing result = %#v", result.Value)
			}
			if requests := capture.snapshot(); len(requests) != 2 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodGet {
				t.Fatalf("REST existing side effects = %+v", requests)
			}
			if _, err := os.Stat(filepath.Join(bare, "refs", "heads", "integration", "coordinate")); !os.IsNotExist(err) {
				t.Fatalf("REST content match pushed a branch: %v", err)
			}
		})
	}
}

func TestPullrequestRESTUnfilteredSHADoesNotPatchUnrelatedRequest(t *testing.T) {
	for _, test := range []struct {
		name   string
		forge  string
		remote string
		path   string
		token  string
		url    string
		create string
	}{
		{name: "github", forge: "github", remote: "https://github.com/acme/repo.git", path: "/repos/acme/repo/pulls", token: "github-token", url: "https://github.com/acme/repo/pull/168", create: "https://github.com/acme/repo/pull/169"},
		{name: "forgejo", forge: "forgejo", remote: "https://forge.example/acme/repo.git", path: "/api/v1/repos/acme/repo/pulls", token: "forgejo-token", url: "https://forge.example/acme/repo/pulls/168", create: "https://forge.example/acme/repo/pulls/169"},
	} {
		t.Run(test.name, func(t *testing.T) {
			worktree, _ := pullrequestRepo(t, test.remote)
			server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					t.Fatalf("unexpected REST request: %s %s", r.Method, r.URL.RequestURI())
				}
				if r.Method == http.MethodPost {
					_, _ = io.WriteString(w, `{"html_url":"`+test.create+`"}`)
					return
				}
				if r.Method != http.MethodGet {
					t.Fatalf("unexpected REST method: %s", r.Method)
				}
				if r.URL.Query().Get("head") != "" {
					_, _ = io.WriteString(w, "[]")
					return
				}
				_, _ = io.WriteString(w, `[{"number":168,"html_url":"`+test.url+`","head":{"user":{"login":"unrelated-head-user"},"repo":{"id":43,"owner":{"login":"unrelated-owner"}},"sha":"unrelated-sha","ref":"other-branch"}}]`)
			})
			pullrequestFailingGHStub(t)
			pullrequestCommandStub(t, "")
			manager, cleanup := loadPullrequest(t, map[string]any{"forge": test.forge, "api_url": server.URL, "token": test.token})
			defer cleanup()

			result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 127))
			if err != nil {
				t.Fatal(err)
			}
			if result.Value != "repo: "+test.create+" (created)" {
				t.Fatalf("unrelated SHA result = %#v", result.Value)
			}
			requests := capture.snapshot()
			if len(requests) != 3 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodGet || requests[2].Method != http.MethodPost {
				t.Fatalf("unrelated SHA requests = %+v", requests)
			}
		})
	}
}

func TestPullrequestGitHubRESTSHAScansAllOpenPages(t *testing.T) {
	const existingURL = "https://github.com/acme/repo/pull/167"
	worktree, bare := pullrequestRepo(t, "https://github.com/acme/repo.git")
	integrationSHA := runGit(t, worktree, "rev-parse", "feature/pullrequest")
	pageOneItems := make([]string, 100)
	for index := range pageOneItems {
		pageOneItems[index] = fmt.Sprintf(`{"number":%d,"html_url":"https://github.com/acme/repo/pull/%d","head":{"sha":"unrelated-%d","ref":"other-%d"}}`, index+1, index+1, index, index)
	}
	pageOne := "[" + strings.Join(pageOneItems, ",") + "]"
	server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/repo/pulls" || r.Method != http.MethodGet {
			t.Fatalf("unexpected paginated SHA request: %s %s", r.Method, r.URL.RequestURI())
		}
		if r.URL.Query().Get("head") != "" {
			_, _ = io.WriteString(w, "[]")
			return
		}
		if r.URL.Query().Get("per_page") != "100" {
			t.Errorf("SHA page size = %q", r.URL.Query().Get("per_page"))
		}
		switch r.URL.Query().Get("page") {
		case "1":
			_, _ = io.WriteString(w, pageOne)
		case "2":
			_, _ = io.WriteString(w, `[{"number":167,"html_url":"`+existingURL+`","user":{"login":"nested"},"head":{"repo":{"id":44,"owner":{"login":"nested-owner"}},"user":{"login":"nested"},"sha":"`+integrationSHA+`","ref":"other-branch"}}]`)
		default:
			t.Fatalf("unexpected SHA page: %s", r.URL.RequestURI())
		}
	})
	pullrequestFailingGHStub(t)
	pullrequestCommandStub(t, "")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "api_url": server.URL, "token": "github-token"})
	defer cleanup()

	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 127))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "repo: "+existingURL+" (existing)" {
		t.Fatalf("paginated SHA result = %#v", result.Value)
	}
	if requests := capture.snapshot(); len(requests) != 3 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodGet || requests[2].Method != http.MethodGet {
		t.Fatalf("paginated SHA requests = %+v", requests)
	}
	if _, err := os.Stat(filepath.Join(bare, "refs", "heads", "feature", "pullrequest")); !os.IsNotExist(err) {
		t.Fatalf("paginated SHA match pushed a branch: %v", err)
	}
}

func TestPullrequestGitHubCLISHAIgnoresBaseFilter(t *testing.T) {
	const existingURL = "https://github.com/acme/repo/pull/167"
	worktree, bare := pullrequestRepo(t, "ssh://git@github.com/acme/repo.git")
	integrationSHA := runGit(t, worktree, "rev-parse", "feature/pullrequest")
	ghLog := pullrequestGitHubSHAListsStub(t, "[]", fmt.Sprintf(`[{"url":%q,"number":167,"headRefOid":%q,"headRefName":"other-branch"}]`, existingURL, integrationSHA))
	pullrequestCommandStub(t, "")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github"})
	defer cleanup()

	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEventWithBody(t, worktree, "repo", "feature/pullrequest", "main", 127))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "repo: "+existingURL+" (existing)" {
		t.Fatalf("CLI SHA result = %#v", result.Value)
	}
	if output := string(mustReadFile(t, ghLog)); strings.Contains(output, "pr list --repo acme/repo --base main") {
		t.Fatalf("CLI SHA lookup retained base filter: %q", output)
	}
	if _, err := os.Stat(filepath.Join(bare, "refs", "heads", "feature", "pullrequest")); !os.IsNotExist(err) {
		t.Fatalf("CLI SHA match pushed a branch: %v", err)
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
	if len(requests) != 4 || requests[0].Path != "/api/v1/version" || requests[1].Method != http.MethodGet || requests[2].Method != http.MethodGet || requests[3].Method != http.MethodPost {
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
			if len(after) != before+3 {
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
