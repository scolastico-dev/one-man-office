# one-man-office agent guide

This is the fast technical orientation for agents modifying `one-man-office` (`omo`). Read [README.md](./README.md) for the complete user-facing behavior and CLI reference; this document focuses on how the repository works and how to change it safely.

## What this project is

`omo` is a self-contained Go terminal application that runs a small hierarchy of AI CLI agents across one or more local Git repositories. The user talks to a CEO agent, which delegates specs to product managers. Product managers create developer jobs, each developer works in an isolated Git worktree, and clean-context reviewers approve or reject the result. Smoke-alarm and firefighter agents monitor and recover unhealthy work.

There is no daemon, tmux layer, or remote service. One `omo` process owns the agent PTYs/ConPTYs, local socket or named pipe, TUI, supervisor loops, and SQLite connection. Durable queue and message state make restart recovery inexpensive.

The supported host and release targets are Linux, macOS, and Windows on amd64 and arm64. The module currently declares Go 1.26.2 in `go.mod`; treat `go.mod` as authoritative if documentation differs. Builds are pure Go with `CGO_ENABLED=0`.

## Start here

```bash
make build                    # bin/omo
make check                    # formatting check, go vet, full test suite
make test                     # tests only
make cross                    # all supported OS/architecture binaries
./bin/omo setup /tmp/demo     # scaffold an office
./bin/omo --mock              # full orchestration without model calls
```

Useful focused commands:

```bash
go test ./internal/supervisor -run '^TestFullReviewMergeFlow$' -count=1
go test ./internal/gitops -run Worktree -count=1
go test ./internal/office -count=1
go test ./internal/cli -count=1
```

Supervisor integration tests start real subprocesses and PTYs. For stress runs, prefer repeated fresh `go test ... -count=1` processes instead of a large `-count=N` in one process; package-level timing overrides and process cleanup are designed around a normal single suite run.

`TestLiveAgentCLIHandshake` is deliberately opt-in via `OMO_LIVE_AGENT_CLI=codex|gemini`. It spends one minimal model request with spawn retries disabled; never add it to the default test suite or run it in a loop.

## Runtime architecture

```text
cmd/omo
  -> internal/cli (Cobra commands and startup checks)
  -> internal/office (assemble config, DB, transport, supervisor, TUI)
       -> internal/supervisor (kernel and lifecycle loops)
            -> internal/session (PTY/ConPTY agent processes and logs)
            -> internal/queue + internal/db (durable jobs/events/incidents)
            -> internal/bus (durable mail and enforced routing)
            -> internal/gitops (worktrees, branches, serialized merges)
       -> internal/sockd (server) <- internal/sockc (agent CLI verbs)
       -> internal/tui (live terminal peek and office overview)
```

Startup follows this path:

1. `internal/cli` locates an office and performs optional release, template, and provider-plugin checks.
2. `internal/office.Open` loads `.omo/omo.yaml`, messages, SQLite, and the platform transport endpoint.
3. Recovery marks old agents dead and requeues every non-terminal job with a restart note.
4. `internal/office.Start` launches the socket server, dispatch, smoke-alarm, notification, retention, and CEO-activity loops, then spawns the CEO. With `--safe-mode`, the CEO is the only role allowed to spawn until the user or CEO resumes spawning.
5. Agent-facing `omo` commands use `OMO_AGENT_ID` and `OMO_SOCKET` to call the running supervisor.

`omo --read-only` deliberately bypasses that lifecycle. It uses
`config.LoadReadOnly`, `db.OpenReadOnly`, and `office.OpenReadOnly`, claims no
lock or transport, runs no recovery or loops, and renders only query-backed TUI
tabs. SQLite `mode=ro` plus `query_only` is a second safety boundary beneath the
UI guards.

Every socket verb is authenticated against the live agent record. State-changing handlers persist changes before acknowledging the request. Preserve that durability rule.

## Repository map

