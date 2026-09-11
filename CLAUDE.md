# one-man-office agent guide

This is the fast technical orientation for agents modifying `one-man-office` (`omo`). Read [README.md](./README.md) for the introduction and quick start and the [wiki](./wiki/README.md) for the complete user-facing behavior and CLI reference; this document focuses on how the repository works and how to change it safely.

## What this project is

`omo` is a self-contained Go terminal application that runs a small hierarchy of AI CLI agents across one or more local Git repositories. The user talks to a CEO agent, which delegates specs to product managers. Product managers create developer jobs, each developer works in an isolated Git worktree, and clean-context reviewers approve or reject the result. Smoke-alarm and firefighter agents monitor and recover unhealthy work.

Each office is one process, with no tmux layer or remote service. That `omo` process owns the agent PTYs/ConPTYs, local socket or named pipe, TUI, supervisor loops, and SQLite connection. The optional company dashboard can run in the background and owns its launched offices. Durable queue and message state make restart recovery inexpensive.

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

1. `internal/cli` locates and canonicalizes an office, requires saved or explicit user trust, then performs optional release, template, managed-plugin update, and provider checks. Unknown locations require terminal approval or `--trust-office`; mock/headless/skip-checks do not bypass this boundary. Read-only observation and non-launch commands do not prompt.
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
| `internal/pluginmanager/` | Git source normalization, managed checkout refresh, atomic plugin activation, and config edits. |
| `internal/globalhome/` | User home paths, independent global YAML, canonical office trust with serialized atomic writes, and fresh-office template overlays. |
| `internal/filelock/`, `internal/pluginfiles/` | Context-aware process locks and the shared plugin installation/snapshot filesystem protocol. |
| `internal/company/` | Local authenticated browser dashboard, trusted project actions, embedded xterm assets, owned office/shell PTYs, and process-tree cleanup. |
| `internal/companyservice/` | Per-home browser lifecycle lock, detached launch/readiness, authenticated local stop, private runtime state, and native login autostart. |
| `internal/company/controlplane/` | Private loopback child authentication, aggregate agent leases, shared usage cache, and fail-closed child watchdog client. |
| `plugins/` | Embedded bundled nudge/tools examples, global filebrowser company plugin, and optional official Git-installed pushover/autoshutdown plugins. |
| `internal/prompts/` | Embedded common/role prompts, export, loading, and template-generation hash. |
| `internal/fakeagent/` | Scenario-driven stand-in used by tests and `--mock`. |
| `internal/selfupdate/` | Latest and exact GitHub release lookup, checksum verification, and platform-specific executable replacement. |
| `internal/superpowercache/` | Shared Superpowers checkout inside the global omo home and startup fast-forward updates. |
| `internal/claudetrust/` | Narrow update of Claude Code folder-trust data for agent workdirs. |
| `internal/names/` | Stable human-readable role-based agent names. |
| `internal/verbs/` | Shared socket handlers that do not require full supervisor ownership. |
| `install.sh`, `install.ps1` | Release installers and PATH setup. |
| `.github/workflows/` | Dedicated pull-request, nightly/main, and release workflows. |
| `Makefile` | Canonical local and CI build/test commands. |

Most behavior has a nearby `_test.go`. Start with the package owning the behavior rather than adding cross-package shortcuts.

## Company dashboard

The CLI wraps browser serving in `companyservice.Run`, holding one OS-backed
lock per `OMO_HOME` through child cleanup. `--detached`/`-d` re-executes the same
binary, waits for authenticated readiness, and prints the startup log/access URL.
`company stop` uses a separate authenticated loopback endpoint, waits for the
lifecycle lock, and never signals a stored PID. Unix SIGTERM uses normal cleanup, including cancellation of startup hooks.
`company autostart register` snapshots literal dashboard arguments (including
defaults), executable location, cwd, PATH, and OMO_HOME for user login. Unregister
removes only that registration. Linux uses XDG autostart, macOS a RunAtLoad
LaunchAgent without KeepAlive, and Windows a per-user Run entry plus hidden
re-exec. Native entries point at private JSON settings to avoid shell expansion
and putting authentication values into desktop/plist/registry commands.
`OMO_HOME/company/` contains the lifecycle locks, `runtime.json` (stop token
and dashboard URL, removed after cleanup), `company.log` (replaced at each
background start), and optional `autostart.json` (including any Basic auth).
Unix restricts the directory/files to the user; Windows protects their inherited
ACL for the user and SYSTEM. Tests must isolate OMO_HOME and native autostart
locations; never register the developer's actual login environment.

