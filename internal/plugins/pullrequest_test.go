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
	contents := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PULLREQUEST_GH_LOG\"\nif [ \"$1\" = auth ] && [ \"$2\" = status ]; then exit 0; fi\nif [ \"$1\" = pr ] && [ \"$2\" = list ]; then printf '[{\"url\":\"%s\"}]' \"$PULLREQUEST_GH_EXISTING\"; exit 0; fi\nif [ \"$1\" = pr ] && [ \"$2\" = create ]; then printf '%s' \"$PULLREQUEST_GH_EXISTING\"; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(script, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULLREQUEST_GH_LOG", logPath)
	t.Setenv("PULLREQUEST_GH_EXISTING", existingURL)
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
			if payload["body"] != "OMO job 53 for acme/repo." {
				t.Errorf("GitHub body = %q, want trusted job metadata only", payload["body"])
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
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", []string{"Add pull request support"}, pullrequestJobEvent(worktree, "acme/repo", "feature/pullrequest", "main", 53, "Add pull request support"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != "https://github.com/acme/repo/pull/53" {
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
	if !strings.Contains(commands, "send -t user") || !strings.Contains(commands, "send -t ceo") || !strings.Contains(commands, "https://github.com/acme/repo/pull/53") {
		t.Fatalf("notifications = %q", commands)
	}
	if strings.Contains(pullrequestOutputText(t, manager), token) {
		t.Fatal("GitHub token leaked into durable plugin output")
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
	data := pullrequestJobEvent("", "", "", "", 59, "Release both")
	data["repo"] = nil
	data["branch"] = nil
	data["base_branch"] = nil
	data["worktree"] = nil
	data["args"] = []string{"Release both"}
	data["integration_branches"] = []map[string]any{
		{"repo": "api", "branch": "feature/pullrequest", "base_branch": "main", "worktree": apiWorktree},
		{"repo": "web", "branch": "feature/pullrequest", "base_branch": "main", "worktree": webWorktree},
	}
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "pm-59", "product_manager", []string{"Release both"}, data)
	if err != nil {
		t.Fatal(err)
	}
	want := "api: https://github.com/acme/one/pull/1\nweb: https://github.com/acme/two/pull/2"
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

	data["args"] = []string{"repo=web", "Selected"}
	selected, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "pm-59", "product_manager", []string{"repo=web", "Selected"}, data)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Value != "https://github.com/acme/two/pull/2" {
		t.Fatalf("PM selected result = %q", selected.Value)
	}
	data["args"] = []string{"repo=missing"}
	if _, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "pm-59", "product_manager", []string{"repo=missing"}, data); err == nil || !strings.Contains(err.Error(), "valid keys: api, web") {
		t.Fatalf("invalid PM selector error = %v", err)
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
	if !ok || !strings.Contains(text, "If completion leaves pull-request branches") || !strings.Contains(text, "run once") || !strings.Contains(text, "pullrequest create") {
		t.Fatalf("PM prompt = %q", text)
	}
}

