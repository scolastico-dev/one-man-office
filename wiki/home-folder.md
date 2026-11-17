# Where state lives

Everything omo keeps is in one folder, the omo home:

- `~/.local/omo` on Linux and macOS,
- `%APPDATA%\omo` on Windows,
- or the absolute path in `OMO_HOME`, which overrides both.

omo writes nothing into your projects: no `.omo` folder, no change to
`.git/info/exclude`, no change to your git config. The agents' worktrees are in
the home too.

Every setting is a plain file. The settings pages edit these files, and a hand
edit shows up on an open page within a few seconds. A file with a mistake is
reported on its page, and omo keeps using the last valid content until you fix
it.

## Layout

| Path | What it holds | In a settings export |
|---|---|---|
| `layout.json` | marks the folder as a home of this omo | no |
| `server.yaml` | start options ([Running omo](running.md#start-options)) | yes, applied only on request |
| `token` | the login token | no |
| `settings.yaml` | general settings: workspace folders, limits, review and merge defaults, the agents' environment, branch names, usage limits, the CEO, plugin lists, interface choices | yes |
| `omo.db` | tickets, comments, history, agent sessions, plugin storage, MCP tokens and sign-ins | only with the database option |
| `projects/<id>.yaml` | one file per project: folder, name, switch, merge modes | yes |
| `profiles/<name>.yaml` | agent profiles: program, arguments, account folder, environment, models, usage script | yes |
| `roles/<role>.yaml` | which models a role uses and how it picks | yes |
| `prompts/<name>.md` | your instructions per role and changed prompts | yes |
| `usage/<name>.lua` | usage scripts you changed or added | yes |
| `skillsets/<name>.yaml` | skill sets | yes |
| `skills/<name>/` | the skill sets' checkouts | no (fetched again) |
| `plugins/<name>.yaml` | a plugin's source, switch and configuration | yes |
| `plugins/<name>/` | the installed plugin's files and data | no (install again) |
| `mcpservers/<name>.yaml` | the MCP servers omo relays | yes |
| `worktrees/<project>/<ticket>/` | the agents' git worktrees | no |
| `workdirs/<ticket>/` | working folders of tasks without a project (kept until you remove them) | no |
| `ceo/` | the CEO's working folder | no |
| `logs/` | agent transcripts | no |
| `images/` | images uploaded to tickets; `images/terminal/` holds images dropped into terminals | no |
| `run/` | the running server's lock, socket, log (`omo.log`), last usage readings, export folders, autostart settings | no |
| `backups/` | files a settings import replaced, and a database an import replaced | no |

The home and `run/` are readable by your user only.

## Several homes

Each home is one independent omo: its own server, port, board and settings.
To try a build next to the omo you use, give it its own home and port:

```bash
export OMO_HOME=$(mktemp -d)       # this shell only
omo config set port 18123          # never the port of your other omo
omo
```

`omo stop`, `omo config` and `omo ai` in that shell then act on that omo only.

A home that holds files but no `layout.json` (for example the home of omo
0.11) is refused; see [Upgrading from 0.11](upgrading-from-0.11.md).