The authenticated `untrust` project action removes available or stale trust
entries without deleting files and returns HTTP 409 while a company-owned
instance for that path is running.

The embedded dashboard uses square, labeled Metro/TUI panels, a monospace font
stack, and purple hover/focus accents. CSS respects reduced motion and stacks
navigation above the terminal on narrow screens. The top-left brand and empty
state use the transparent white-artwork variant, `assets/logo-transparent.png`,
derived from `.github/assets/logo.jpg`; the favicon uses the normal `logo.jpg`
artwork with baked rounded corners. List rendering reuses buttons and rows to
preserve keyboard focus across polling refreshes. Offices have an in-memory Edit
mode with ordered up/down controls and confirmation-protected Remove actions;
the stored order is persistent. Keep the stable plugin DOM IDs and xterm
fit/resize behavior intact when changing these assets.

`omo company` owns a public loopback dashboard (default `127.0.0.1:8090`)
and a separate ephemeral private loopback HTTP listener. The public surface
requires the per-run browser capability and validates Host/Origin; the URL
fragment is removed from browser history and retained only in page memory.
The explicit `--unsafe` escape hatch disables only that public capability
check and prints a prominent warning. `--basic-auth USER:PASSWORD` replaces
the capability with browser-native Basic authentication for trusted networks.
Host/Origin validation remains enabled unless `--no-origin-check` explicitly
drops only the Origin comparison for a trusted reverse proxy.
Project launches resolve canonical paths against global trust. Create and clone
use interactive setup terminals and literal argv; create accepts an existing
directory when it has no `.omo/omo.yaml`, while clone accepts only an absent or
empty destination. Both require a clean absolute destination with an existing
parent, and clone sources prohibit executable Git transports. Setup success
trusts the canonical destination; a nonzero exit retains the terminal output and
does not trust it. Before setup, cloned `.omo` trees reject symlinks and special
files so scaffolding cannot write outside the reserved destination; unrelated
project symlinks remain supported.

Each launched office receives unique `OMO_CONTROL_URL`/`OMO_CONTROL_TOKEN`
environment settings. The private server derives identity and usage-profile
allowlists from registration, never request-supplied profile definitions.
Launched offices retain the normal interactive startup checks; release,
embedded-asset, and plugin update prompts appear in the browser terminal.
`office.Open` uses the remote fetcher for usage preflight and runtime checks;
only product managers, developers, and freelancers acquire global leases.
CEOs, reviewers, smoke alarms, firefighters, and branch namers remain
controlled and supervised with the same control credentials and heartbeat, but
do not acquire leases. A leased slot is released after the process exits and
before management-agent respawn; unregistering a dead child releases all its
leases. The parent cache coalesces both ordinary fetches and child refresh
timers by credential scope. Profile allowlists are frozen until the child is
restarted.
Relative file-credential roots resolve against the child office directory.
Darwin Claude registration rejects relative non-empty config/secure-storage
roots because absolutizing their raw values would change the Keychain namespace;
absolute spelling and explicit empty secure-storage overrides are preserved.

Aggregate capacity denial is backpressure, not terminal job failure. Pending
counted work roles retry before the dispatcher pause gate; missing reviewers
retry ahead of queued jobs, and AI branch naming keeps its job queued. Reviewers
run alongside their retained developers without a capacity handoff. Supervised
config reload rejects changes to profile names or provider/credential scopes
before preflight or apply.

