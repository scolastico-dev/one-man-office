# Upgrading from 0.11

omo 0.11 was a terminal program: one "office" per folder with its
configuration in `.omo/omo.yaml`, a TUI, and a company dashboard for several
offices. This version replaces it with one server per user and one board in
the browser for all your repositories.

Nothing of 0.11 is carried over: offices, jobs, mail, model profiles, prompts,
plugins and settings stay behind, and you set omo up once more, which takes a
few minutes (see [Getting started](getting-started.md)). Your repositories and
their branches are not touched.

Both versions use the same home folder by default, `~/.local/omo`
(`%APPDATA%\omo` on Windows), so the first start of this omo meets the old
installation. It does not guess: it tells you what it found and asks.

## Before you start

Stop the old omo first: quit every office's TUI and stop the company
dashboard. Choice 1 below is refused while any of them runs. Then install this
version (the installer in the [README](../README.md); see
[Getting started](getting-started.md#install)) and run `omo` in a terminal.

## What omo asks

Plain `omo` and `omo -d`, started in a terminal, list what the old omo left:

- its home folder (`~/.local/omo`) with what is in it,
- the `.omo` folder of every office you ever trusted, with the agents'
  worktrees in it; a worktree with uncommitted changes is marked
  `UNCOMMITTED CHANGES` (`CANNOT BE CHECKED` counts the same),
- the login autostart entry of the old company dashboard,
- trusted offices whose `.omo` folder is already gone ("not found").

Then it asks:

```text
  1  Continue: delete everything listed above (git branches stay) and start
     this omo with a fresh home.
  2  Go back: install omo 0.11.0 again and leave everything as it is.
  3  Quit: change nothing.
Choose 1, 2 or 3 [3]:
```

- **1 Continue** deletes everything listed: the worktrees (through git, so the
  repositories forget them; their branches stay), each office's `.omo` folder,
  the dashboard's autostart entry and everything in the home folder. It asks
  `Delete everything listed above? [y/N]`. When a worktree holds uncommitted
  work, it says so and you must type `delete` instead. Then omo starts with an
  empty home.
- **2 Go back** installs omo 0.11.0 over this binary and changes nothing else.
  In the listed offices it switches the update check at start off
  (`startup.check_self_update: false`), so they stop offering this version.
  Other offices still offer it: answer no there. When you want this version
  later, run 0.11's `omo self-update`.
- **3 Quit**, also what a bare Enter does, changes nothing and exits.

Offices you never trusted are not in the old list, so omo does not find them:
their `.omo` folders stay. Delete them by hand, together with the `.omo/`
line 0.11 added to each repository's `.git/info/exclude`.

If the old omo still runs, choice 1 names what runs ("the company dashboard
of …", "the office …") and changes nothing: stop it, then start omo again.

## Without a terminal

Started without a terminal (a login autostart entry of 0.11, a service,
Docker without `-it`), omo prints what it found, changes nothing and exits.
Every other command that opens the home (`omo config`, `omo stop`,
`omo autostart`, ...) stops with:

```text
omo: /home/you/.local/omo holds the files of omo 0.11 or older. Start omo in a terminal to choose what happens to them, or set OMO_HOME to another folder
```

If the old login entry still starts `omo` at login, it fails like this every
time until you choose in a terminal (choice 1 removes the entry) or remove the
entry yourself. Setting `OMO_HOME` to another folder starts a separate, fresh
omo and leaves the old files alone.

A non-empty home that is no omo home of any version is never touched: omo
says that it is not empty and is no omo home, and asks you to empty it or to
set `OMO_HOME` to an empty folder.

## Docker

Run the choice once, interactively, then start the service:

```bash
docker compose run --rm omo      # answer 1, 2 or 3 in the terminal
docker compose up -d
```

## Commands that are gone

`omo setup`, `omo company …`, `omo self-update`, `omo safe-shutdown`,
`omo repo …` and the flag `--mock` do not exist any more. A call of one (from
a script, an agent of an old office, an old login entry, or the old omo
restarting itself after it installed this version) ends with the unknown
command or flag and, while the home is still the old one, the hint to start
omo in a terminal. Updating is `omo update`. After `omo update`, restart a
running omo: `omo stop`, then `omo`.

A database written by a newer omo than the one you run is refused and left
unchanged ("the database was made by a newer omo … update omo to use it"):
run `omo update`.

## Where your 0.11 settings went

| In 0.11 | Now |
|---|---|
| An office per folder, `.omo/omo.yaml` | One board for every repository; settings in the home folder, edited on the settings pages |
| `repos:` of an office, `omo repo add` | **Settings → Projects**: workspace folders that omo searches for repositories |
| Model profiles | **Settings → Agents**: profiles and their models ([Agents](agents.md)) |
| Role assignment | **Settings → Roles** |
| Usage limits | **Settings → Usage**, with usage scripts |
| The TUI and the company dashboard | The board in the browser |
| Prompt extensions, global extensions | **Settings → Prompts** (your instructions per role) and **Settings → Skill sets** |
| Plugins | Plugins have a new format and are installed on **Settings → Plugins**; 0.11 plugins do not load ([Plugins](plugins.md)) |
| `omo company autostart` | `omo autostart` ([Start at login](running.md#start-at-login)) |

After the choice, omo starts as on a first start: follow
[Getting started](getting-started.md). Register the new omo for login with
`omo autostart register` if you want it.
