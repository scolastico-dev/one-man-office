# Core concepts

![Core concepts overview](../.github/assets/core_concepts.jpg)

## How the office works

```text
   o   o
   │   │
  [o m o] ◀─ the office binary, owns every agent PTY
   │   │
   │   └────────────────┐
   │                    │
┌─ you ─────────────────┴──────────────────────────────────┐
│ omo TUI  -  peek/type into any agent, read notifications │
└───────────────────────┬──────────────────────────────────┘
                        │
                      CEO ─────────────► freelancer   (one-off research/task)
                        │
                 product manager  (one per spec)
                        │
                    developer  (one per job, own worktree + branch)
                        │
                     reviewer  (clean context: goal + diff only) ──► merge

         smoke alarm (every 5m, fresh) ──► incident ──► firefighter
```

You talk to the CEO. The CEO writes specs and delegates them to product
managers, who split the work into developer jobs. Each developer works in its
own worktree and branch, and a reviewer checks the result before it merges. A
smoke alarm watches for stuck or unhealthy agents and can raise an incident for
a firefighter to resolve.

`omo` is a single Go binary that owns every agent PTY itself. There is no tmux,
daemon, or attach workflow. Closing `omo` stops the office; the persistent job
queue makes restarts cheap.

### Use omo when

- A substantial feature spans several components or repositories and benefits
  from parallel planning, implementation, review, and integration.
- You have a backlog of reasonably independent jobs that should keep moving
  with durable status, supervision, review cycles, and restart recovery.

### Prefer a normal single-agent session when

- The task is a small, focused fix or question, where orchestration would add
  more coordination and token cost than useful work.
- The work is highly exploratory and needs a tight conversation with you in one
  shared context, especially when conserving tokens matters.

## The office

An **office** is a directory containing `.omo/omo.yaml`. It is either a
repository itself or a directory holding several repositories. Create one with
`omo setup`, then run `omo` inside it. Several independent offices can exist
side by side.

```bash
omo setup            # scaffold the current directory
omo setup my-office  # ...or a new one
omo setup --update   # replace messages, prompts, and bundled plugins
omo setup --sync     # reapply the partial global config override
```

`omo setup` writes the config, the `.omo` layout, an initialized empty
database, editable message and role-prompt templates, and bundled plugins.

In an interactive setup, an active global template can prefill role choices and
offer to skip their questions. The confirmation defaults to yes; it skips all
role and assignment questions when the template defines every role, or leaves
only undefined roles in the form for a partial template. Answer no to review
all roles. Unavailable template profiles and inactive templates do not count
as defined roles, and `--non-interactive` retains the default setup flow.

Once `.omo/omo.yaml` exists, ordinary `omo setup` preserves the office and only
creates missing extensions or bundled plugins. `omo setup --update [dir]`
replaces `.omo/messages`, `.omo/prompts`, and each bundled plugin directory
with the defaults from the installed `omo` version. This discards edits and
extra files in those embedded-asset directories, but does not touch other
plugins, the configuration, database, logs, worktrees, socket metadata, or
`.gitignore`. It also refreshes the embedded-generation marker used by the
startup freshness check.

```text
my-office/
└── .omo/
    ├── .gitignore    # ignores every office file except itself
    ├── omo.yaml      # configuration
    ├── omo.db        # jobs, messages, agents, events, incidents (SQLite, WAL)
    ├── omo.sock      # Unix: link to a short socket under the temp directory
    ├── omo.lock      # live instance's socket or named-pipe endpoint
    ├── messages/     # what omo says to agents, editable
    ├── prompts/      # common + role instructions, editable
    ├── extensions/   # optional role-preset prompt additions
    ├── plugins/      # event-driven Lua and command plugins
    ├── storage/      # shared workspace for CEO, PMs, smoke alarm, firefighter
    ├── worktrees/    # <repo>-<job-id>/ per developer job
    └── logs/         # one readable transcript per agent session
```

`.omo/` is excluded from Git through the repository's `.git/info/exclude`,
never through your `.gitignore`. See [Git integration](git-integration.md) for
committing an office deliberately.

### Office shapes

**Single repository**: the office lives inside the project. `omo setup` finds
the repository and writes `.omo/` next to its `.git`.

**Microservice landscape**: the office is a parent directory containing
several Git repositories.

```text
~/workspace/acme/          <- run omo here
├── api/                   <- a git repo
├── ui/                    <- a git repo
└── worker/                <- a git repo
```

In either shape, `repos:` in `.omo/omo.yaml` defines what the CEO is allowed
to work on. `omo repo list`, `omo repo add [name] <path>`, and
`omo repo remove <name>` edit that list. Cross-repository work is expressed as
one job per repository. The CEO is told which repositories exist and is asked
to pin the interface between services in the spec, so a UI job can start before
the corresponding API job has merged.

### Bundled Superpowers