Heartbeat failure is sticky, halts spawning, and requests emergency cleanup;
managed children never fall back to independent usage requests or spawn limits.
The hidden shell wrapper uses a nested PTY and the same heartbeat lifecycle,
stripping control credentials before invoking `sh`/`cmd.exe`. The session package
also strips these credentials from agent environments. Standalone offices keep
their existing lifecycle. Capacity is per company process (`--max-agents`,
default 12) and counts only product managers, developers, and freelancers.
Coordination, safety, and naming roles are supervised but exempt; interactive
shells do not consume agent capacity.

Web terminal state is bounded and memory-only: 256 KiB server replay per terminal,
64 retained instances, 16 websocket connections, and bounded input queues.
Browser input uses `assets/terminal-input.js`: at most one 16 KiB frame is in
flight per connection, with a 4 MiB/1,024-event pending limit. The server sends
an `input-ack` JSON text frame only after the PTY write completes; terminal
output remains binary and client text frames remain resize requests. Keep
partial-write handling, output/resize responsiveness, and final-output draining
intact. Ctrl+Shift+V/native context-menu paste stays with xterm; Ctrl+V remains
a control key. Do not replay queued input after disconnect. Browser queue
regressions run through `go test` when Node.js is installed, or directly with
`node --test internal/company/terminal_input.test.cjs` (Node.js 18+).
The company also loads enabled global plugin `company_startup` hooks
and serves declared `company_load` files from immutable runtime snapshots
under `/plugins/<manifest-name>/`. Injected scripts receive `omo.execute`, a
company-load listener shortcut, the in-memory capability token, a DOM ID
shortcut, and stable page ID constants. Commands are rooted only in the user's
home or a trusted office, capped at eight concurrent runs, and use Unix
process groups or Windows Job Objects so request cancellation and completion
reap descendants. Global manual hooks can run without an office through
`omo plugin trigger --global`, with storage and audit data in
`OMO_HOME/plugins.db`.
The browser `execute` API also accepts `{stdin: string|Uint8Array|Blob|File}`;
stdin requests use ordered multipart parts and always close the child stdin
stream at EOF. The bundled filebrowser is installed in the global plugin root,
receives its four transfer limits from the frozen `company_load` config detail,
and keeps all file contents in page memory. It adds Files to the dashboard
toolbar and Browse to project setup, with the browser in an overlay rather than
a sidebar panel. It browses and transfers on Unix; one probe disables its Files,
picker Browse, upload, download, new-folder, and refresh actions on Windows or
probe failure. The Files button title and overlay warning use the exact text
`The file manager is not supported on Windows`.
Start/estop probes never delete office locks; empty startup locks retain their
grace, and stale-lock reclamation stays in the child's office ownership lifecycle.
Estop uses the existing office socket; forced kill freezes and snapshots Unix
descendants or terminates a Windows Job Object. Unix daemonized/reparented
commands are outside the process-tree snapshot; this is not a sandbox. Closing
the company stops every owned instance. Embedded xterm 6.0.0/fit 0.11.0 assets
and licenses live under `internal/company/assets`, with acquisition and
checksum details there. The company dashboard persists no terminal contents; its private lifecycle and
autostart files contain the credentials described above. Child offices keep
their normal transcript behavior.

## Office data layout

`internal/globalhome` resolves `~/.local/omo` on Unix and `%APPDATA%/omo` on
Windows; an absolute `OMO_HOME` overrides both. Setup and writable startup
create independent strict `config.yaml` with `trusted_offices: []` and
`plugins: {update_on_start: true, installed: {}}`, plus empty `plugins/`,
`extensions/`, and `template/` directories. `config.lock` serializes initialization
and trust updates across processes; trust writes preserve comments/settings and
atomically replace the YAML using platform-specific file operations. No global
messages or prompts are loaded. Superpowers downloads to `superpowers/` here.

