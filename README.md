# one-man-office (omo)

A single self-contained Go binary that runs a small **company of AI CLI agents**
in parallel across your repos. You talk to the CEO; the CEO writes specs and
delegates them to product managers, who break them into jobs for developers,
whose work is reviewed and merged by reviewers — while a smoke alarm watches
for stuck agents and a firefighter fixes what it finds.

omo is its own supervisor and its own terminal: every agent runs in a PTY that
omo owns, so there is no tmux, no daemon and no attach dance. Closing omo stops
the office; a persistent job queue makes restarting cheap.

> **A note on token usage:** omo is effective, but it is not token-efficient.
> Running several agents will burn through tokens. With the default model
> profiles, normal workloads generally stay within the rolling five-hour limits
> of Claude premium seats, but this is not guaranteed. If usage matters, choose
> lighter model profiles in `.omo/omo.yaml` or explicitly tell the CEO to use a
> lighter model for the work it delegates.

**Use omo when:**

- A substantial feature spans several components or repositories and benefits
  from parallel planning, implementation, review, and integration.
- You have a backlog of reasonably independent jobs that should keep moving
  with durable status, supervision, review cycles, and restart recovery.

**Prefer a normal single-agent session when:**

- The task is a small, focused fix or question; orchestration would add more
  coordination and token cost than useful work.
- The work is highly exploratory and needs a tight conversation with you in one
  shared context, especially when conserving tokens is important.

```
┌─ you ────────────────────────────────────────────────────┐
│ omo TUI  ── peek/type into any agent, read notifications │
└───────────────────────┬──────────────────────────────────┘
                        │
                      CEO ─────────────► freelancer   (one-off research)
                        │
                 product manager  (one per spec)
                        │
                   developer  (one per job, own worktree+branch)
                        │
                    reviewer  (clean context: goal + diff only) ──► merge

        smoke alarm (every 5m, fresh) ──► incident ──► firefighter
```

## Install

Quick install (Linux and macOS):

```bash
curl -fsSL https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/install.sh | sh
```

Or download and inspect the script before running it:

```bash
curl -fsSLO https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/install.sh
sh install.sh
```

Quick install (Windows PowerShell):

```powershell
irm https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/install.ps1 | iex
```