| Path | Responsibility |
|---|---|
| `cmd/omo/main.go` | Small executable entry point; build-time version injection. |
| `internal/agentcli/` | Claude/Codex/Gemini detection, reliable initial-prompt delivery, and trust adaptation. |
| `internal/cli/` | Cobra tree, office startup, setup/repo commands, agent verbs, job/mail/power commands, hidden fake-agent command. |
| `internal/office/` | Office discovery/setup, component wiring, restart recovery, `.omo` Git exclusion, platform lifecycle. |
| `internal/supervisor/` | Core orchestration: spawning, dispatch, completion/review, incident handling, cleanup, notifications, stats, and agent permissions. |
| `internal/session/` | PTY on Unix, ConPTY on Windows, virtual terminal screen, input, readable transcript generation, and log rotation. |
| `internal/queue/` | Persistent job model and validated state machine. |
| `internal/db/` | SQLite schema, additive migrations, agents, events, incidents, and retention. |
| `internal/bus/` | Stored office mail, directory lookup, and server-enforced role routing. |
| `internal/gitops/` | `.git/info/exclude`, worktree creation/removal, branch deletion, and per-repository merge locks. |
| `internal/proto/` | JSON request/response types and arguments for every socket verb. |
| `internal/sockc/` | Agent-side Unix-socket/Windows-pipe client. |
| `internal/sockd/` | Office-side authenticated verb server. |
| `internal/transport/` | Short Unix socket endpoint/symlink and Windows named-pipe endpoint selection. |
| `internal/tui/` | Bubble Tea UI: agent peek, overview tabs, messages, jobs, incidents, events, and statistics. |
| `internal/messages/` | Embedded supervisor-to-agent text templates and per-office overrides. |
| `internal/plugins/` | Strict manifests, event dispatch, sandboxed Lua hooks, command hooks, and durable plugin storage. |
| `internal/prompts/` | Embedded common/role prompts, export, loading, and template-generation hash. |
| `internal/fakeagent/` | Scenario-driven stand-in used by tests and `--mock`. |
| `internal/selfupdate/` | GitHub release lookup, checksum verification, and platform-specific executable replacement. |
| `internal/superpowercache/` | Shared Superpowers checkout beside the omo executable and startup fast-forward updates. |
| `internal/claudetrust/` | Narrow update of Claude Code folder-trust data for agent workdirs. |
| `internal/names/` | Stable human-readable role-based agent names. |
| `internal/verbs/` | Shared socket handlers that do not require full supervisor ownership. |
| `install.sh`, `install.ps1` | Release installers and PATH setup. |
| `.github/workflows/` | Dedicated pull-request, nightly/main, and release workflows. |
| `Makefile` | Canonical local and CI build/test commands. |

Most behavior has a nearby `_test.go`. Start with the package owning the behavior rather than adding cross-package shortcuts.

## Office data layout

`omo setup` creates an office-local `.omo/` directory:

```text
.omo/
  omo.yaml            configuration
  omo.db              SQLite jobs, agents, mail, events, incidents (WAL)
  messages/           editable supervisor message templates
  prompts/            editable common and role prompts
  extensions/         optional <role>.md or lexically ordered <role>/*.md prompt additions
  plugins/            office-local event plugins with plugin.json manifests
  storage/            shared workspace for CEO, PM, smoke-alarm, and firefighter sessions
  worktrees/          <repo>-<job-id>/ developer worktrees
  logs/               readable per-agent session transcripts
  omo.sock            Unix display symlink; absent on Windows
  omo.lock            live instance's socket or named-pipe endpoint
  templates.sha256    installed prompt/message generation marker
```

For a new office, the CLI command auto-detects executables on `PATH` in Claude, Codex, Gemini priority order. `omo setup --agent-cli <provider>` overrides detection; the programmatic `office.Setup` helper retains Claude as its deterministic default for tests and callers.

In a single-repository office, `.omo/` is added to `.git/info/exclude`, never `.gitignore`. Do not turn office runtime state into tracked project data.

On Unix the real socket lives under the system temp directory to avoid socket path-length limits; `.omo/omo.sock` points to it. Windows uses an office-specific named pipe.

## Roles and lifecycle

The seven roles are `ceo`, `product_manager`, `developer`, `reviewer`, `freelancer`, `smokealarm`, and `firefighter`. `config.AllRoles` is the code source of truth.

Normal developer job states are:

```text
queued -> assigned -> working -> review -> merging -> done
                                  |          |
                                  +-> rework+
```

`failed` and `cancelled` may be requeued. PM and freelancer jobs skip review via `working -> merging -> done`. A completed freelancer remains alive and normally parks in `omo wait` for CEO follow-ups, but no longer consumes the active freelancer-job limit. State edges are enforced in `internal/queue/queue.go`; never update `jobs.state` directly.

Developer jobs always name a repository and receive an isolated worktree. Generated naming uses `<branches.prefix><job-id>`; AI naming first runs a short-lived internal `branch_namer` agent and appends its validated Conventional Commits-style suffix to the prefix. Freelancer jobs may optionally name a repository to receive the same isolation for repository-scoped research or artifacts.

Important merge ordering:

1. Transition the job to `merging`.
2. Merge the developer branch into the repository's checked-out branch under a per-repository lock.
3. Transition the job to `done` and publish result/notifications.
4. Stop the developer, remove its worktree, and delete its branch.
5. Emit the durable `job_merged` event.

`done` can therefore become observable just before filesystem cleanup completes. Tests or consumers that inspect/remove the worktree or repository must wait for the matching `job_merged` event, which is the post-cleanup boundary.

