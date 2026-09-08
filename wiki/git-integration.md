# Git integration

By default an office is runtime state. In a single-repository office `omo`
adds `/.omo/` to the repository's `.git/info/exclude`, so its database, logs,
and worktrees never appear in `git status` or in an agent's commit. Your
`.gitignore` is not touched.

## Committing an office with `--with-git`

`omo setup --with-git` turns the office into a portable, commit-ready handoff:

- Repository paths in `omo.yaml` become relative to the office root.
- OMO's own `.git/info/exclude` entry is removed.
- A selective `.omo/.gitignore` is written that exposes configuration,
  prompts, messages, extensions, plugins, specs, and job handoffs while
  keeping the database, locks, sockets, logs, storage, worktrees, and plugin
  checkout caches ignored.
- In an interactive terminal, enabled global plugins that are missing from the
  office are offered, preselected, so they can become repository-local and
  reviewable.
- Single-repository offices record `omo.gitIntegration=true` in local Git
  config.

Do not turn other office runtime state into tracked project data.

## Exporting and importing job handoffs

With Git integration enabled, `omo export git` writes portable job handoffs
under `.omo/jobs/{active,completed}/<year>/<month>/` and PM specs under
`.omo/specs/`. Git-integrated offices run this export automatically during
shutdown.

The Jobs tab shows unimported handoffs as "external" and can filter this office
versus other offices, alongside active, completed, and failed jobs.

Import a handoff explicitly with `omo export import <file>`. The job is queued
without auto-running on pull, retains its checkpoint, clears the old assignee,
and notifies the CEO.

## Other exports

- `omo export statistics [--output <file>]` writes aggregate row counts, job
  states, and role counts without project paths, titles, goals, or mail bodies.
- `omo export db <table|all> [--output <file>]` writes one safe-listed SQLite
  table, or every safe-listed table, as JSON.