To inspect it first, run:

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/install.ps1 -OutFile install.ps1
.\install.ps1
```

The quick installers detect the operating system and CPU architecture, verify
the latest release checksum, install `omo` into a user-owned directory, and add
that directory to `PATH`. Running an installer again is safe: it keeps the
current release or updates it when a newer release is available. Set
`OMO_INSTALL_DIR` to choose a different directory. The default is
`~/.local/bin` on Linux/macOS and `%LOCALAPPDATA%\Programs\omo` on Windows.
Linux/macOS users may need to restart their shell after the first install;
Windows users should open a new terminal so other processes see the updated
user `PATH`.

Examples using a custom location:

```bash
OMO_INSTALL_DIR="$HOME/bin" sh install.sh
```

```powershell
$env:OMO_INSTALL_DIR = "$HOME\bin"
.\install.ps1
```

To build and install from source instead:

```bash
make install                 # → /usr/local/bin/omo  (may need sudo)
make install PREFIX=~/.local # → ~/.local/bin/omo
```

`omo` must be on the `PATH` of the agents themselves: they invoke it by name to
send mail, queue jobs and report completion.

Other targets: `make build` (→ `./bin/omo`), `make test`, `make check`
(fmt + vet + test), `make cross` (Linux/macOS/Windows, amd64 + arm64),
`make clean`, `make help`. Use `make cross-linux`, `cross-darwin`, or
`cross-windows` for one platform.

Requirements: Go ≥ 1.22 and `git`. The build is pure Go — `CGO_ENABLED=0`
everywhere, including the SQLite driver. Linux, macOS and Windows are
supported on amd64 and arm64. Windows uses ConPTY and a named pipe;
Linux/macOS use a PTY and Unix socket.

## License

Copyright (C) 2026 scolastico-dev. Licensed under the [GNU Affero General
Public License v3.0 or later](LICENSE).

## Quick start

> ⚠️ Before starting, make sure you have installed superpowers, as our
> provided prompt templates are built on top of it, or modify the templates. See
> [superpowers](https://github.com/obra/superpowers) for installation instructions.

omo works in two shapes, and `omo setup` detects which one you are in.

**A single repository** — the office lives inside the project:

```bash
cd ~/workspace/acme/my-service
omo setup          # finds this repo, writes .omo/
omo --mock         # dry run on fake agents: no model calls
omo                # for real
```

omo adds `/.omo/` to the repo's `.git/info/exclude`, so its database, logs and
worktrees never show up in your `git status` or in an agent's commit. Your
`.gitignore` is not touched.

**A microservice landscape** — the office is the parent directory:

```
~/workspace/acme/          ← run omo here
├── api/                   ← a git repo
├── ui/                    ← a git repo
└── worker/                ← a git repo
```

```bash
cd ~/workspace/acme
omo setup          # finds api, ui and worker
omo --mock
omo
```

Either way, check `repos:` in `.omo/omo.yaml` afterwards — that list is what
the CEO is allowed to work on. Add or remove entries by hand at any time;
paths must be absolute.

Then just talk to the CEO: omo opens on its screen. Describe what you want
built. It writes a spec, hands it to a product manager, and the work fans out
into developer jobs, each in its own worktree, each reviewed before it merges.

Cross-repo work is expressed as one job per repository. The CEO is told which
repositories exist and is asked to pin the interface between services in the
spec, so the UI job can start before the API job has merged.

## An office

An *office* is a directory containing a `.omo/omo.yaml`: either a repository
itself, or a directory holding several. Create one with `omo setup`, then run
`omo` inside it. Several independent offices can exist side by side.

```bash
omo setup            # scaffold the current directory
omo setup my-office  # …or a new one
omo setup --update   # replace messages and prompts in an existing office
```

`omo setup` writes the config, the `.omo` layout, an initialised (empty)
database and the editable message and role-prompt templates. Once
`.omo/omo.yaml` exists, ordinary `omo setup` ignores that office completely.
Use `omo setup --update [dir]` to replace `.omo/messages` and `.omo/prompts`
with the defaults from the installed omo version. This discards edits and
extra files in those two directories, but does not touch the configuration,
database, logs, worktrees, socket metadata, or `.gitignore`; it also refreshes
the small template-generation marker used by the boot check.

```
my-office/
└── .omo/
    ├── .gitignore    # ignores every office file except itself
    ├── omo.yaml      # configuration (below)
    ├── omo.db        # jobs, messages, agents, events, incidents (SQLite, WAL)
    ├── omo.sock      # Unix: link to a short socket under the temp directory
    ├── messages/     # what omo says to agents — editable, see below
    ├── prompts/      # common + role instructions — editable, see below
    ├── worktrees/    # <repo>-<job-id>/ per developer job
    └── logs/         # one readable transcript per agent session
```

### .omo/omo.yaml

```yaml
repos:                        # local paths only
  api: /home/you/workspace/acme/api
  ui:  /home/you/workspace/acme/ui

models:                       # named runner profiles: just cmd + args + env
  fable:
    cmd: claude
    args: ["--model", "fable", "--dangerously-skip-permissions"]
    selectable: false         # the CEO may NOT choose this per job
  opus:
    cmd: claude
    args: ["--model", "opus", "--dangerously-skip-permissions"]
  sonnet:
    cmd: claude
    args: ["--model", "sonnet", "--dangerously-skip-permissions"]
  haiku:
    cmd: claude
    args: ["--model", "haiku", "--dangerously-skip-permissions"]

roles:                        # default profile per role (all seven required)
  ceo: fable
  product_manager: opus
  developer: sonnet
  reviewer: opus
  freelancer: sonnet
  smokealarm: haiku
  firefighter: opus

startup:
  check_self_update: true     # check the latest GitHub release on boot
  check_templates: true       # compare editable-template generation marker
  check_timeout: 5s

agents:
  ready_timeout: 2m           # handshake deadline
  start_prompt_delay: 2s
  max_spawn_retries: 2
  max_job_retries: 3

ceo:
  max_restarts: 3             # crash-loop protection
  restart_window: 30s
  restart_backoff: 500ms

limits:
  max_developers: 4           # concurrency caps
  max_freelancers: 2

smokealarm:
  enabled: true
  run_on_start: false
  mode: all                   # all | per_agent
  interval: 5m
  tail_lines: 120             # how much recent output each round sees
  history_runs: 3             # prior tails included for comparison
  include_events: true
  include_pm_chatter: true

