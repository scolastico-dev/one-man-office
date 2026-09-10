# Configuration

The office configuration lives in `.omo/omo.yaml`. Decoding is strict: unknown
or misspelled keys fail loudly. When `omo` loads a config that predates a new
setting, it writes back the missing key with its default while preserving
configured values and comments.

Apply edits to a running office with `omo reload`; see
[Running an office](running.md#reloading-configuration).

## Complete example

```yaml
repos:                        # local paths, absolute or relative to the office root
  api: /home/you/workspace/acme/api
  ui:  /home/you/workspace/acme/ui

models:                       # named runner profiles: just cmd + args + env
  claude-fable:
    provider: claude          # claude | codex | gemini; omit for custom CLIs
    cmd: claude
    args: ["--model", "fable", "--dangerously-skip-permissions"]
    env: {CLAUDE_CONFIG_DIR: /home/you/.claude-work}
    selectable: false         # the CEO may NOT choose this per job
  codex-astra:
    provider: codex
    cmd: codex
    args: ["--model", "gpt-6-astra", "--dangerously-bypass-approvals-and-sandbox"]
  opus:
    provider: claude
    cmd: claude
    args: ["--model", "opus", "--dangerously-skip-permissions"]
    env: {CLAUDE_CONFIG_DIR: /home/you/.claude-work}
  sonnet:
    provider: claude
    cmd: claude
    args: ["--model", "sonnet", "--dangerously-skip-permissions"]
    env:                       # passed only to this profile's CLI process
      CLAUDE_CONFIG_DIR: /home/you/.claude-personal
  haiku:
    provider: claude
    cmd: claude
    args: ["--model", "haiku", "--dangerously-skip-permissions"]
    env: {CLAUDE_CONFIG_DIR: /home/you/.claude-personal}

  # Custom delivery example. %prompt% substitution is explicit and remains
  # independent from automatic injection. Retries stop after `omo ready`.
  # custom:
  #   cmd: custom-agent
  #   args: ["--initial-prompt=%prompt%"]
  #   prompt_delay: 1s        # applies when launch args do not carry prompt
  #   inject_prompt: true
  #   prompt_retry_count: 3
  #   prompt_retry_wait: 30s

  # Alternative providers. Uncomment a complete profile after installing its
  # CLI, then assign the profile key to a role below.
  # codex-capable:
  #   provider: codex
  #   cmd: codex
  #   args: ["--model", "gpt-5.3-codex", "--dangerously-bypass-approvals-and-sandbox"]
  # codex-fast:
  #   provider: codex
  #   cmd: codex
  #   args: ["--model", "codex-mini-latest", "--dangerously-bypass-approvals-and-sandbox"]
  # gemini-auto:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "auto", "--yolo"]
  # gemini-pro:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "pro", "--yolo"]
  # gemini-fast:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "flash", "--yolo"]
  # gemini-light:
  #   provider: gemini
  #   cmd: gemini
  #   args: ["--model", "flash-lite", "--yolo"]

roles:                        # all seven roles are required
  ceo:
    models: [claude-fable, codex-astra]
    assignment: failover      # Fable first; Astra when Fable is unavailable
  product_manager: opus       # a bare string selects one profile
  developer:
    models: [sonnet, opus]
    assignment: round_robin   # round_robin | random | failover | smart
  reviewer: opus
  freelancer: sonnet
  smokealarm: haiku
  firefighter: opus

startup:
  check_self_update: true     # check the latest GitHub release on boot
  check_templates: true       # compare embedded template/plugin generation
  check_timeout: 5s

agents:
  ready_timeout: 2m           # handshake deadline
  start_prompt_delay: 2s      # fallback when a model has no prompt_delay
  max_spawn_retries: 2
  max_job_retries: 3
  lower_priority: true        # Linux: lower agent process priority
  nice_increment: 10          # added to inherited nice value, capped at 19
  env:                        # environment defaults for every agent PTY
    GIT_AUTHOR_NAME: "OMO - AI Orchestrator"
    GIT_AUTHOR_EMAIL: "omo@scolasti.co"
    GIT_COMMITTER_NAME: "${GIT_COMMITTER_NAME:${GIT_AUTHOR_NAME:-}}"
    GIT_COMMITTER_EMAIL: "${GIT_COMMITTER_EMAIL:${GIT_AUTHOR_EMAIL:-}}"
    GIT_CONFIG_PARAMETERS: "'commit.gpgSign=false' ${GIT_CONFIG_PARAMETERS:-}"

ceo:
  max_restarts: 3             # crash-loop protection
  restart_window: 30s
  restart_backoff: 500ms

limits:
  max_developers: 4           # concurrency caps
  max_freelancers: 2

branches:
  prefix: omo/job-            # fallback prefix for generated names
  naming: ai                  # ai | generated

usage:
  enabled: true               # false disables usage API calls and limits
  weekly_limit_percent: 90    # hard-stop ceiling for any watched window
  safe_shutdown_percent: 85   # stop spawning and request handoffs first
  refresh_interval: 10m       # proactive cache refresh; 0s disables scheduler
  claude_config_dirs:         # absolute Claude account configuration roots
    - /home/you/.claude-work
    - /home/you/.claude-personal
  codex_homes: []             # absolute CODEX_HOME account roots

smokealarm:
  enabled: true
  run_on_start: false
  mode: all                   # all | per_agent
  interval: 5m
  timeout: 2m                 # restart a round that does not finish
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
  input_debounce: 30s         # don't inject mail/type input while typing; 0s disables

plugins:
  update_on_start: true       # fast-forward managed Git plugins on boot
  log_lines: 500              # retained history lines per plugin; must be positive
  installed:
    nudge:                    # bundled workflow-reminder/example plugin
      source: builtin:nudge
      enabled: true
      config:
        check_interval: 1m
        activity_sample_interval: 30s
        reminders:
          inbox: {after: 5m, repeat: 15m}
          smokealarm_done: {after: 5m, repeat: 10m}
          park_completed: {after: 2m, repeat: 15m}
          reviewer_wait: {after: 5m, repeat: 15m}
          freelancer_waiting: {after: 5m, repeat: 15m}
          no_job_wait: {after: 15m, repeat: 30m}
          stale_work: {after: 15m, repeat: 30m}
    tools:                    # bundled CEO maintenance-action presets
      source: builtin:tools
      enabled: true

cleanup:                      # retention scheduler; 0 disables each rule
  interval: 1h
  read_messages_after: 0s     # delete mail this long after it is read
  terminal_jobs_after: 0s     # delete safe done/failed/cancelled leaf jobs
  storage_active_days: 60     # per-file age; shutdown days do not count
  max_entries:                # oldest safe rows; 0 disables each table cap
    agents: 10000
    jobs: 10000
    messages: 50000
    events: 100000
    incidents: 10000
    overall_statistics: 1000
    shutdown_contexts: 1000
    model_usage_snapshots: 100

# Satisfy supported CLIs' workspace and plugin trust gates for each agent
# working directory. Agents have nobody to answer interactive trust dialogs.
trust_workdirs: true
```

The example keeps the non-CEO roles Claude-only for brevity. Real setup
activates the first installed CLI in Claude -> Codex -> Gemini order unless
`--agent-cli` overrides it. A Claude-generated configuration starts the CEO
with Fable and falls back to Codex Astra; Codex- and Gemini-generated
configurations activate one account-default profile for the selected CLI and
leave the concrete alternatives commented.

## Repositories

`repos` maps a key to a local Git checkout. Paths may be absolute or relative
to the office root; loading resolves them to absolute runtime paths without
rewriting the YAML. The key is what agents pass to `omo job create --repo`.
Edit the list with `omo repo add`, `omo repo remove`, and `omo repo list`.

## Model profiles

`omo` has no concept of a "model". A profile is simply a command line: `cmd`,
`args`, and an optional `env` map passed only to that profile's process.
Set `provider: claude`, `provider: codex`, or `provider: gemini` to enable that
CLI's startup adapter; direct commands with those names are also detected
automatically.

The unattended flags grant agents broad access to the worktree. Review the
generated config and list only repositories you are prepared to let the
selected CLI modify.

### Initial prompt delivery

Every `%prompt%` substring in `args` is replaced before launch. This explicit
substitution is independent from `inject_prompt`: setting `inject_prompt: false`
disables automatic provider/PTY delivery without disabling `%prompt%`. For PTY
delivery, `prompt_delay` overrides the global `agents.start_prompt_delay`.
When automatic injection is enabled, `omo` resends the prompt if the agent has
not called `omo ready` within `prompt_retry_wait` (default `30s`);
`prompt_retry_count` is the number of additional sends (default `3`; `0`
sends once).

### Provider adapters and folder trust

Supported CLIs receive the initial `omo ready` instruction in the way their
interactive UI handles reliably:

- **Claude Code** receives a delayed PTY submission. `omo` records
  per-directory consent in `~/.claude.json` while preserving every unrelated
  setting.
- **Codex** receives the prompt as its initial positional argument. A
  per-launch config override trusts the exact workdir, and `TERM` is normalized
  for the PTY so no pre-prompt confirmation appears.
- **Gemini** receives `--prompt-interactive`, keeping the session open after
  its initial task. Workspace trust is granted only for that process through
  `GEMINI_CLI_TRUST_WORKSPACE=true`.

Set `trust_workdirs: false` to manage these trust gates yourself. With trust
automation disabled, a fresh worktree may block before the agent sees its
start prompt. Claude is the only adapter that edits a trust file.

### Selectable profiles

The CEO may pick any profile per job with `--model <key>` unless it is marked
`selectable: false`. A role can **run on** a profile it is forbidden to
**spawn**. PMs use the same `--model` flag when creating developer jobs. When
creating a PM job, the CEO can independently constrain its developers with
`--developer-models sonnet,haiku` or force one profile with
`--force-developer-model sonnet`; neither changes the PM's own model.

## Role assignment

A role may name one profile, use a bare profile list (which defaults to
`round_robin`), or configure `models` and `assignment`:

- `round_robin` rotates through eligible profiles.
- `random` chooses among them. Repeated list entries are weights:
  `models: [a, a, b]` gives `b` one third of selections.
- `failover` advances from the retry position, so a later profile is used only
  after an earlier one failed to start.
- `smart` chooses the first profile with the most capacity remaining, with
  configuration order breaking ties. Smart roles support Claude and Codex
  profiles and degrade to round-robin when usage checks are disabled.

## Usage limits

When `usage.enabled` is true, `omo` reads the native Claude Code and Codex
OAuth credentials and calls their usage APIs at startup. This preflight is
strict, even with `--skip-startup-checks`: missing credentials or unavailable
usage data stops startup before the office lock, database, recovery, or CEO
spawn.

Usage is account-scoped rather than model-scoped. Profiles sharing one
credential scope reuse the same request and resolve to Claude weekly, Claude
session, and Codex weekly windows. Set `usage.claude_config_dirs` and
`usage.codex_homes` to the absolute credential roots `omo` may use. A single
configured root is applied automatically to matching profiles. With multiple
roots, each matching profile must select one through `env.CLAUDE_CONFIG_DIR` or
`env.CODEX_HOME`; those variables are also passed to the CLI, so profiles can
use separate accounts. For Claude, `omo` mirrors the selected root into
`CLAUDE_SECURESTORAGE_CONFIG_DIR` so filesystem and macOS Keychain credentials
resolve to the same account.

Successful responses are cached, and simultaneous cache misses are coalesced
into one provider request. The scheduler refreshes each credential scope at
`usage.refresh_interval`; `0s` disables proactive refresh while retaining lazy
cache refresh. A runtime refresh failure falls back to round-robin for that
spawn and sends one user warning per consecutive failure streak. The TUI keeps
a separate usage row for each credential root.

Metered profiles at or above `usage.safe_shutdown_percent` in any watched
window are excluded from assignment. If a role has no eligible candidate,
`omo` reports that role as blocked. Safe shutdown begins only when every
configured Claude/Codex credential scope reaches the soft threshold; a capped
Claude role does not stop an office that still has Codex capacity, or vice
versa. If every scope reaches `usage.weekly_limit_percent` before handoffs
finish, `omo` stops immediately. After the TUI restores the terminal, `omo`
prints the usage reason for either exit to stdout.

An explicit per-job `--model` is rejected at the soft threshold with its
current percentage and window plus instructions to rerun with `--force`; the
force approval is persisted for that job and does not affect child jobs.

Set `usage.enabled: false` to disable usage API calls and enforcement entirely.

## Agent environment

`agents.env` supplies environment defaults to every agent PTY and to omo's
internal Git client for worktree, diff, merge, and cleanup operations. This
allows settings such as Git author/committer identity and `GIT_CONFIG_*`
variables to apply consistently. Profile `env` values remain specific to that
profile's CLI process and override the shared defaults byte-for-byte, while the
supervisor-owned `OMO_AGENT_ID` and `OMO_SOCKET` remain authoritative.

Shared `agents.env` values are expanded without a shell: `$VAR` and `${VAR}`
read the inherited process environment or another `agents.env` key, and
`${VAR:-fallback}` (or `${VAR:fallback}`) substitutes the fallback when the
variable is empty. Fallbacks may nest, as in the default committer identity
above. Profile `env` values are never expanded, so secrets containing `$` stay
intact.

The default Git identity makes agent commits and omo-created merge commits
attributable to omo and disables commit signing only for agent and internal Git
processes; your own Git configuration is never modified.

On Linux, `agents.lower_priority` runs agent processes with a nice increment of
`agents.nice_increment`, capped at nice 19. The `omo` process itself keeps its
original priority.

## Branch naming

`branches.naming: ai` (the default) starts a short-lived branch-naming agent
with the job brief, using the smoke-alarm role's profile selection. It returns a
Conventional Commits-style branch name such as `feat/add-search` or
`fix/login-timeout`, so branches group by change type. If the naming agent
times out, `omo` falls back to `<branches.prefix><job-id>` so dispatch can
continue. `branches.naming: generated` always uses that generated name. The
naming instructions are customizable in `.omo/messages/branch_naming_goal.txt`.

## Cleanup and retention

Storage cleanup deletes each `.omo/storage` file after
`cleanup.storage_active_days` distinct office-active days since its last
modification. Activity comes from event timestamps, so days while `omo` is shut
down do not count; `0` disables it. Agents receive the configured policy in
their common prompt so they can move durable deliverables into a repository.

The same scheduler caps durable SQLite history under `cleanup.max_entries`;
set an individual table to `0` to disable its cap. Caps delete the oldest safe
rows first. Living agents, unread mail, open incidents, non-terminal jobs, job
lineage with retained children, and storage-retention event-day anchors are
protected, so a table can temporarily remain above its cap. Cleanup runs once
when the office starts and then at `cleanup.interval`.

Plugin log history is bounded separately and synchronously by
`plugins.log_lines`.

## Recommended model choices

For the most reliable setup, we recommend a Claude Team Premium seat together
with ChatGPT Plus or Pro. In practice, that combination roughly matches the
usage limits of this office pattern: with a few concurrent agents and active
work across a normal week, we saw around 20–30 hours of useful active work
before both subscriptions reset. The config below uses the strongest models for
the right task while letting you fall back to lower-end models when budget or
speed matters. It is the setup that has proven to work well in practice, not
the only valid one.

```yaml
models:
  claude-fable:
    provider: claude
    cmd: claude
    args: ["--model", "fable", "--dangerously-skip-permissions"]
    selectable: false
  codex-astra:
    provider: codex
    cmd: codex
    args: ["--model", "gpt-6-astra", "--dangerously-bypass-approvals-and-sandbox"]
  claude-opus:
    provider: claude
    cmd: claude
    args: ["--model", "opus", "--dangerously-skip-permissions"]
  claude-sonnet:
    provider: claude
    cmd: claude
    args: ["--model", "sonnet", "--dangerously-skip-permissions"]
  claude-haiku:
    provider: claude
    cmd: claude
    args: ["--model", "haiku", "--dangerously-skip-permissions"]
  codex-sol:
    provider: codex
    cmd: codex
    args: ["--model", "gpt-5.6-sol", "--dangerously-bypass-approvals-and-sandbox"]
  codex-luna:
    provider: codex
    cmd: codex
    args: ["--model", "gpt-5.6-luna", "--dangerously-bypass-approvals-and-sandbox"]
  codex-mini:
    provider: codex
    cmd: codex
    args: ["--model", "gpt-5.4-mini", "--dangerously-bypass-approvals-and-sandbox"]
roles:
  ceo:
    models: [claude-fable, codex-astra]
    assignment: failover
  product_manager:
    models: [claude-opus, codex-sol]
    assignment: smart
  developer:
    models: [claude-sonnet, codex-luna]
    assignment: smart
  reviewer:
    models: [claude-opus, codex-sol]
    assignment: random
  freelancer:
    models: [claude-sonnet, codex-luna]
    assignment: smart
  smokealarm:
    models: [claude-haiku, codex-mini]
    assignment: failover
  firefighter: claude-opus
```

Gemini CLI's `auto` alias is the safest general Gemini default, while `pro`,
`flash`, and `flash-lite` trade capability for progressively faster or lighter
work. Model availability depends on the CLI version and account.

> ⚠️ **Regarding Gemini usage**: Gemini can be prone to context drift, and its
> pricing is generally less competitive than Claude or Codex. Even with a
> Gemini subscription, billing is per request rather than token-based, which
> means the frequent request pattern used by `omo` can add up quickly. We
> recommend Claude or Codex instead.