A merge conflict is aborted in the main checkout and returned to review/rework; do not leave a repository mid-merge. Developers never merge their own branches. Reviewers receive only the job goal and diff, preserving clean context.

## Configuration and templates

`internal/config/config.go` defines the strict YAML schema and defaults. Decoding uses known-field checking, so misspelled keys must fail. Older valid configs are extended with missing defaults while preserving existing values and comments. When adding configuration:

1. Add the typed field and YAML tag.
2. Add a useful default.
3. Add it to the human-authored missing-default YAML.
4. Validate it and update config tests.
5. Update the configuration example in `README.md` and this guide if architectural.

Messages in `internal/messages/defaults/` are short supervisor-generated prompts. Role instructions live in `internal/prompts/templates/`. Setup exports both into `.omo` so users can edit them. Missing files fall back to embedded defaults; malformed templates fail loudly. Preserve required machine-readable lines such as the firefighter incident ID. Run package tests after any template change because freshness hashes and exported defaults are intentional behavior.

Role prompt extensions use either `.omo/extensions/<role>.md` or Markdown
fragments in `.omo/extensions/<role>/`, loaded lexicographically and exposed
to templates as `.Extensions`. Setup/update create but never replace this
user-owned directory.

Prompt data exposes `.Paths` as labeled absolute references for `office_root`,
`omo_dir`, `storage`, `workspace`, and every configured `repo:<key>`. Agent
rows persist the actual launch workdir so the workspace reference remains
truthful for worktrees and non-repository roles.

Model profiles remain generic `cmd + args + env`, despite the field name. Roles accept a legacy scalar profile, a profile list, or a `models`/`assignment` mapping; assignments are `round_robin`, `random`, retry-aware `failover`, or Claude/Codex-only `smart`. `internal/modelusage` is the narrow exception that reads native OAuth credentials and usage APIs: startup preflight is strict when enabled, `usage.safe_shutdown_percent` starts orderly handoffs, and the higher `usage.weekly_limit_percent` ceiling hard-stops the office. `usage.enabled: false` disables those calls and limits, with `smart` degrading to round-robin. Explicit per-job model choices take precedence but require a persisted `--force` approval above the soft ceiling. Profile arguments support `%prompt%` substitution independently from automatic provider/PTY injection; per-profile delay, retry count, and retry wait settings govern automatic delivery until `omo ready`. The optional `provider` field enables the narrow compatibility adapter in `internal/agentcli`; do not bake provider assumptions into the generic session package. Claude's persistent folder trust remains isolated in `internal/claudetrust`. Codex uses per-launch workspace/hook trust overrides, Gemini uses process-local workspace trust, and all are controlled by `trust_workdirs`.

## Persistence and concurrency invariants

- SQLite uses WAL mode, a busy timeout, foreign keys, and one open connection to serialize writes.
- Cumulative model statistics are idempotently upserted into one `overall_statistics` row per model on a timer and during orderly shutdown.
- Successful usage checks replace canonical provider rows in `model_usage_snapshots`; profiles sharing a credential scope reuse a process-wide, coalescing cache refreshed by `usage.refresh_interval`, leaving only Claude weekly/session and Codex weekly values in the TUI.
- A role with no eligible metered profile is reported as blocked, but safe shutdown starts only when every configured Claude/Codex credential scope is capped.
- Usage-triggered shutdown stores a user-facing reason; the CLI prints it to stdout only after the TUI has returned and restored the terminal.
- Job transitions update state and append a `job_state` event in one transaction.
- Agent permissions, mail routing, and sender identity are enforced server-side, not only by prompts. Firefighter contact grants only the contacted agent a direct reply path; supervisor-authored mail uses the reserved `omo` sender, never `user`.
- Direct PTY input through `omo type` is server-authorized for only the user, CEO, and firefighter. Its durable event records the target and input size/key count, never the input payload.
- The supervisor owns session maps and wait channels; follow the existing mutex boundaries.
- Plugin hooks run in lexical plugin-directory and manifest order. Job-create
  authorization precedes mutable hooks; modified data flows through hooks in
  that order and then passes normal server-side validation. Lua values are stored
  in SQLite; plugin code is trusted because command hooks and `omo.exec` can
  launch user-level processes.
