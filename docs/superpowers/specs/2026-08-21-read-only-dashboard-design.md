# Read-Only Dashboard Design

## Purpose

`omo --read-only` opens an observation-only TUI for an existing office. It may
run concurrently with the normal owning `omo` process and must be incapable of
starting agents, changing durable office state, sending socket commands, or
modifying configuration and generated files.

This is a separate startup path, not a normal office with spawning paused.
Pausing a normal office still owns sockets, recovery, mail delivery, and mutable
state; read-only mode owns none of those responsibilities.

## CLI contract

Add `--read-only` to the root `omo` command. It is valid only when running the
root dashboard and is incompatible with `--mock`, `--no-tui`, and `--safe-mode`.
Conflicting combinations return a usage error before opening office files.
`--skip-startup-checks` is accepted but redundant because read-only mode always
skips startup checks.

Read-only startup requires an existing `.omo/omo.yaml` and `.omo/omo.db`. It
does not create an office or any missing directory.

## Non-mutating open path

Add explicit read-only entry points instead of boolean flags hidden inside the
normal mutating open path:

- `config.LoadReadOnly` performs strict YAML decoding, defaults in memory, and
  validation without invoking missing-default writeback.
- `db.OpenReadOnly` opens the existing SQLite database with `mode=ro`, enables
  `query_only`, uses a busy timeout suitable for a concurrent WAL writer, and
  performs neither schema creation nor additive migrations.
- `office.OpenReadOnly` assembles only the configuration, read-only database,
  queue/mail readers, and supervisor-derived query helpers required by the TUI.
  It does not load editable message templates because Preview and spawning are
  unavailable.

The read-only path does not claim or inspect the instance lock as ownership,
create a transport endpoint, start a socket server, run recovery, mark agents
dead, requeue jobs, begin a statistics session, append events, exclude `.omo`
from Git, create directories, or launch goroutines. Closing it only closes the
read-only database handle.

SQLite WAL provides a consistent reader alongside the live writer. Normal
`busy` handling remains visible as a non-destructive TUI/read error rather than
retrying through a write transaction.

## TUI behavior

Read-only mode starts directly in Overview and labels both header and footer
with `READ ONLY`. It never starts on the CEO terminal because it owns no agent
sessions.

Visible tabs are:

- Agents
- Messages
- Jobs
- Incidents
- Events
- Statistics

Commands and Preview are omitted. Statistics show persisted overall totals;
the process-local current-session section is omitted because a second observer
cannot truthfully reconstruct the owning process's in-memory counters.

The six visible tabs retain navigation, scrolling, and detail views. Opening a
message shows its full content but does not mark it read. Agent rows are
informational and cannot be peeked. No key opens an action menu, message
composer, command console, preview input, safe-shutdown confirmation, or direct
agent input. `q` retains the normal quit confirmation and `Ctrl+C` exits only
the observer process; neither signals the owning office.

Footer controls list only actions that are actually available. Live unread
counts and role counts may be displayed because they are read queries.

## Concurrent and offline viewing

When a normal office is active, the observer displays its committed database
state as it changes. Agent status is the last committed lifecycle/step state;
the observer does not access PTYs or transcripts through the supervisor.

When no normal office is active, the observer displays the last durable state
without performing restart recovery. Consequently, agents that were living at
an unclean stop can still appear living. The header includes a concise note
that read-only data is an unmodified database snapshot, so stale lifecycle rows
are not mistaken for active processes.

## Safety boundaries

Read-only enforcement exists at three layers:

1. The CLI selects only the non-mutating open path.
2. SQLite rejects writes through `mode=ro` and `query_only` even if a caller
   accidentally reaches a mutating store method.
3. The TUI removes every mutating or session-dependent interaction.

This defense prevents a future UI regression from silently turning the viewer
into a writer. Read-only mode never connects as the reserved `user` identity
and never sends commands to a concurrently running office socket.

## Testing and documentation

Tests create a real temporary office database through the normal test helpers,
then reopen it read-only. They verify:

- concurrent open succeeds while an instance lock and writable DB connection
  exist;
- configuration missing defaults is not rewritten;
- no lock, socket, directory, event, message, migration, or recovery state is
  created or changed;
- direct write attempts fail at SQLite;
- incompatible flags fail before open;
- only the six observation tabs render;
- message detail does not change read state;
- mutating keys and agent Enter do nothing;
- statistics omit process-local session claims; and
- quitting the observer does not trigger the owning office's emergency stop.

Update the README startup options, TUI overview/control documentation, complete
CLI reference, and `CLAUDE.md` lifecycle description. The full `make check`
gate remains required.