func TestPullrequestPMRejectsWhenNoAsIsEntriesAreAvailable(t *testing.T) {
	manager, cleanup := loadPullrequest(t, nil)
	defer cleanup()
	data := pullrequestJobEvent("", "", "", "", 59)
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
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEvent(worktree, "acme/repo", "feature/pullrequest", "main", 53))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != wantURL {
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
					} else if payload["head"] != "feature/pullrequest" || payload["base"] != "main" {
						t.Errorf("Forgejo payload = %#v", payload)
					}
					_, _ = io.WriteString(w, `{"html_url":"https://forge.example/acme/repo/pulls/53"}`)
				} else {
					form, err := url.ParseQuery(string(mustReadBody(t, r)))
					if err != nil || form.Get("source_branch") != "feature/pullrequest" || form.Get("target_branch") != "main" {
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
			result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", []string{"Adapter test"}, pullrequestJobEvent(worktree, "repo", "feature/pullrequest", "main", 53, "Adapter test"))
			if err != nil {
				t.Fatal(err)
			}
			wantURL := "https://forge.example/acme/repo/pulls/53"
			if test.name == "gitlab" {
				wantURL = "https://gitlab.com/group/sub/repo/-/merge_requests/53"
			}
			if result.Value != wantURL {
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
				if r.URL.EscapedPath() != test.path {
					t.Errorf("existing %s path = %s, want %s", test.name, r.URL.EscapedPath(), test.path)
				}
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
				if r.Method != http.MethodGet {
					t.Errorf("existing %s used %s instead of GET", test.name, r.Method)
					return
				}
				test.checkQuery(t, r)
				if test.forge == "gitlab" {
					_, _ = io.WriteString(w, `[{"web_url":"`+test.existing+`"}]`)
				} else {
					_, _ = io.WriteString(w, `[{"html_url":"`+test.existing+`"}]`)
				}
			})
			if test.forge == "github" {
				pullrequestFailingGHStub(t)
			}
			commandLog := pullrequestCommandStub(t, "id: 53\ntitle: Existing request\ngoal:\nexercise existing lookup\n")
			worktree, bare := pullrequestRepo(t, test.remote)
			manager, cleanup := loadPullrequest(t, map[string]any{"forge": test.forge, "api_url": server.URL, "token": test.token})
			defer cleanup()
			result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", nil, pullrequestJobEvent(worktree, "repo", "feature/pullrequest", "main", 53))
			if err != nil {
				t.Fatal(err)
			}
			if result.Value != test.existing {
				t.Fatalf("existing %s result = %q, want %q", test.name, result.Value, test.existing)
			}
			requests := capture.snapshot()
			if len(requests) != 1 || requests[0].Method != http.MethodGet {
				t.Fatalf("existing %s requests = %+v, want one GET", test.name, requests)
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

func TestPullrequestGitHubCLIExistingIsIdempotent(t *testing.T) {
	const existingURL = "https://github.com/acme/repo/pull/99"
	commandLog := pullrequestCommandStub(t, "id: 53\ntitle: CLI adapter\ngoal:\nexercise gh\n")
	ghLog := pullrequestAuthenticatedGHStub(t, existingURL)
	worktree, bare := pullrequestRepo(t, "ssh://git@github.com/acme/repo.git")
	manager, cleanup := loadPullrequest(t, map[string]any{"forge": "github", "token": "unused"})
	defer cleanup()
	result, err := manager.TriggerManualContextWithRoleAndDataResult(context.Background(), "pullrequest", "create", "user", "user", []string{"CLI adapter"}, pullrequestJobEvent(worktree, "repo", "feature/pullrequest", "main", 53, "CLI adapter"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Value != existingURL {
		t.Fatalf("GitHub CLI existing result = %q, want %q", result.Value, existingURL)
	}
	ghOutput, err := os.ReadFile(ghLog)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(ghOutput)
	if !strings.Contains(commands, "auth status") || !strings.Contains(commands, "pr list") || !strings.Contains(commands, "--head feature/pullrequest") || !strings.Contains(commands, "--base main") {
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
	if err := runPullrequestManual(t, manager, pullrequestJobEvent(worktree, "repo", "feature/pullrequest", "main", 53)); err != nil {
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
	err := runPullrequestManual(t, manager, pullrequestJobEvent(worktree, "repo", "feature/pullrequest", "main", 53))
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
	err := runPullrequestManual(t, manager, map[string]any{
		"caller":      "user",
		"caller_role": "user",
		"args":        []any{"title"},
	})
	if err == nil || !strings.Contains(err.Error(), "job metadata") {
		t.Fatalf("missing metadata error = %v", err)
	}
}

func TestPullrequestRemoteURLForms(t *testing.T) {
	server, capture := newPullrequestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/repo/pulls" {
			t.Errorf("remote-form request = %s %s", r.Method, r.URL.RequestURI())
		}
		_, _ = io.WriteString(w, `[{"html_url":"https://github.com/acme/repo/pull/53"}]`)
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
			if err := runPullrequestManual(t, manager, pullrequestJobEvent(worktree, "repo", "feature/pullrequest", "main", 53, "Remote form")); err != nil {
				t.Fatal(err)
			}
			after := capture.snapshot()
			if len(after) != before+1 {
				t.Fatalf("remote form request count grew from %d to %d", before, len(after))
			}
		})
	}
}

func TestPullrequestRejectsMultipleTitleArguments(t *testing.T) {
	manager, cleanup := loadPullrequest(t, map[string]any{})
	defer cleanup()
	err := runPullrequestManual(t, manager, pullrequestJobEvent("/tmp/worktree", "repo", "branch", "main", 53, "one", "two"))
	if err == nil || !strings.Contains(err.Error(), "zero or one title") {
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