logs:                         # session transcripts in .omo/logs
  max_size_kb: 2048           # rotate the live log at this size
  keep: 50                    # completed sessions; live ones don't count

reviews:
  escalate_after: 2           # PM judges repeated rejection

notifications:
  repeat_interval: 3m         # repeat while inbox remains unread
  input_debounce: 30s         # don't insert while the user is typing

# Pre-accept Claude Code's "do you trust this folder?" dialog for each
# agent's working directory. Agents have nobody to answer it, so without
# this they hang on first run in a fresh worktree.
trust_workdirs: true
```

When omo loads an older valid config, it writes back any missing built-in keys
with their defaults while preserving configured values and comments. Unknown
keys remain errors, so typos still fail loudly.

omo has no concept of a "model" — a profile is a command line. The CEO may pick
any profile per job with `--model <key>` unless it is marked
`selectable: false`; a role can *run on* a profile it is forbidden to *spawn*.
PMs use the same `--model` flag when creating developer jobs. When creating a
PM job, the CEO can independently constrain its developers with
`--developer-models sonnet,haiku` or force one profile with
`--force-developer-model sonnet`; neither option changes the PM's own model.

On Unix, the real socket is created under the system temporary directory and
`.omo/omo.sock` links to it. This avoids the kernel's ~103-byte socket-path
limit for deeply nested offices. Windows uses an office-specific named pipe
and does not create a socket file.

## Running

```bash
cd my-office
omo                 # opens the TUI, starting on the CEO's screen
omo --mock          # same org chart driven by scripted fake agents (no AI)
omo --no-tui        # headless, for CI; Ctrl+C stops it
```

Before an interactive start, omo checks for a newer release and for prompt or
message defaults from a newer template generation. It asks before downloading
a checksum-verified release or running the equivalent of `omo setup --update`,
then restarts itself. Non-interactive/headless starts only print availability;
they never accept on your behalf. Disable either check under `startup`, or use
`--skip-startup-checks` for one invocation. Template freshness uses
`.omo/templates.sha256`, so local edits are not mistaken for an old generation.

`--mock` is the fastest way to see the whole machine work: it runs the full
CEO → PM → developer → reviewer → merge chain with scripted fake agents and no
model calls, using the first repository in your config.

### Complete CLI reference

There are two kinds of command. Standalone commands work in your normal shell.
Live-office commands use `OMO_AGENT_ID` and `OMO_SOCKET`, which omo injects into
each agent terminal; they therefore run as that selected agent and remain
subject to its role permissions. A human can issue a shared command by peeking
that agent in the TUI, enabling input with `Ctrl+T` when necessary, and typing
the command there.

Every command accepts `-h`/`--help`. `omo help [command]` shows the same command
help, and `omo -v`/`omo --version` prints the installed version.

#### Commands for the user

These are the normal entry points expected to be run directly from your shell.

| Command | Arguments and flags | Purpose |
|---|---|---|
| `omo` | `--mock`, `--no-tui`, `--skip-startup-checks` | Start the office. `--mock` uses scripted agents; `--no-tui` runs headless until `Ctrl+C`; `--skip-startup-checks` suppresses release/template checks once. |
| `omo setup [dir]` | Optional destination directory; defaults to `.` | Create a new office. Does nothing if `.omo/omo.yaml` already exists. |
| `omo setup --update [dir]` | Optional existing office directory; defaults to `.` | Replace `.omo/messages` and `.omo/prompts` with this binary's defaults and refresh their generation marker. Existing edits and extra files in those directories are removed. |
| `omo completion <shell>` | Shell is `bash`, `fish`, `powershell`, or `zsh`; each accepts `--no-descriptions` | Print a shell-completion script to standard output. |
| `omo --help` | Also `omo <command> --help` | Show the command tree or help for one command. |
| `omo --version` | Short form: `-v` | Print the omo version. |

#### Commands for both the user and agents

These inspect or operate a running office. Agents use them directly; a human
uses them from an appropriate agent terminal in the TUI. Permission notes are
enforced by the supervisor, regardless of who is typing.

| Command | Arguments and flags | Purpose / permission |
|---|---|---|
| `omo office pause` | None | Pause spawning new agents. Firefighter identity required. |
| `omo office resume` | None | Resume spawning. Firefighter identity required. |
| `omo office halt-spawns` | None | CEO: halt new work-agent spawns. Queued work and smoke/fire safety monitoring remain active. |
| `omo office resume-spawns` | None | CEO: resume new work-agent spawns. |
| `omo agent list` | None | List all living agents with role, lifecycle state, job, and published step. |
| `omo agent kill <name-or-role>` | Exact agent name or role | Stop matching agents and requeue their work. Firefighter identity required. |
| `omo agent restart <name-or-role>` | Exact agent name or role | Stop matching agents and let the supervisor respawn their work. Firefighter identity required. |
| `omo job list` | None | List jobs visible in the office queue. |
| `omo job show <id>` | Numeric job ID | Show the complete stored job. |
| `omo job cancel <id>` | Numeric job ID | Cancel a job. Firefighter identity required. |
| `omo job requeue <id>` | Numeric job ID | Requeue a failed or cancelled job. Firefighter identity required. |
| `omo inbox` | None | List unread mail for the current agent identity. |
| `omo read <id>` | Numeric message ID | Show one message and mark it read. |
| `omo send [body]` | `-s`/`--subject` required; `-t`/`--to` target; `-p`/`--priority` is `low`, `normal`, `high`, or `urgent` (default `normal`) | Send mail as the current agent. Omit `--to` to broadcast; omit the body argument to read it from stdin. Normal mail-routing rules apply. |

#### Commands mostly for agents

These drive the orchestration protocol and are normally generated by role
prompts rather than typed by the user.

| Command | Arguments and flags | Purpose / permission |
|---|---|---|
| `omo ready` | None | Report that the process is alive and print its common prompt, role prompt, and goal. |
| `omo step "<current status>"` | One non-empty description, at most 500 characters | Publish the agent's current activity to `omo agent list` and the TUI. Quote descriptions containing spaces. |
| `omo done [result]` | Optional single result argument | Report goal completion. Quote a multi-word result. The CEO cannot finish. |
| `omo wait` | None | Park until mail or a supervisor release arrives. The CEO cannot wait. |
| `omo job create` | `--title`, `--goal`, and `--role` required; optional `--model`, `--repo`, `--parent`, `--developer-models`, `--force-developer-model` | Queue a `product_manager`, `developer`, or `freelancer` job. `--repo` is required for developer jobs. Developer policy flags are CEO-only and valid only on PM jobs. Role-gated. |
| `omo job verdict <id> <merge\|reject>` | Optional `--notes`; required when rejecting | Submit a review verdict. Reviewer identity required. |
| `omo job override <id>` | `--notes` required | Have the owning PM overrule an out-of-scope or nitpicking rejection and direct the retained reviewer to merge. |
| `omo incident create` | `--agent` and `--class` required; optional `--detail`. Class: `stuck`, `looping`, `drifting`, `too-slow`, or `other` | File an incident and request a firefighter. Smoke-alarm or firefighter identity required. |
| `omo incident resolve <id>` | `--report` required | Resolve an incident and send its report to the user. Firefighter identity required. |

`omo fake-agent [--scenario <file>] [--auto-role <role>]` is a hidden internal
test command used by `--mock` and the integration suite. It is not part of the
normal user or agent workflow.

### The TUI

Two surfaces:

- **Peek** (the home screen, on the CEO) — the agent's live terminal. Everything
  you type goes to it, so talking to the CEO is just using its session.
  `Ctrl+O` or `Ctrl+Q` → overview, `Ctrl+T` → toggle read-only. `Ctrl+O` is a
  macOS-safe alternative; a terminal-level `Cmd+Q` cannot be intercepted by a
  terminal application. Peeking any *other* agent
  opens read-only, so a stray keystroke cannot derail a working agent;
  `Ctrl+T` lets you type to it anyway when you mean to. Mouse-wheel events are
  forwarded to the nested CLI, so its conversation remains scrollable.
- **Overview** — four tabs: live agents, full office-message history (including
  read and inter-agent mail), all jobs, and current-session statistics. The
  agent list is an indented spawn tree (CEO → PM → developer → reviewer), so
  related agents stay together instead of appearing in start order. Statistics include
  messages, agent starts by role, review outcomes, session duration, and
  active/idle worker time per model (CEO excluded). `Tab`/`←`/`→` switch tabs,
  `↑`/`↓` select, `Enter` peeks, and `x` reads a selected message. `q` opens a
  `y/N` confirmation; `Ctrl+C` remains the immediate emergency stop.

The full-width footer shows active/total counts for every live role. If mail
arrives while you type into an agent, a pending-mail marker appears and omo
waits for `input_debounce`, or for overview/read-only mode, before inserting
the nudge. Switching away may leave partly composed text in the nested CLI.
Compose long text elsewhere and paste it into omo when ready.

Agents publish their current activity with `omo step "..."`. The Agents tab
shows that description beside lifecycle state and job; agents can inspect the
same live view with `omo agent list`.

### Folder trust

Claude Code asks "do you trust the files in this folder?" the first time it
runs somewhere new. An agent has nobody to answer that, and the dialog eats
the start prompt, so omo records the same consent the user would give — per
working directory, in `~/.claude.json` — before spawning. Set
`trust_workdirs: false` to manage trust yourself. Nothing else in that file is
touched, but note omo and a running Claude Code both write it, so avoid
`omo setup` mid-session if you are being careful.

## What omo says: .omo/messages and .omo/prompts

Every line omo itself puts in front of an agent is a template in
`.omo/messages`, rendered with Go `text/template`:

| File | When | Placeholders |
|---|---|---|
| `start_prompt.txt` | typed into a fresh session | `.Name` |
| `mail_nudge.txt` | typed when mail arrives | — |
| `restart_note.txt` | appended to a requeued job's goal | — |
| `ceo_goal.txt` | the CEO's standing goal | — |
| `review_goal.txt` | the reviewer's clean-context briefing | `.JobID` `.Title` `.Goal` `.Branch` `.Diff` |
| `firefighter_goal.txt` | the incident briefing | `.ID` `.Agent` `.Class` `.Detail` `.Snapshot` |
| `smokealarm_goal.txt` | the inspection round's input | `.Report` |
| `spawn_failed.txt` | agent never started | `.Name` `.Role` `.Attempts` |
| `job_failed.txt` | job exceeded its retries | `.JobID` `.Title` `.Count` |
| `review_failed.txt` | reviewers kept dying | `.JobID` `.Count` |
| `review_escalated.txt` | consecutive rejects reached the PM threshold | `.JobID` `.Count` |
| `ceo_gave_up.txt` | omo stopped respawning the CEO | `.Count` `.Window` |
| `review_override.txt` | PM overrules a rejection and directs merge | `.JobID` `.Notes` |

Edit a file to change what omo says; delete it to fall back to the built-in
default. A malformed template fails at startup rather than silently reverting.
Keep the `INCIDENT_ID: {{.ID}}` line in `firefighter_goal.txt` — the resolve
flow parses it back out.

`omo setup` also exports `.omo/prompts/common.md` and one `<role>.md` file per
role. omo reads them at startup and falls back to embedded defaults only for
missing files. Ordinary setup leaves an existing office alone; use
`omo setup --update` when you intentionally want to reset both editable
template directories to the installed defaults. Startup freshness checking can
be disabled with `startup.check_templates: false`.

## Roles

| Role | Lifetime | What it does |
|---|---|---|
| **CEO** | the whole office | Talks to you, brainstorms, writes specs, delegates to PMs and freelancers, picks per-job models/policies, and can halt new work spawns. Never finishes or parks in `omo wait`. |
| **Product manager** | one spec | Plans modest rolling batches of developer jobs, selects allowed models, answers questions, judges review disputes, and performs a final integrated self-review. |
| **Developer** | one job | Works in a dedicated worktree on `omo/job-<id>`; TDD, commits, never merges. |
| **Reviewer** | one review cycle | Gets only the goal and branch diff. It may commit a tiny unambiguous fix, but rejects substantive work. After rejection it remains for questions; a normal re-review replaces it. |
| **Freelancer** | one job | The CEO's odd jobs: research, configs, spec drafts. |
| **Smoke alarm** | one round | On schedule, inspects all agents together or one per alarm, with current and prior output tails. It raises at most one incident; smoke rounds pause while that incident/firefighter is active. |
| **Firefighter** | one incident | Outranks the CEO: pauses spawning, kills/restarts agents, cancels/requeues jobs, then reports to you. |

Role prompts have embedded defaults, are exported into `.omo/prompts`, and mandate the matching
[superpowers](https://github.com/obra/superpowers) skills (brainstorming,
writing-plans, executing-plans, TDD, verification). Edit them per office in
`.omo/prompts/<role>.md`. omo warns at
startup if a `claude` profile is configured but the plugin is missing.

### Parallel work by design

The CEO/PM may pin probable interface contracts (likely API routes and types)
so UI and API jobs start together before either side is merged. Both goals and
review briefs should call the contract provisional. Once both sides land, the
PM queues a focused integration/alignment job for any mismatch. omo enforces
concurrency caps internally, so PMs can maintain a modest rolling batch in the
queue without serializing everything behind the first task or flooding it with
speculative work.

## Agent verbs

Agents drive omo through the same binary; identity comes from the environment
omo injects (`OMO_AGENT_ID`, `OMO_SOCKET`). Every verb round-trips through the
local socket/pipe, and **every state change is committed to SQLite before the verb is
acknowledged** — a crash between verbs loses nothing.

The complete syntax, flags, audience, and role restrictions are listed in the
[CLI reference](#complete-cli-reference) directly below **Running**.

When mail arrives for a running agent, omo types
`You have new mail. Run: omo inbox` into its terminal; an agent parked in
`omo wait` is released directly. Unread mail is nudged again after the
configured interval, subject to the TUI typing debounce described above.

### Who may talk to whom

Routing is enforced by the server, not merely suggested in a prompt:

| Sender | May send to |
|---|---|
| developer | its own PM and current reviewer for clarification |
| product manager | CEO, its own developers/current reviewers, **other PMs** (the only lateral channel) |
| freelancer | CEO |
| reviewer | the reviewed job's developer and that job's PM |
| smoke alarm | incidents only |
| CEO, firefighter | anyone |
| **everyone** | **the CEO — always (emergency channel)** |

PM↔PM traffic volume is an input to the smoke alarm: unusual lateral chatter
makes it look at those PMs closely.

## Jobs

States: `queued → assigned → working → review → merging → done`, plus `rework`
(reviewer rejected — same worktree, new findings), `failed` and `cancelled`.
`parent_job` records spec → plan → task lineage, and a PM's job id is forced
onto the developer jobs it creates, so lineage cannot be faked.

Every developer job names exactly one repository and gets a worktree at
`.omo/worktrees/<repo>-<id>` on branch `omo/job-<id>` — the same mechanism in
both layouts. Work spanning services becomes one job per repository. Merges are serialized per repo; a conflicted merge is always
aborted (the repo is never left mid-merge) and handed back to the reviewer to
resolve in the worktree. On a successful merge the developer is retired and the
worktree and branch are removed.

**Restart recovery is deliberately dumb.** On startup every non-terminal job is
requeued with the note *"this is a restart — inspect the worktree and message
history and determine what remains"*. There is no transcript replay: agents
re-derive state from the worktree and their mail.

omo never touches your git signing configuration.

## Logs

Each spawn gets its own transcript at
`.omo/logs/yyyy-mm-dd_hh-mm-<role>-<name>.log`, so the directory sorts
chronologically. omo interprets the full-screen terminal and writes changed
screen lines, so logs are readable line-oriented text instead of concatenated
cursor redraws. Recent lines are deduplicated even when the CLI moves them to a
different row, while transient spinners, empty prompts, separators, and status
bar chrome are omitted. Large live sessions rotate into `.log.1`, `.log.2`, ….

`logs.keep` counts completed session groups across the office. Living agents
are excluded, so `keep: 10` can leave more than ten groups while agents are
active. The default is 50; `0` removes completed logs and `-1` disables inactive
log pruning. Every rotated segment belonging to a retained session stays with
that session.

## Testing

The kernel is fully testable without any AI: a scenario-driven fake agent
(`omo fake-agent`) stands in for the CLI, and the integration tests spawn it in
real PTYs/ConPTYs to exercise spawn → queue → review → merge, the smoke-alarm →
firefighter path and restart recovery.

```bash
make test
```

## Out of scope (v1)

Detachable daemon mode and remote attach; Gas-Town-style crash-context
restoration; provisioning repos from clone URLs; officially supported non-Claude
CLIs (the profile mechanism does not preclude them, but only Claude Code is
tested).
