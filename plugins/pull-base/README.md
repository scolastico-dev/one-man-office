# Pull base plugin

Work should not start from a stale base branch. This plugin holds a ticket
before its work begins, finds out whether the branch the ticket will be based
on is behind its upstream on the remote, and offers to pull it first (or pulls
it by itself, when you choose that).

**It changes the checked-out branch of the project folder when it pulls**: a
pull fast-forwards the branch that is checked out in the folder you registered
as the project, so that folder's files move to the newer commits. It is built
only from plugin capabilities (a gate on a ticket's move, `omo.exec`, a task
for the fetch, manual hooks for the choices); nothing of it is in omo's core.

It is an official plugin of omo's list: install it on the Plugins page under
"Available plugins". It installs from the `release` branch of omo's
repository, folder `plugins/pull-base`.

## What it checks

- **Which tickets:** stories, and tasks that do not belong to a story. A task
  inside a story is based on its story and is never checked (the plugin goes
  ahead at once).
- **What:** for every project of the ticket, the branch the ticket will be
  based on (the branch checked out in the project's folder, as omo's gate
  names it) against its upstream (`branch.<name>.remote` and `merge`, usually
  `origin/<name>`). The plugin fetches that upstream with git, for at most the
  configured timeout, and at most once per project and branch for one round
  of checks: several tickets checked together (within a minute of each other)
  share the fetch. A failed fetch counts for the round too.
- **Up to date, or ahead:** it goes ahead; nothing is shown.

## When the branch is behind

The ticket is held in its column with the reason

> `main` of app is 3 commits behind `origin/main`. Pull before starting?

(the real branch, project, remote branch and count) and three choices:

| Choice | What happens |
|---|---|
| **Pull and continue** | Fast-forwards the branch, then goes ahead. |
| **Continue without pulling** | Goes ahead; the folder is unchanged. |
| **Cancel** | Cancels the held move: a release stays in Planning, a start goes back to Planning. |

With **Pull automatically instead of asking** the plugin pulls and goes ahead
without a question.

A ticket with several projects gets one question for all projects that are
behind; the pull fast-forwards each of them.

## Only safe pulls

The plugin pulls with `git merge --ff-only` and nothing else: it never merges,
rebases, stashes, resets, checks out or discards anything. When a pull is not
possible it does not pull, in automatic mode either; it holds the ticket with
exactly one of these reasons, offers only **Continue without pulling** and
**Cancel**, and leaves the project folder as it is:

| Reason | When |
|---|---|
| `main` of app has diverged from `origin/main` (1 commit ahead, 2 behind) and cannot be fast-forwarded. | The branch has commits the upstream lacks and the upstream has commits the branch lacks. |
| Uncommitted changes in app would be overwritten by pulling `origin/main` into `main`. | A file the pull would change has uncommitted changes in the folder, or exists there untracked. Other uncommitted changes do not stop the pull. |
| `main` of app has no upstream branch to pull. | The branch has no upstream configured. |
| `origin/main` of app could not be fetched, so it is unknown whether `main` is behind it. | The remote cannot be reached, or the fetch ran into the timeout. |
| `main` is not the branch checked out in the folder of app, so it cannot be pulled safely. | The folder has another branch checked out. |

The fetch itself updates the remote-tracking branch (for example
`refs/remotes/origin/main`) of the project, like `git fetch` does.

## Configuration

| Key | Meaning |
|---|---|
| `mode` | `release`: check when a story or task is moved from Planning to Ready For Dev. `start`: check when omo is about to start a ticket in Ready For Dev, before it creates the worktree (the worktree is then based on the pulled branch). Default `release`. |
| `automatic` | `true` pulls without asking whenever the pull is safe. Default `false`. |
| `fetch_timeout_seconds` | How long one fetch may take, 1 to 240. Default 30. All fetches of one check together never run longer than 270 seconds. |

The plugin also reads `fetch_reuse_seconds` (default 60), how long one fetch
of a project and branch is reused by later checks; it is not shown on the
page, add it under **Configuration (JSON)** to change it.

## Failures

A gate that fails (crashes, runs out of time) lets the ticket through, with a
note on the ticket and a line in the plugin's log, like every gate (see
[Gates](../../wiki/plugins.md#gates)). You can always **continue anyway** or
cancel a held move from the ticket.