Fresh setup overlays every regular file in global `template/` onto the office
root after embedded assets, except `template/.omo/omo.yaml`, which is a strict
partial YAML override merged with the generated current-schema config. It may
not replace discovered `repos`; `setup --sync` reapplies only that override to
an existing office. Repeated setup and `setup --update` ignore the overlay.
Preflight rejects symlinks and
special files, including destination symlinks, and captures source bytes before
fresh setup mutates the office. Failed setup removes the config initialization
marker so correcting a copy failure and rerunning completes the overlay.
The CLI owns interactive trust;
programmatic `office.Open` callers must enforce their own approval policy.

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

For a new office, the CLI command auto-detects executables on `PATH` in Claude, Codex, Gemini priority order. On a terminal it builds an interactive catalog from every detected provider and asks for each role's profiles and assignment method plus plugin choices; `--non-interactive` uses the auto-detected single-provider defaults. `omo setup --agent-cli <provider>` overrides the primary defaults, and the programmatic `office.Setup` helper retains Claude as its deterministic default for tests and callers. The Claude setup profile starts the CEO on Claude Fable and uses Codex Astra as its ordered failover when Fable is unavailable. User-maintained recommended plugin metadata lives in the strict global `known_plugins.json`; new homes start with an empty user catalog, while setup embeds official Pushover/autoshutdown defaults and `known_plugins.example.json` provides copyable catalog objects.

In a single-repository office, `.omo/` is added to `.git/info/exclude`, never `.gitignore`. `omo setup --with-git` removes only OMO's own exclude entry, converts repository paths to relative paths, and writes a selective `.omo/.gitignore` that exposes durable handoff files while keeping the database and other runtime/cache state ignored. Interactive runs offer enabled global plugins that have no local configuration before enabling the handoff. Do not turn office runtime state into tracked project data.

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
5. Update the configuration example in `wiki/configuration.md` and this guide if architectural.

Messages in `internal/messages/defaults/` are short supervisor-generated prompts. Role instructions live in `internal/prompts/templates/`. Setup exports both into `.omo` so users can edit them. Missing files fall back to embedded defaults; malformed templates fail loudly. Preserve required machine-readable lines such as the firefighter incident ID. Run package tests after any template change because freshness hashes and exported defaults are intentional behavior.

Role prompt extensions use either `.omo/extensions/<role>.md` or Markdown
fragments in `.omo/extensions/<role>/`, loaded lexicographically and exposed
to templates as `.Extensions`. Setup/update create but never replace this
user-owned directory. Global `extensions/` uses the same forms and validation;
its content is appended before office extensions, with lexical ordering within
each fragment directory.

Prompt data exposes `.Paths` as labeled absolute references for `office_root`,
`omo_dir`, `storage`, `workspace`, and every configured `repo:<key>`. Agent
rows persist the actual launch workdir so the workspace reference remains
truthful for worktrees and non-repository roles.

Model profiles remain generic `cmd + args + env`, despite the field name. Roles accept a scalar profile, a profile list, or a `models`/`assignment` mapping; repeated list entries are permitted as selection weights. Assignments are `round_robin`, `random`, retry-aware `failover`, or Claude/Codex-only `smart`. `internal/modelusage` is the narrow exception that reads native OAuth credentials and usage APIs: startup preflight is strict when enabled, `usage.safe_shutdown_percent` starts orderly handoffs, and the higher `usage.weekly_limit_percent` ceiling hard-stops the office. `usage.enabled: false` disables those calls and limits, with `smart` degrading to round-robin. Explicit per-job model choices take precedence but require a persisted `--force` approval above the soft ceiling. Profile arguments support `%prompt%` substitution independently from automatic provider/PTY injection; per-profile delay, retry count, and retry wait settings govern automatic delivery until `omo ready`. The optional `provider` field enables the narrow compatibility adapter in `internal/agentcli`; do not bake provider assumptions into the generic session package. Claude's persistent folder trust remains isolated in `internal/claudetrust`. Codex uses per-launch workspace/hook trust overrides, Gemini uses process-local workspace trust, and all are controlled by `trust_workdirs`.