- Git operations for a repository share one mutex. Do not bypass `internal/gitops` for merge/worktree mutations.
- Restart recovery is deliberately simple: living agents are marked dead and every non-terminal job is requeued. There is no transcript replay.
- Safe shutdown is the exception to no transcript replay: agents save concise role/job-keyed handoffs in `shutdown_contexts`; the next matching `omo ready` renders a handoff into its prompt and only then deletes the row. Safe shutdown halts spawning and stops after all targeted agents finish/checkpoint or its bounded deadline expires.
- Startup claims `.omo/omo.lock`, validates any recorded endpoint, and refuses a second live instance. The user can emergency-stop a live office over that endpoint; CEO and firefighter sessions have the same role-gated power.
- Read-only observation is the sole exception to single-owner startup: it ignores ownership state, never changes lifecycle rows or unread mail, and may display stale durable agent state when no owner is running.
- Agent identity comes from injected environment, not CLI arguments supplied by the model.
- Role prompts prohibit direct access to supervisor-owned `.omo` state (including SQLite and `omo.yaml`) unless the user explicitly requests a specific internal-file task. Job creators should pass substantial briefs with `omo job create --goal-file`; the CLI reads the file and stores its contents in the normal `jobs.goal` field.
- CEO, product-manager, smoke-alarm, and firefighter processes use `.omo/storage` as their shared working directory; developer/reviewer work remains in job worktrees and repository-scoped freelancers retain their worktree behavior.
- Cross-platform process, socket, and replacement implementations use `_unix.go`/`_windows.go`; keep platform-specific APIs behind those files.
- Agent processes default to a Linux nice increment of 10 when `agents.lower_priority` is enabled, capped at nice 19. The session package owns this platform-specific adjustment; the omo process itself retains its original priority.
- Superpowers is installed once beside the resolved omo executable and fast-forwarded on normal startup; prompts point agents directly at that shared checkout rather than relying on provider plugin state.
- `omo` must not modify user Git signing settings or commit office state.

## Tests

`make check` is the required local gate and matches CI:

1. `gofmt -l` must report nothing.
2. `go vet ./...` must pass.
3. `go test ./... -timeout 900s` must pass.

The integration suite builds `cmd/omo`, uses the scenario-driven fake agent, and launches real PTYs/ConPTYs. Tests may take tens of seconds. Use `t.TempDir`, isolated Git repositories, and the helpers in each package. Never depend on the developer's global Git configuration.

Fake-agent scenario lines include commands such as `ready`, `shell|...`, `done|...`, `verdict|...`, `wait`, and `sleep|...`. Prefer them over mocking away the socket/session boundary when testing orchestration.

For asynchronous assertions, wait for a durable state or event rather than sleeping a fixed duration. In particular, use `job_merged` for post-merge filesystem assertions and `review_started` to distinguish successive review cycles.

## CI and releases

Workflows are intentionally separated so only relevant jobs appear:

- `.github/workflows/pull-request.yml`: test, cross-build, and one-day PR artifact on `pull_request`.
- `.github/workflows/nightly.yml`: test, cross-build, and seven-day artifact on pushes to `main` and the nightly schedule.
- `.github/workflows/release.yml`: test, cross-build, package, checksum, and upload on a published GitHub release.

Keep action versions and build commands aligned across workflows. Preserve existing job display names if branch protection may reference them.

Release assets must retain these names because installers and self-update depend on them:

```text
omo-linux-amd64.tar.gz
omo-linux-arm64.tar.gz
omo-darwin-amd64.tar.gz
omo-darwin-arm64.tar.gz
omo-windows-amd64.zip
omo-windows-arm64.zip
SHA256SUMS
```

`Makefile` injects the version with `-ldflags`; do not add a second runtime version source. `install.sh`, `install.ps1`, and `internal/selfupdate` all verify SHA-256 checksums before replacement.

## Change checklist

Before editing:

1. Read the owning package and its tests.
2. Check `git status` and preserve unrelated work.
3. Identify whether the behavior is durable state, an asynchronous observation, or platform-specific.

Before handing off:

1. Add or update the narrowest useful test.
2. Run the focused package/test.
3. Run `make check`.
4. Run `git diff --check` and inspect the final diff.
5. If workflows changed, validate their syntax and inspect the resulting Actions run after push.
6. If user-visible behavior, configuration, CLI, layout, or release assets changed, update `README.md` and this guide.

## Common mistakes

- Treating `done` as proof that worktree cleanup has completed; wait for `job_merged`.
- Writing directly to queue state instead of using `queue.Store.Transition`.
- Relying on prompt compliance for a permission that belongs in server-side auth/routing.
- Adding `.omo` to a repository `.gitignore`; setup deliberately uses `.git/info/exclude`.
- Putting Unix-only process/socket code in a shared file or forgetting the Windows counterpart.
- Calling external agent CLIs directly from orchestration code instead of going through `session` profiles.
- Changing release filenames without updating both installers and self-update logic.
- Using fixed sleeps for lifecycle tests when a stored event or state is available.
- Assuming README requirements override `go.mod`, workflow definitions, or executable behavior.