The provided role prompts require [Superpowers](https://github.com/obra/superpowers).
`omo` installs a shared shallow checkout into `superpowers/` inside its
[global home](global-home.md) and fast-forwards it on every normal start.
Prompts point agents at that checkout's `skills/<skill>/SKILL.md` files, so
Claude, Codex, and Gemini need no provider-specific Superpowers installation.
If an update fails, `omo` warns and continues with the existing checkout.

## Roles

| Role | Lifetime | What it does |
|---|---|---|
| **CEO** | the whole office | Talks to you, brainstorms, writes specs, delegates to PMs and freelancers, picks per-job models/policies, and can halt new work spawns. Never finishes or parks in `omo wait`. |
| **Product manager** | one spec | Plans modest rolling batches of developer jobs, selects allowed models, answers questions, judges review disputes, and performs a final integrated self-review. |
| **Developer** | one job | Works in a dedicated worktree on the job branch; uses TDD and focused tests for changed packages and direct dependents, commits, and never merges. |
| **Reviewer** | one review cycle | Gets only the goal and branch diff, then runs the full repository/end-to-end suite. It may commit a tiny unambiguous fix, but rejects substantive work. After rejection it remains for questions; a normal re-review replaces it. |
| **Freelancer** | one job plus follow-ups | Handles bounded research, information gathering, configs, simple work, and spec drafts. After reporting completion it stays parked for CEO follow-up questions; completed freelancers do not consume the active-job concurrency limit. Add `--repo` to give one an isolated worktree. |
| **Smoke alarm** | one round | On schedule, performs one short inspection of all agents together or one per alarm, with authoritative agent/job lifecycle state, published step, unread-mail count, and current/prior output tails, then exits with `omo done`. A parked `omo wait` session is distinguished from a stalled worker. It raises at most one incident; timed-out rounds restart, and rounds pause while an incident or firefighter is active. |
| **Firefighter** | one incident | Outranks the CEO: pauses spawning, kills/restarts agents, cancels/requeues jobs, then reports to you. |

The CEO, product managers, smoke alarms, and firefighters run with
`.omo/storage` as their working directory. They keep coordination artifacts
there without cluttering the office root. Developer and repository-scoped
freelancer sessions use isolated Git worktrees.

Role prompts are exported into `.omo/prompts` and can be edited per office.
See [Prompts, messages, and extensions](prompts.md).

## Jobs and the merge lifecycle

Job states:

```text
queued -> assigned -> working -> review -> merging -> done
                                  |          |
                                  +-> rework +
```

- `rework`: the reviewer rejected the job; the same worktree remains, with new findings.
- `failed` and `cancelled`: terminal, but may be requeued.

PM and freelancer jobs skip review (`working -> merging -> done`).
`parent_job` records spec -> plan -> task lineage; a PM's job ID is forced onto
the developer jobs it creates, so lineage cannot be faked.

Every developer job names exactly one repository and gets a worktree at
`.omo/worktrees/<repo>-<id>`. With `branches.naming: ai` (the default) a
short-lived branch-naming agent chooses a Conventional Commits-style name such
as `feat/add-search`; otherwise the branch is `<branches.prefix><id>`. A
freelancer job can optionally name a repository and receives the same kind of
isolated worktree. Cancelled-job worktrees are removed after their agents stop;
a completed freelancer keeps its worktree until its retained session ends.
Startup reconciliation removes terminal worktrees left by an interrupted
shutdown.

Merges are serialized per repository. A conflicted merge is always aborted, so
the repository is never left mid-merge, and the job is handed back to the
reviewer to resolve in the worktree. On a successful merge the developer is
retired and the worktree and branch are removed. Developers never merge their
own branches.

`omo` never touches your Git signing configuration.

### Parallel work by design

The CEO or PM may pin probable interface contracts, such as likely API routes
and types, so UI and API jobs can start together before either side merges.
Job goals and review briefs describe the contract as provisional. Once both
sides land, the PM queues a focused integration/alignment job for any mismatch.
`omo` enforces concurrency caps internally, so PMs can keep a modest rolling
batch in the queue without serializing everything behind the first task.

## Agent communication and durable state

Agents drive `omo` through the same binary. Their identity comes from the
environment variables `omo` injects: `OMO_AGENT_ID` and `OMO_SOCKET`.

Every agent verb round-trips through the local socket or pipe, and **every
state change is committed to SQLite before the verb is acknowledged**. A crash
between verbs loses nothing.

When mail arrives for a running agent, `omo` types:

```text
You have new mail. Run: omo inbox
```

An agent parked in `omo wait` is released directly. Core sends only this
immediate notification; repeated unread-mail and workflow reminders belong to
the bundled nudge plugin and stop when that plugin is disabled.

### Who may talk to whom

Routing is enforced by the server, not merely suggested in a prompt.

| Sender | May send to |
|---|---|
| developer | its own PM and current reviewer for clarification |
| product manager | CEO, its own developers/current reviewers, **other PMs** (the only lateral channel) |
| freelancer | CEO |
| reviewer | the reviewed job's developer and that job's PM |
| smoke alarm | incidents only |
| CEO, firefighter | anyone |
| agent contacted by a firefighter | that firefighter (direct reply only) |
| **everyone** | **the CEO, always (emergency channel)** |

Any agent may reply directly to a firefighter that first contacted it; this
does not grant a general agent-to-firefighter channel. Agent-originated mail
keeps the authenticated agent as sender, while supervisor-generated
notifications use the distinct `omo` sender rather than impersonating the
human user. PM-to-PM traffic volume is an input to the smoke alarm: unusual
lateral chatter makes it inspect those PMs more closely.

## Restart recovery

**Restart recovery is deliberately dumb.** On startup, every non-terminal job
is requeued with a safety note that requires the agent to run `git status`
before taking any action, preserve all existing uncommitted changes, inspect
message history, and avoid destructive cleanup such as `git checkout .` or
`git reset --hard`.

Any incident left open by the previous process is marked resolved during
recovery. If the underlying problem persists, a later smoke-alarm round files a
fresh incident with current evidence.

A developer job that had already entered review receives a more specific note:
it records that review was underway and asks the developer to perform a brief
self-check, then promptly call `omo done` to start a fresh review unless more
work is needed.

There is no transcript replay. Agents re-derive state from the worktree and
their mail. The one exception is [safe shutdown](running.md#safe-shutdown),
where agents save concise handoffs that the next matching agent receives once.