`agents.env` supplies environment defaults to every agent PTY and to the
internal Git client used for worktrees, diffs, merges, and cleanup. Profile
`env` values override those shared defaults only for the profile's CLI, while
supervisor-owned `OMO_AGENT_ID` and `OMO_SOCKET` remain authoritative. The
default Git identity uses fallback expressions and backtick command
interpolation expanded by `internal/session`; commands execute through the
platform shell with parent control credentials removed. Profile environments
remain literal. Commit signing is disabled only for agent and internal Git
processes. Repository entries may be absolute or relative
to the office root; configuration loading resolves them to absolute runtime
paths without rewriting the portable YAML spelling.

## Persistence and concurrency invariants

- SQLite uses WAL mode, a busy timeout, foreign keys, and one open connection to serialize writes.
- Cumulative model statistics are idempotently upserted into one `overall_statistics` row per model on a timer and during orderly shutdown.
- Successful usage checks upsert one `model_usage_snapshots` row per credential scope; profiles sharing that scope reuse a process-wide, coalescing cache refreshed by `usage.refresh_interval`. Optional `usage.claude_config_dirs` and `usage.codex_homes` constrain account roots; multiple roots require each profile to select one through its environment. The TUI distinguishes account scopes while retaining Claude weekly/session and Codex weekly values.
- A role with no eligible metered profile is reported as blocked, but safe shutdown starts only when every configured Claude/Codex credential scope is capped.
- Usage-triggered shutdown stores a user-facing reason; the CLI prints it to stdout only after the TUI has returned and restored the terminal.
- Job transitions update state and append a `job_state` event in one transaction.
- Agent permissions, mail routing, and sender identity are enforced server-side, not only by prompts. Firefighter contact grants only the contacted agent a direct reply path; supervisor-authored mail uses the reserved `omo` sender, never `user`.
- Direct PTY input through `omo type` is server-authorized for only the user, CEO, firefighter, and trusted plugins running under the reserved system identity. A sanitized request event is persisted before queuing or writing, and a delivery event follows the actual PTY write; both record the target and input size/key count, never the input payload. Input targeting the agent under recent human editing in a writable TUI peek waits for the configured debounce or for that view to become non-writable, preserving FIFO text/key boundaries. The final readiness check happens while the session owns its input stream, and config reload invalidates old debounce timers. An explicit `input_debounce: 0s` consistently disables this protection. The TUI pending-input marker covers both queued direct input and mail notices.
- The supervisor owns session maps and wait channels; follow the existing mutex boundaries.
- TUI renders share one per-view data cache. Keep the live peek at its faster
  refresh cadence, avoid repeated database reads from footer/control helpers,
  and use bounded history-page queries rather than loading an unbounded event
  table during every repaint.
- Interactive cells are registered in a terminal-cell hit-map during each
  render; the map resets for every `View()`, and click dispatch reuses the
  equivalent keyboard behavior.
- Plugin hooks run in lexical plugin-directory and manifest order. Job-create
  authorization precedes mutable hooks; modified data flows through hooks in
  that order and then passes normal server-side validation. Manual manifests
  default `roles` to `["user"]`, accept `user` plus every `config.AllRoles`
  role, and are authorized against the authenticated server-side caller. Manual
  events expose `caller` and `caller_role`; audit details include action and
  argument count but never argument contents. Plugin config is passed as a Lua
  table or JSON command environment variable. Lua values are stored in SQLite;
  plugin code is trusted because command hooks and `omo.exec` can launch
  user-level processes.
- `prompt_render` runs before ordinary, restored-handoff, and `branch_namer`
  ready prompts are durably stored or returned. It exposes only `role`,
  `agent`, `job_id`, and mutable `text`, runs in lexical order, supports Lua and
  command hooks, and caps each plugin's cumulative append at 2 KiB per prompt.
- Cron plugin snapshots expose body-free `user_inbox`, latest CEO
  `ceo_activity_at_unix`, canonical `office_path`, current-session
  `office_started_at_unix`, and boolean `shutdown_in_progress`. `omo.http`
  permits HTTP(S) requests with mutually exclusive body modes, a 10-second
  default timeout, a 1 MiB response cap, same-host redirects, Go TLS defaults,
  and sanitized errors.
