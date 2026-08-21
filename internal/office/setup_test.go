package office

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
)

func TestSetupCreatesAWorkingOffice(t *testing.T) {
	dir := t.TempDir()
	created, err := Setup(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) == 0 {
		t.Fatal("Setup reported nothing created")
	}
	for _, p := range []string{
		ConfigPath, ".omo/.gitignore", ".omo/omo.db", ".omo/logs", ".omo/storage", ".omo/worktrees", TemplatesVersionPath,
		filepath.Join(messages.Dir, "start_prompt.txt"),
		filepath.Join(prompts.Dir, "common.md"),
		filepath.Join(prompts.Dir, "reviewer.md"),
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	if outdated, err := TemplatesOutdated(dir); err != nil || outdated {
		t.Fatalf("fresh setup templates reported outdated=%v, err=%v", outdated, err)
	}
	if raw, err := os.ReadFile(filepath.Join(dir, ".omo", ".gitignore")); err != nil || string(raw) != officeGitignore {
		t.Fatalf(".omo/.gitignore = %q, err %v", raw, err)
	}
	// The generated config must actually load and validate.
	if _, err := config.Load(filepath.Join(dir, ConfigPath)); err != nil {
		t.Fatalf("generated config does not validate: %v", err)
	}
	// The database must be a real, migrated omo database.
	d, err := sql.Open("sqlite", filepath.Join(dir, ".omo", "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, table := range []string{"agents", "jobs", "messages", "events", "incidents"} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Errorf("table %s not initialised: %v", table, err)
		}
	}
}

func TestSetupSupportsEachOfficialAgentCLI(t *testing.T) {
	tests := []struct {
		provider agentcli.Provider
		profile  string
		wantArg  string
	}{
		{agentcli.Claude, "claude-fable", "--dangerously-skip-permissions"},
		{agentcli.Codex, "codex", "--dangerously-bypass-approvals-and-sandbox"},
		{agentcli.Gemini, "gemini", "--yolo"},
	}
	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			dir := t.TempDir()
			if _, err := SetupWithAgentCLI(dir, tt.provider); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Load(filepath.Join(dir, ConfigPath))
			if err != nil {
				t.Fatal(err)
			}
			profile := cfg.Models[tt.profile]
			if profile.Provider != tt.provider {
				t.Fatalf("profile provider = %q, want %q", profile.Provider, tt.provider)
			}
			if !contains(profile.Args, tt.wantArg) {
				t.Fatalf("profile args = %v, want %q", profile.Args, tt.wantArg)
			}
			for _, role := range config.AllRoles {
				for _, model := range cfg.Roles[role].Models {
					assigned := cfg.Models[model]
					if tt.provider != agentcli.Claude && assigned.Provider != tt.provider {
						t.Errorf("role %s uses provider %q, want %q", role, assigned.Provider, tt.provider)
					}
				}
			}
		})
	}
}

