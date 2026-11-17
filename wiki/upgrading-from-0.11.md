# Upgrading from 0.11

omo 0.11 was a terminal program: one "office" per folder with its
configuration in `.omo/omo.yaml`, a TUI, and a company dashboard for several
offices. This version replaces it with one server per user and one board in
the browser for all your repositories. Nothing of 0.11 is converted: you set
omo up once more, which takes a few minutes (see
[Getting started](getting-started.md)).

## What happens if you just start it

Both versions use the same home folder by default, `~/.local/omo`. The new omo
refuses a home that 0.11 wrote and stops with:

```text
omo: omo: /home/you/.local/omo holds an installation of an older omo. This version cannot use it yet; set OMO_HOME to another folder.
```

It changes nothing in that folder.

## Steps

1. With 0.11 still installed, stop it: quit every office's TUI (or
   `omo safe-shutdown` in the office folder), then stop the company and remove
   its login entry:

   ```bash
   omo company stop
   omo company autostart unregister
   ```

2. Move the 0.11 home out of the way, so the new omo can use the default
   place:

   ```bash
   mv ~/.local/omo ~/.local/omo-0.11
   ```

   On Windows the folder is `%APPDATA%\omo`. If you would rather keep it where
   it is, set `OMO_HOME` to another absolute path for the new omo instead, in
   every shell and in the autostart (see [Where state lives](home-folder.md)).

3. Install the new version with the installer from the
   [README](../README.md) or [Getting started](getting-started.md#install). It replaces the `omo` binary in
   the same place.

4. Start it with `omo -d` and open the printed address. Follow
   [Getting started](getting-started.md): add the folder that holds your
   repositories, check the agents and roles, create a first ticket.

5. If 0.11 started at login, register the new omo:
   `omo autostart register` (see [Start at login](running.md#start-at-login)).

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
| `omo company autostart` | `omo autostart` |

Jobs, mail, incidents and the history of 0.11 offices are not carried over.

## Cleaning up

The new omo never reads or writes a project's `.omo/` folder, and it never
writes `.git/info/exclude`. Once you no longer need 0.11's state, delete
`~/.local/omo-0.11`, the `.omo/` folders of your offices, and the `.omo/` line
0.11 added to each repository's `.git/info/exclude`.
