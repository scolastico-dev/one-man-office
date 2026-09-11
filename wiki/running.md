# Running an office

```bash
cd my-office
omo                 # opens the TUI, starting on the CEO's screen
omo --mock          # same org chart driven by scripted fake agents, no AI
omo --no-tui        # headless, for CI; Ctrl+C stops it
omo --safe-mode     # start only the CEO until spawning is resumed
omo --read-only     # observe an existing office without locking or mutating it
```

`--mock` is the fastest way to see the whole machine work. It runs the full
CEO -> PM -> developer -> reviewer -> merge chain with scripted fake agents and
no model calls, using the first repository in your config.

Every start requires the office location to be [trusted](global-home.md#office-trust).

## Safe mode

Start with `omo --safe-mode` to debug the office or provision new agent rules
before normal work begins. The CEO starts with an explicit safe-mode
instruction, while product managers, developers, reviewers, freelancers, smoke
alarms, and firefighters are blocked from spawning. Queued work is preserved.
After discussing the change with the CEO, either you (from the office
directory) or the CEO can run `omo office resume-spawns` to exit safe mode and
boot the full office.

## Read-only observation

`--read-only` opens a concurrent observer with Agents, Messages, Jobs,
Incidents, Events, and persisted Statistics tabs. It does not claim the office
lock, connect to the command socket, run recovery, spawn agents, mark messages
read, or expose management actions. When no owner is running, lifecycle rows
are an unmodified database snapshot and can therefore be stale. Read-only mode
cannot be combined with `--mock`, `--no-tui`, `--safe-mode`, or
`--trust-office`.

## Startup checks

Before an interactive start, `omo` checks for:

- A newer release.
- Prompt, message, or bundled-plugin defaults from a newer embedded generation.
- Remote updates for managed Git plugins, local and global.

Separately, every normal start installs or fast-forwards omo's shared
Superpowers checkout.

`omo` asks before downloading a checksum-verified release or replacing editable
templates and bundled plugins with `omo setup --update`, then restarts itself.
Before any accepted embedded-asset write, it lists every file that would
change, including obsolete files removed by directory replacement. Managed
plugin checks query remote revisions without changing their checkout, list each
pending local or global plugin update, and only then perform the update.

Non-interactive or headless starts only print availability. They never accept
on your behalf.

Disable individual checks under `startup` in `omo.yaml`, or use
`--skip-startup-checks` for one invocation. Embedded-asset freshness uses
`.omo/templates.sha256`, so local edits are not mistaken for an old generation.
`--skip-startup-checks` never bypasses office trust or plugin dependency
enforcement.

## Manual self-update

`omo self-update` works from any directory and never starts or restarts an
office. It downloads and verifies the platform release asset before replacing
the running executable.

```bash
omo self-update                    # install the latest release only when newer
omo self-update --check            # report a newer release without writing
omo self-update --version v1.2.3   # install this exact release, including a downgrade
```

`--check` and `--version` cannot be combined. An explicit version searches the
current directory and its parents for `.omo/omo.yaml`; when found, it disables
`startup.check_self_update` without rewriting the rest of the configuration,
so the next office start does not immediately replace a deliberately selected
version. Outside an office, the binary update still succeeds and reports that
no office setting changed.

## Single-instance lock and emergency stop

While an office is live, `.omo/omo.lock` records its real socket or named-pipe
endpoint. A second `omo` validates the endpoint and offers to emergency-stop
the existing session before starting; stale locks are discarded. `omo estop`
sends the same shutdown request directly from the office directory. The CEO
and firefighter can issue the same command from their sessions.

On Unix, the real socket is created under the system temporary directory and
`.omo/omo.sock` links to it, avoiding the kernel's roughly 103-byte
socket-path limit for deeply nested offices. Windows uses an office-specific
named pipe and creates no socket file.

## Safe shutdown

`omo safe-shutdown [--reason "<text>"]` (or `s` in the TUI quit dialog) halts new spawns,
broadcasts and injects a handoff request into every agent, and stops after
every targeted agent finishes or checkpoints, or after a bounded deadline.
Agents persist concise handoffs with `omo context save`; the next agent with
the same role and job receives that handoff in its prompt and the stored row is
then deleted. A reason is retained for the first in-progress request and is
printed as `omo exited: <reason>` after the TUI returns control of the terminal.
Repeated requests during shutdown are idempotent and do not replace that first
reason.

Safe shutdown also starts automatically when every configured Claude/Codex
credential scope reaches `usage.safe_shutdown_percent`. See
[usage limits](configuration.md#usage-limits).

Normal, safe, and emergency shutdown all reap commands started by agent
sessions. On Windows each session owns a kill-on-close Job Object. On Linux a
per-session process marker also finds background commands that detached and were
reparented after their agent exited; other Unix systems terminate the session's
process groups and observed descendants. This cleanup is defense in depth, not
a sandbox: a hostile process can deliberately escape user-level tracking, so
agents must still not create unattended destructive loops.

## Reloading configuration

While the office is running, `omo reload` validates `.omo/omo.yaml` and
atomically applies it to subsequent scheduling, spawning, and completion work
without killing existing agents. It can be run by you from the office directory
or by the CEO/firefighter inside their sessions. Existing processes keep the
command line, environment, prompt, and worktree they started with.

## Logs

Each spawn gets its own transcript at:

```text
.omo/logs/yyyy-mm-dd_hh-mm-<role>-<name>.log
```

The directory therefore sorts chronologically.

`omo` interprets the full-screen terminal and writes changed screen lines, so
logs are readable line-oriented text instead of concatenated cursor redraws.
Recent lines are deduplicated even when the CLI moves them to a different row.
Transient spinners, empty prompts, separators, and status-bar chrome are
omitted.

Large live sessions rotate into `.log.1`, `.log.2`, and so on.

You, the CEO, and the firefighter can inspect a living developer without
opening its TUI session with `omo logs <developer-name> -n <lines>`.

`logs.keep` counts completed session groups across the office. Living agents
are excluded, so `keep: 10` can leave more than ten groups while agents are
active.

- Default: `50`
- `0`: remove completed logs
- `-1`: disable inactive log pruning

Every rotated segment belonging to a retained session stays with that session.

## Manual agent input

You, the CEO, and the firefighter can answer an interactive confirmation or
menu without restarting an agent and losing its context. Send literal text,
named keys, or both in order:

```bash
omo type developer-ada "yes" --key enter
omo type developer-ada "1" --key enter
omo type developer-ada --key down,down,enter
omo type developer-ada --key ctrl+c
```

Supported named keys are `enter`/`return`, `tab`, `space`, `escape`/`esc`,
`backspace`, `delete`, arrow keys, `home`, `end`, page up/down aliases, and
`ctrl+a` through `ctrl+z`. Inspect the agent output first and send only the
minimum input required; arbitrary terminal input has the same power as typing
directly into that agent in the TUI.

Input targeting the agent you are currently typing into in a writable TUI peek
waits for `notifications.input_debounce` or for that view to become
non-writable. Queued requests keep their order and keep text separate from
following special keys. The same path delivers mail notices and plugin nudges;
a pending-input marker appears in the agent footer while input is waiting.

## Token usage

`omo` is effective, but it is not token-efficient. Running several agents can
burn through tokens quickly. Usage depends on your chosen CLI, account, models,
task, and concurrency. Configure `usage.safe_shutdown_percent` for orderly
handoffs and `usage.weekly_limit_percent` for the hard ceiling, and use
`assignment: smart` to prefer the eligible Claude/Codex profile with the most
capacity left. See [Configuration](configuration.md#usage-limits).