func TestGeneratedConfigIncludesConcreteCommentedModelExamples(t *testing.T) {
	tests := []struct {
		provider agentcli.Provider
		want     []string
	}{
		{
			provider: agentcli.Claude,
			want: []string{
				`codex-sol:`, `args: ["--model", "gpt-5.6-sol"`,
				`codex-luna:`, `args: ["--model", "gpt-5.6-luna"`,
				`codex-mini:`, `args: ["--model", "gpt-5.4-mini"`,
				`# gemini-auto:`, `#   args: ["--model", "auto"`,
				`# gemini-pro:`, `#   args: ["--model", "pro"`,
				`# gemini-fast:`, `#   args: ["--model", "flash"`,
				`# gemini-light:`, `#   args: ["--model", "flash-lite"`,
			},
		},
		{
			provider: agentcli.Codex,
			want: []string{
				`# codex-capable:`, `#   args: ["--model", "gpt-5.3-codex"`,
				`# codex-fast:`, `#   args: ["--model", "codex-mini-latest"`,
			},
		},
		{
			provider: agentcli.Gemini,
			want: []string{
				`# gemini-auto:`, `#   args: ["--model", "auto"`,
				`# gemini-pro:`, `#   args: ["--model", "pro"`,
				`# gemini-fast:`, `#   args: ["--model", "flash"`,
				`# gemini-light:`, `#   args: ["--model", "flash-lite"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.provider), func(t *testing.T) {
			dir := t.TempDir()
			if _, err := SetupWithAgentCLI(dir, tt.provider); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, ConfigPath))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(raw), want) {
					t.Errorf("generated config missing %q", want)
				}
			}
			for _, want := range []string{"%prompt%", "prompt_retry_count: 0", "prompt_retry_wait"} {
				if !strings.Contains(string(raw), want) {
					t.Errorf("generated config missing prompt-delivery documentation %q", want)
				}
			}
			if strings.Contains(string(raw), "YOUR_") {
				t.Error("generated config contains a placeholder model identifier")
			}
		})
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestSetupIsIdempotentAndPreservesEdits(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, ConfigPath)
	os.WriteFile(cfg, []byte("# my config\nmodels:\n  x:\n    cmd: claude\nroles:\n  ceo: x\n  product_manager: x\n  developer: x\n  reviewer: x\n  freelancer: x\n  smokealarm: x\n  firefighter: x\n"), 0o644)
	nudge := filepath.Join(dir, messages.Dir, "mail_nudge.txt")
	os.WriteFile(nudge, []byte("MINE"), 0o644)
	ignore := filepath.Join(dir, ".omo", ".gitignore")
	os.WriteFile(ignore, []byte("# MINE\n"), 0o644)
	missing := filepath.Join(dir, prompts.Dir, "reviewer.md")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}

	created, err := Setup(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 0 {
		t.Fatalf("second Setup should create nothing, reported %v", created)
	}
	raw, _ := os.ReadFile(cfg)
	if string(raw[:11]) != "# my config" {
		t.Fatal("Setup overwrote an existing config")
	}
	if raw, _ := os.ReadFile(nudge); string(raw) != "MINE" {
		t.Fatalf("Setup overwrote an edited template: %q", raw)
	}
	if raw, _ := os.ReadFile(ignore); string(raw) != "# MINE\n" {
		t.Fatalf("Setup overwrote an edited .gitignore: %q", raw)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("Setup changed an initialized office; missing prompt stat error = %v", err)
	}
}

func TestUpdateTemplatesReplacesOnlyMessagesAndPrompts(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}

	messagePath := filepath.Join(dir, messages.Dir, "mail_nudge.txt")
	promptPath := filepath.Join(dir, prompts.Dir, "common.md")
	wantMessage, err := os.ReadFile(messagePath)
	if err != nil {
		t.Fatal(err)
	}
	wantPrompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(messagePath, []byte("CUSTOM MESSAGE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptPath, []byte("CUSTOM PROMPT"), 0o644); err != nil {
		t.Fatal(err)
	}
	messageExtra := filepath.Join(dir, messages.Dir, "obsolete.txt")
	promptExtra := filepath.Join(dir, prompts.Dir, "obsolete.md")
	if err := os.WriteFile(messageExtra, []byte("obsolete"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(promptExtra, []byte("obsolete"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, TemplatesVersionPath), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if outdated, err := TemplatesOutdated(dir); err != nil || !outdated {
		t.Fatalf("old marker reported outdated=%v, err=%v", outdated, err)
	}

	untouched := map[string]string{
		ConfigPath:                         "# custom config\n",
		".omo/.gitignore":                  "# custom ignore\n",
		".omo/logs/keep.log":               "log data\n",
		".omo/worktrees/keep/sentinel.txt": "worktree data\n",
		".omo/other-state":                 "other data\n",
	}
	for path, content := range untouched {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d, err := sql.Open("sqlite", filepath.Join(dir, ".omo", "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO events(kind, detail) VALUES ('sentinel', 'keep me')`); err != nil {
		d.Close()
		t.Fatal(err)
	}
	d.Close()

	replaced, err := UpdateTemplates(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 2 {
		t.Fatalf("UpdateTemplates reported %v, want two replaced directories", replaced)
	}
	if outdated, err := TemplatesOutdated(dir); err != nil || outdated {
		t.Fatalf("updated templates reported outdated=%v, err=%v", outdated, err)
	}
	if got, _ := os.ReadFile(messagePath); string(got) != string(wantMessage) {
		t.Fatalf("message was not reset to embedded default: %q", got)
	}
	if got, _ := os.ReadFile(promptPath); string(got) != string(wantPrompt) {
		t.Fatalf("prompt was not reset to embedded default: %q", got)
	}
	for _, extra := range []string{messageExtra, promptExtra} {
		if _, err := os.Stat(extra); !os.IsNotExist(err) {
			t.Fatalf("obsolete template survived replacement: %s (err %v)", extra, err)
		}
	}
	for path, want := range untouched {
		if got, err := os.ReadFile(filepath.Join(dir, path)); err != nil || string(got) != want {
			t.Errorf("update touched %s: got %q, err %v", path, got, err)
		}
	}
	d, err = sql.Open("sqlite", filepath.Join(dir, ".omo", "omo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM events WHERE kind = 'sentinel' AND detail = 'keep me'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("update changed database state: count=%d, err=%v", count, err)
	}
}

func TestUpdateTemplatesRequiresExistingOffice(t *testing.T) {
	if _, err := UpdateTemplates(t.TempDir()); err == nil {
		t.Fatal("UpdateTemplates should reject a directory without an initialized office")
	}
}

// A fresh office must be immediately runnable, with no manual steps.
func TestSetupThenOpenWorks(t *testing.T) {
	dir := t.TempDir()
	if _, err := Setup(dir); err != nil {
		t.Fatal(err)
	}
	o, err := Open(dir, true)
	if err != nil {
		t.Fatalf("a freshly set-up office must open: %v", err)
	}
	o.Close()
}

func TestSetupAllowsDeepOfficeWithShortRuntimeSocket(t *testing.T) {
	deep := filepath.Join(t.TempDir(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"cccccccccccccccccccccccccccccc", "dddddddddddddddddddddddddddddd")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Skipf("cannot create deep path: %v", err)
	}
	if _, err := Setup(deep); err != nil {
		t.Fatalf("deep office should use a short temporary socket: %v", err)
	}
}