- Managed plugin repositories live under `.omo/plugins/.repos`. Activation
  copies a repository root or configured subpath atomically into
  `.omo/plugins/<name>`; disabled entries remain installed but are excluded
  when the runtime is loaded. An optional `branch` pins clone, startup update,
  and explicit update operations to one validated remote branch; switching the
  configured branch replaces the managed checkout branch on the next sync.
- Embedded-asset and managed-plugin update flows preview changes before their
  first write. `office.PlanTemplateUpdate` lists the complete replacement set,
  including files that disappear with an old directory.
  `pluginmanager.SyncAllWithPreview` and `SyncAllAtWithPreview` hold the plugin
  root lock across remote revision planning and sync, and invoke the output
  callback before changing a checkout or active copy. Existing checkouts query
  the configured branch (or the checkout's tracked branch when unpinned)
  rather than a possibly different remote HEAD.
- Plugin manifests may declare a `default_config` JSON object. Managed sync
  strictly decodes the manifest before activation and adds missing defaults
  to `plugins.installed.<name>.config`, including during startup or disabled
  plugin updates. Existing YAML values, comments, styles, and permissions are
  preserved; arrays, nulls, and type conflicts are never replaced. YAML
  aliases/merge keys receive additions locally without changing shared anchors.
  Sync owns the initial config entry as well as updates: do not separately
  upsert the entry after it. A config-write failure rolls activation back;
  failed rollback retains the previous directory backup for recovery. The Git
  cache can advance even when activation fails. Bundled sync uses the existing
  local manifest and preserves local plugin files.
- Plugin manifests may declare `requires` entries containing a plugin name,
  Git source, and optional subpath. Enabled global or local plugins satisfy a
  requirement by installation or manifest name. The runtime returns a typed,
  deterministic missing-dependency error; interactive startup can explicitly
  install or enable each office-local dependency and retry `office.Open`.
  Headless startup and declined prompts fail with an actionable install command.
  Dependencies remain enforced when startup update checks are skipped, and
  conflicting installation sources for one missing name fail closed.
- Global plugins live under the global home's `plugins/`, with managed Git
  checkouts in `plugins/.repos` and settings in its independent `config.yaml`.
  `plugins.LoadSources` selects by installation name: office directories or
  configuration entries override global ones, including disabled local entries.
  The effective set runs lexically using each selected scope's configuration;
  manifest aliases colliding across installation names remain errors. Runtime
  state/storage remains office-local. `pluginmanager.SyncAllAt` takes an explicit
  plugin root; global startup updates obey their own switch, and both scopes
  honor `--skip-startup-checks`. Plugin management commands default to the
  office-local scope; `--global` selects the global scope.
- Bundled plugin ownership is scoped: nudge and tools are office-owned, while
  filebrowser is global-owned and is never copied into office `.omo/plugins`.
  The filebrowser `default_config` supplies 50 MiB warnings and 1 GiB limits
  for both transfer directions; global `plugins.installed.filebrowser.config`
  overrides them. Disabling retains the config entry and directory, while
  removing only the entry while retaining the directory prevents automatic
  bundled reclaim. Filebrowser uses only plugin-owned dialogs, validates one
  basename component for uploads, confirms overwrites independently, uses
  portable `wc`/`base64`/`dd` argv, and refreshes after successful writes.
- Shared plugin roots use `plugins/.update.lock` across updating/loading
  processes. `Source.Shared` makes the loader select and copy global plugin
  files under that lock into private runtime snapshots before parsing manifests.
  Hooks use the snapshot for the manager lifetime, so another office's update
  cannot change its code/resources or expose an activation gap. `Manager.Close`
  waits for active hooks and removes snapshots; `office.Open` failure and normal
  close both release them. Cron workers are joined before `Manager.Run` returns.
  `LoadSourcesContext` lets callers bound waits for a shared-root lock. Snapshot
  temporary directories can remain after forced process termination.
- Plugin runtime state and its latest log line are stored durably per plugin.
  A separate per-line history is pruned synchronously to `plugins.log_lines`,
  and command stderr uses a bounded in-memory tail before persistence. Immutable
  command-hook stdout is discarded; mutable JSON stdout is capped at 64 KiB.
  Lua hooks log through `omo.log(message)`; command hook stderr is the
  equivalent log channel.
- Manual plugin hooks subscribe to `manual`; each requires a unique action
  `name` and non-empty `description`, with hook-local `manual_args` controlling
  optional string arguments. `omo plugin actions [plugin]` discovers loaded
  actions and their descriptions. `omo plugin trigger <plugin> <action>
  [-- <args>...]` connects from the office directory. The Plugins detail lists
  actions; `r` selects one and runs asynchronously with argument entry only
  when enabled for that action.
  Both enter the shared `Supervisor.TriggerPlugin` authorization boundary. The runtime
  targets one named hook in the loaded manifest, excludes disabled plugins,
  rejects manual broadcasts, and records a durable request before execution.
  Completion/failure audits identify the action and request without storing
  argument contents. A per-plugin admission guard rejects overlapping manual
  runs. `Manager.Close` closes admission, cancels active manual hooks, and waits
  through outcome persistence; office shutdown calls it before closing SQLite,
  and runtime cancellation also closes the manager. TUI busy/result state is
  per plugin. Requests interrupted by a crash are not replayed after restart.
  Command hooks and Lua `omo.exec` bound inherited output-pipe draining with a
  one-second `WaitDelay`, so canceled commands cannot keep shutdown waiting on
  pipe descriptors retained by descendants.
- The bundled nudge plugin is installed only when missing; setup, update, and
  startup must preserve user edits to an existing `.omo/plugins/nudge` copy.
  Scheduler snapshots expose lifecycle/job/mail metadata, while plugin nudges
  use the authorized `omo type` path to submit reminders without creating mail.
  It tracks freelancer waiting periods in plugin-local storage and reminds the
  CEO that finished retained freelancers require an explicit agent kill.
- Core mail delivery wakes parked agents or inserts one debounced inbox notice;
  repeated unread-mail and workflow reminders belong exclusively to the nudge
  plugin. Plugins can enumerate durable storage keys by prefix and should
  remove per-agent local state after agents leave the live snapshot.
- Common prompt path references emit each filesystem path once, combining
  semantic labels when the workspace, storage, office, or repository coincide.
- Git operations for a repository share one mutex. Do not bypass `internal/gitops` for merge/worktree mutations.
- Restart recovery is deliberately simple: living agents are marked dead and every non-terminal job is requeued. There is no transcript replay.
- Safe shutdown is the exception to no transcript replay: agents save concise role/job-keyed handoffs in `shutdown_contexts`; the next matching `omo ready` renders a handoff into its prompt and only then deletes the row. Safe shutdown halts spawning and stops after all targeted agents finish/checkpoint or its bounded deadline expires.
- Pushover and autoshutdown are optional official plugins installed from Git;
  they are not embedded or auto-installed. Safe-shutdown requests accept a
  reason, retain the first reason during idempotent in-progress requests, and
  display that reason after the TUI restores the terminal.
- Startup claims `.omo/omo.lock`, validates any recorded endpoint, and refuses a second live instance. The user can emergency-stop a live office over that endpoint; CEO and firefighter sessions have the same role-gated power.
- Read-only observation is the sole exception to single-owner startup: it ignores ownership state, never changes lifecycle rows or unread mail, and may display stale durable agent state when no owner is running.
- Each agent row stores the exact prompt returned by its latest `omo ready` handshake so a read-only TUI peek can display what that agent received.
- Agent identity comes from injected environment, not CLI arguments supplied by the model.
- Role prompts prohibit direct access to supervisor-owned `.omo` state (including SQLite and `omo.yaml`) unless the user explicitly requests a specific internal-file task. Job creators should pass substantial briefs with `omo job create --goal-file`; the CLI reads the file and stores its contents in the normal `jobs.goal` field.
- CEO, product-manager, smoke-alarm, and firefighter processes use `.omo/storage` as their shared working directory; developer/reviewer work remains in job worktrees and repository-scoped freelancers retain their worktree behavior. Storage files are ephemeral by default: each file expires after 60 distinct event-bearing office days since its last modification, and shutdown gaps do not count.
- The cleanup scheduler also caps historical SQLite rows per table. It must preserve live orchestration state and the event-day anchors used by storage retention even when that means temporarily exceeding a configured cap.
- Cross-platform process, socket, and replacement implementations use `_unix.go`/`_windows.go`; keep platform-specific APIs behind those files.
- Agent sessions own process-tree cleanup: Windows uses kill-on-close Job Objects, Linux supplements process-group termination with an inherited per-session marker so reparented background commands are swept, and other Unix systems snapshot descendants before killing their groups.
- Agent processes default to a Linux nice increment of 10 when `agents.lower_priority` is enabled, capped at nice 19. The session package owns this platform-specific adjustment; the omo process itself retains its original priority.
- Superpowers is installed once in the global home's `superpowers` directory and fast-forwarded on normal startup; prompts point agents directly at that shared checkout rather than relying on provider plugin state. Old executable-adjacent caches are left untouched and unused.
- `omo` must not modify user Git signing settings or commit office state.

## Tests

`make check` is the required local gate and matches CI:

1. `gofmt -l` must report nothing.
2. `go vet ./...` must pass.
3. `go test ./... -timeout 900s` must pass.

The integration suite builds `cmd/omo`, uses the scenario-driven fake agent, and launches real PTYs/ConPTYs. Tests may take tens of seconds. Use `t.TempDir`, isolated Git repositories, and the helpers in each package. Never depend on the developer's global Git configuration.

CLI, office, supervisor, and prompt test suites isolate `OMO_HOME` in temporary
directories so user templates/plugins/trust never affect tests. Feature tests
may override that location with `t.Setenv`.

Fake-agent scenario lines include commands such as `ready`, `shell|...`, `done|...`, `verdict|...`, `wait`, and `sleep|...`. Prefer them over mocking away the socket/session boundary when testing orchestration.

For asynchronous assertions, wait for a durable state or event rather than sleeping a fixed duration. In particular, use `job_merged` for post-merge filesystem assertions and `review_started` to distinguish successive review cycles.

## CI and releases

Workflows are intentionally separated so only relevant jobs appear:

- `.github/workflows/pull-request.yml`: test and cross-build on `pull_request`; it does not retain build artifacts.
- `.github/workflows/nightly.yml`: a scheduled run checks `main` for commits from the preceding 24 hours before test, cross-build, and seven-day artifact work; `workflow_dispatch` always runs that work.
- `.github/workflows/release.yml`: test, cross-build, package, checksum, and upload on a published GitHub release, then syncs the tag's `plugins/` tree to the stable `release` branch. `release` is stable; `main` is the latest development branch.

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
6. If user-visible behavior, configuration, CLI, layout, or release assets changed, update the matching `wiki/` page (and `README.md` when install or quick start change) and this guide.
7. Document only current behavior. This project is prerelease: do not record replaced behavior, deprecation language, or migration notes in the wiki, README, or this guide. Git history is the record of old behavior.

## Common mistakes

- Treating `done` as proof that worktree cleanup has completed; wait for `job_merged`.
- Writing directly to queue state instead of using `queue.Store.Transition`.
- Relying on prompt compliance for a permission that belongs in server-side auth/routing.
- Adding `.omo` to a repository `.gitignore`; setup deliberately uses `.git/info/exclude`.
- Putting Unix-only process/socket code in a shared file or forgetting the Windows counterpart.
- Calling external agent CLIs directly from orchestration code instead of going through `session` profiles.
- Changing release filenames without updating both installers and self-update logic.
- Using fixed sleeps for lifecycle tests when a stored event or state is available.
- Assuming README or wiki requirements override `go.mod`, workflow definitions, or executable behavior.
- Describing replaced behavior in documentation instead of replacing it with the current behavior.
