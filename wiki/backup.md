# Backup and restore

omo exports its settings, and on request its database, to one zip file, and
imports such a file again: to move to another machine, to keep a copy before
you experiment, or to go back.

## Export

**Settings → Backup → Export**, or on the command line while omo runs:

```bash
omo config export omo-backup.zip            # settings only
omo config export omo-backup.zip --with-db  # settings and the database
```

The zip (`omo-settings-<date>.zip` from the browser) holds `settings.yaml`,
`server.yaml`, the projects, agent profiles, roles, prompts, usage scripts,
skill sets, plugin settings and relayed MCP servers, with a `manifest.json`
(omo version, database schema, date, whether the database is inside).
**Include the database (tickets, comments, history)** (`--with-db`) adds a
consistent copy of `omo.db`, taken while omo runs.

The file contains your tokens and environment values: keep it private.

Not in the zip: the login token, logs, worktrees, the working folders of tasks
without a project, images, skill set checkouts and installed plugin files.
After an import, install missing plugins again on the Plugins page, and fetch
skill sets again with **Update now** on the Skill sets page.

## Import

**Settings → Backup → Import settings…**, or:

```bash
omo config import omo-backup.zip          # check it and show what it would replace
omo config import omo-backup.zip --yes    # apply it
```

omo checks the whole zip before anything changes: a manifest of an omo that is
not newer than this one, only the files listed above, no links or paths
leaving the home, size limits, and every file loading as a save on its page
would. The dialog (or the command without `--yes`) lists what is replaced and
what is added. On **Import** (`--yes`) all files are written at once; the
replaced ones are kept in `OMO_HOME/backups/<time>-import/`. Settings the zip
does not hold stay as they are.

- **Also apply server settings** (`--server`) writes `server.yaml` too: listen
  address, sign-in and public address. They are left out otherwise, because
  they belong to one machine, and apply at the next start.
- **Also replace the database** (`--with-db`) replaces all tickets, comments
  and their history with the zip's. It is refused while agents run (the
  dialog offers **Stop all agents**). omo pauses, and the database is put in
  place when omo starts again, so restart omo afterwards. The database it
  replaces is kept in `OMO_HOME/backups/<time>-database/`.

## Copying the home by hand

With omo stopped (`omo stop`), a copy of the whole home folder (see
[Where state lives](home-folder.md)) is a complete backup, including
worktrees, logs and images. Do not copy `omo.db` while omo runs: use an export
with the database instead.

The worktrees in the home belong to the git repositories of your projects; a
home restored on another machine needs the projects at the same paths.
