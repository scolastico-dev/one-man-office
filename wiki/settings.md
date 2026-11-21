# Settings reference

The gear in the top bar opens **Settings**. Every page edits files in the omo
home (see [Where state lives](home-folder.md)); a hand edit shows up on an open
page within a few seconds, and a page with unsaved edits offers the new value
("Changed outside this page", **Use the new value** / **Keep mine**). A save is
refused with the reason under the field. Leaving a page with unsaved edits
asks first.

Everything here applies while omo runs; profile changes apply to agents
started after the save, and limits and CEO switches at the next scheduling
pass. Only the start options (host, port, sign-in, `public_url`) need a
restart; they are not on these pages but in `omo config` (see
[Running omo](running.md#start-options)).

Durations are written like `45s`, `15m`, `1h30m`.

- [Projects](#projects)
- [Agents](#agents)
- [Roles](#roles)
- [Usage](#usage)
- [Prompts](#prompts)
- [Skill sets](#skill-sets)
- [MCP](#mcp)
- [Language servers](#language-servers)
- [Environment](#environment)
- [Limits](#limits)
- [Review](#review)
- [Plugins](#plugins)
- [General](#general)
- [Backup](#backup)
- [settings.yaml](#settingsyaml)

## Projects

*Workspace folders and the repositories found in them.* Files:
`settings.yaml` (`workspace_roots`), `projects/<id>.yaml`.

**Workspace folders**: omo searches each folder for git repositories and makes
every repository it finds an enabled project, at start, every **Rescan
interval** (Settings → Limits), when the folders change and on **Rescan**. It
does not look inside a repository, does not follow links, and skips the omo
home.

| Field | Meaning |
|---|---|
| **Folder N** (**Browse…**) | an absolute path or one starting with `~` |
| **Search depth** | how many folders deep to look, 1 to 16 (4) |
| **Ignore** | folder names (`node_modules`) or paths below the folder (`archive/*`); a new folder starts with `node_modules` and `.*` |

**Scan details** says per folder what was searched and what was cut by the
scan's time and size limits.

**Projects**: every project with its folder, branch and state. The switch
(**Use <name>**) turns a project off: it keeps its tickets but takes no new
ones. **Edit** renames it and sets its merge modes ("When a story is merged",
"When a task without a story is merged", "When a task is merged into its
story": **Use global default (…)**, **Merge commit** or **Squash into one
commit**; see [Merging](merging-and-export.md#merge-commit-or-squash)).
**Select…** acts on several projects at once (**Switch on**, **Switch off**,
**Remove…**). **Add project** adds one repository by hand. **Delete all missing
projects…** forgets projects whose folder is gone.

A project found in a workspace folder comes back after a removal: switch it
off, or add an ignore pattern. A project that open tickets use cannot be
removed. The branch column shows the branch the folder has checked out, which
new tickets take as their base.

## Agents

*Agent programs, their accounts and models.* Files: `profiles/<name>.yaml`.
**New profile…**, the profile editor, **Detect agent programs** and **Add
missing models** are described in [Agents](agents.md#profiles-and-models).

## Roles

*Which models each role works with.* Files: `roles/<role>.yaml` for `ceo`,
`pm`, `developer` and `reviewer`. Per role: **Models of the <role>** (an
ordered list; **Add model**) and **Assignment** (Round robin, Random,
Failover, Smart, Super smart). See [Roles and assignment](agents.md#roles-and-assignment).

## Usage

*Usage scripts and the limits that keep agents off busy models.* **Limits**
(`settings.yaml` `usage`: **Soft limit, %** 90, **Hard limit, %** 98, **Refresh
every** 5m, **Limits per window**) and **Scripts** (`usage/<name>.lua`). See
[Usage](agents.md#usage).

## Prompts

*Your instructions and the prompts agents get.* Files: `prompts/<name>.md`; a
missing file is omo's default.

Per role (CEO, Product manager, Developer, Reviewer, Export):

- **Your instructions**: text inserted into the role's prompt as it is, for
  example your coding conventions or how to run the tests. This is the usual
  place to tell agents about your repositories.
- **omo's prompt for this role**: what omo tells the agent when it starts, a
  Go template over the ticket, the projects and the settings. Changing it is
  possible (**Preview**, **Show default**, **Reset to omo's default…**); the
  server checks it when you save.

**Messages**: the short texts omo types into running agents: Start, Restart,
Handoff, Wait timeout, Wait investigate, Stalled, Question reminder.

Agents run as you and can read these files: nothing written here is a secret
from them.

## Skill sets

*Skill repositories agents are pointed at.* Files: `skillsets/<name>.yaml`. See
[Skill sets](skills.md).

## MCP

*Servers omo relays to agents, the agent programs it is registered in and
access for other programs.* Files: `mcpservers/<name>.yaml`, `settings.yaml`
`mcp`. See [MCP](mcp.md).

## Language servers

Open **Settings → LSP** to register and configure language servers. The page
edits the `lsp` group in `settings.yaml`, also available through the user-only
`GET`, `PUT` and `PATCH /api/settings` routes. Starting servers, watching files
and sending notifications are separate work. No language server is installed
automatically.

**Add language server** adds an initially disabled registration. Set its unique
name, **Command**, literal **Arguments** (one per row), **Environment overrides**
and **File selection** (pattern and language ID). **Move up** and **Move down**
change selector priority; the first matching selector wins. **Initialization
options (JSON)** and **Server configuration (JSON)** accept JSON objects,
including nested nulls. Invalid JSON is marked and blocks Save. **Remove language
server** removes a registration from the draft. Registrations run in managed
worktrees, with the worktree root as their working directory.

Under **Reporting issues**, set **Debounce** (`500ms` by default, `0s` to disable
the delay, at most `1m`). **Force chat by agent program** explicitly selects chat
delivery for Claude Code, Codex, Antigravity, Gemini or Custom agents. The fallback
sends a chat message even when the agent is listening for MCP notifications. A
program left off uses MCP when the agent is listening, with chat as the fallback
otherwise.

Changes take effect only after **Save**; **Discard** restores stored values.
The page asks before leaving with unsaved edits, places server field errors
beside their fields and reports hand edits. A conflicting edit offers **Use the
new value** or **Keep mine** before another save.

The default for existing and new installations is:

```yaml
lsp:
  servers: []
  debounce: 500ms
  force_chat: []
```

An example registration (the executable must already be available):

```yaml
lsp:
  debounce: 500ms
  force_chat: [codex]
  servers:
    - name: go
      enabled: true
      command: gopls
      args: [serve]
      env: {}
      files:
        - pattern: '*.go'
          language_id: go
        - pattern: go.mod
          language_id: go.mod
      initialization_options: {}
      configuration:
        gopls:
          staticcheck: true
```

| Field | Contract |
|---|---|
| `servers` | Ordered list, at most 32 registrations; omitted means `[]`. |
| `servers[].name` | Required unique name matching `[a-z0-9][a-z0-9._-]{0,63}`. |
| `servers[].enabled` | `false` unless explicitly enabled. Disabled entries still need valid configuration. |
| `servers[].command` | Required executable name or path, at most 4096 bytes, without hidden characters. A bare name uses `PATH`; this is not a shell command. The working directory is always the managed worktree root; there is no `cwd` field. |
| `servers[].args` | Literal argv, default `[]`; at most 100 arguments of 4096 bytes each, no NUL. No shell expansion. |
| `servers[].env` | Literal environment overrides, default `{}`; at most 200 variables, each value at most 32 KiB, no NUL. No variable or backtick expansion. Names follow the ordinary environment setting rules. |
| `servers[].files` | Required list of 1–100 selectors, each with `pattern` and `language_id`; see below. |
| `servers[].initialization_options` | JSON object for the LSP `initialize` request's `initializationOptions`, default `{}`. |
| `servers[].configuration` | JSON object of server settings for LSP configuration requests and changes, default `{}`. |
| `debounce` | Quiet interval before reporting changed diagnostics, `500ms` by default; `0s` disables the delay, maximum `1m`. |
| `force_chat` | Unique agent program (`Profile.CLI`) values from `claude`, `codex`, `agy`, `gemini`, `custom`; default `[]`. For a listed program, chat delivery takes precedence even when its agent has an active MCP notification listener. |

Each opaque JSON object may contain text, finite numbers, booleans, null,
lists and objects with text keys, nested at most 64 levels, and encode to at
most 64 KiB of JSON. Quote YAML dates/times to keep them strings. Commands,
arguments and environment values must be valid UTF-8. Values are stored and
returned as supplied, and never included in refusal messages or change events.

File selection is case-sensitive on every OS. Selectors are tried in listed
order; the first match supplies the file's `language_id` for that server. No
match excludes the file from that server; several servers may select the same
file. Language IDs are explicit, not inferred from extensions: nonempty text of
at most 64 bytes without whitespace or hidden characters.

Patterns use Go `path.Match` glob syntax: `*`, `?`, and character classes such
as `[ch]`, each matching within one path component. A pattern without `/`
matches the basename at any depth (`*.go` includes `main.go` and
`internal/app/main.go`). A pattern with `/` matches the complete path relative
to the worktree root, using `/` on every OS (`src/*.go` includes `src/main.go`
but excludes `src/nested/main.go`). Dot-prefixed names are treated like other
names. Patterns are at most 1024 bytes; `**`, backslashes, colons, absolute
paths and empty, `.` or `..` components are refused. Quote wildcard patterns
in YAML.

The group uses the existing strict settings store, revisions (`If-Match`),
hand-edit notifications, last-valid-state handling and backup/import flow.
`PATCH {"lsp":{"debounce":"1s"}}` keeps registrations and delivery overrides.
Patching `servers` or `force_chat` replaces that whole list; server objects
inside a new list are complete replacements. Opaque options/configuration
objects allow nested null values; null settings fields are refused on PATCH.
The complete encoded `settings.yaml` must also fit the store's 1 MiB file
limit. A save that would exceed it is refused before changing the file or
active settings, even if each registration meets its own limits.

## Environment

*Variables every agent starts with.* `settings.yaml` `agent_env`.

`$VAR` and `${VAR:-fallback}` take values from omo's environment; a
`` `command` `` runs when an agent starts and its output becomes the value, so
`` `pass show anthropic` `` keeps a key in your password manager. A value
written out is stored as typed in `settings.yaml`. Profile and model
environments override these. omo's own git uses them for merge commits.

**Check (runs your commands)** runs every command on this machine and says per
variable whether it ends up set, empty or failed, without the values.
**Restore shipped defaults…** puts back omo's defaults:

| Variable | Default |
|---|---|
| `GIT_AUTHOR_NAME` | `OMO - AI Orchestrator` |
| `GIT_AUTHOR_EMAIL` | `omo@scolasti.co` |
| `GIT_COMMITTER_NAME` | your `git config user.name`, unless set |
| `GIT_COMMITTER_EMAIL` | your `git config user.email`, unless set |
| `GIT_CONFIG_PARAMETERS` | `'commit.gpgSign=false'` and what was set before |

So agents' commits name omo as author and you as committer, and are not
signed.

## Limits

*Tasks and stories in work, health checks and time limits.* `settings.yaml`.

| Field | Key | Default | Meaning |
|---|---|---|---|
| **Tasks in work** | `limits.tasks` | 4 | 1 to 64; tasks inside a story count too ([Limits](concepts.md#limits)) |
| **Stories in work** | `limits.stories` | 2 | 1 to 64 |
| **Silent after** | `health.silent_after` | 15m | an agent without output this long is reminded; after the same time again it is flagged |
| **Question unanswered after** | `health.question_unanswered_after` | 30m | a question this old is flagged (a question to an agent is first reminded, and flagged after twice the time) |
| **Timeouts before investigating** | `waiting.timeouts_before_investigate` | 3 | after this many `omo ai wait` timeouts in a row the waiting agent is told to find out why nothing happens |
| **Handoff time limit at stop** | `stop.handoff_timeout` | 2m | how long `omo stop` (and a usage stop) waits for handoff comments; 5s to 1h |
| **Rescan interval** | `rescan_interval` | 10m | how often workspace folders are searched |
| **Export check interval** | `export_check_interval` | 10m | how often remote target branches are checked for exported work; `0` disables automatic checks, otherwise at least `1m`; **Check now** remains available on a waiting ticket |
| **Keep images after Done (days)** | `images.keep_days_after_done` | 60 | 0 keeps images for ever |

## Review

*Review and release defaults for new tickets.* For three kinds of ticket:
**Stories**, **Tasks without a story** and **Tasks in a story**, each with
**Review** (Skipped, Agent, Human, Agent, then human) and **When approved**
(Merge locally, Export as a pull request; tasks in a story always merge). A
ticket's own value overrides these.

| Kind | Review | When approved |
|---|---|---|
| Stories | Agent, then human | Merge locally |
| Tasks without a story | Agent, then human | Merge locally |
| Tasks in a story | Agent | Merge locally (fixed) |

**Tasks are released by**: **The product manager or the CEO** (default) or
**You** (`tasks.require_user_release`). See
[Approval and release](concepts.md#approval-and-release).

## Plugins

*Plugins that react to the board.* Files: `plugins/<name>.yaml` and
`plugins/<name>/`, `settings.yaml` `plugin_lists`. See [Plugins](plugins.md).

## General

*The CEO, merge defaults, this server and the theme.*

- **CEO**: **The CEO supervises the board** (on), **The CEO may approve
  stories** (off), **The CEO's tickets wait for your approval** (on). See
  [The CEO](concepts.md#the-ceo).
- **Tickets**: **Open tickets on their own page** (off).
- **Accessibility**: **Screen reader mode for terminals** (off): screen
  readers get a text copy of terminal output.
- **Branches**: **Branch name template** (`omo/{slug}`); see
  [Branch names](merging-and-export.md#branch-names).
- **Merge defaults**: the three merge modes (all **Merge commit**).
- **This server**: version, the address omo advertises (`public_url`) and the
  home folder. Host, port and sign-in are changed with `omo config`.
- **Theme**: **Colours** System, Dark or Light, remembered in this browser.

Web UI forward subdomains use the server start options `proxy_base_host`,
`proxy_label_prefix` and `proxy_label_suffix`, changed with `omo config` and
applied on restart. They are separate from `settings.yaml` and the General
page. See [Web UI forwards](proxy.md#optional-subdomains) for DNS, accepted
patterns and [HTTPS deployment](proxy.md#behind-an-https-reverse-proxy).

## Backup

*Export the settings to a zip file and import them again.* See
[Backup and restore](backup.md).

## settings.yaml

The general settings file, for reference. A key that is missing takes its
default; a file that does not load is reported and the last valid settings
stay in use.

| Key | Default |
|---|---|
| `limits` | `tasks: 4`, `stories: 2` |
| `ceo` | `enabled: true`, `may_approve_stories: false`, `tickets_need_user_approval: true` |
| `tasks` | `require_user_release: false` |
| `review_defaults` | `story: agent_then_human`, `task: agent_then_human`, `story_task: agent` |
| `when_approved_defaults` | `story: merge`, `task: merge`, `story_task: merge` |
| `merge_defaults` | `story: merge`, `task: merge`, `story_task: merge` |
| `waiting` | `timeouts_before_investigate: 3` |
| `health` | `silent_after: 15m`, `question_unanswered_after: 30m` |
| `images` | `keep_days_after_done: 60` |
| `stop` | `handoff_timeout: 2m` |
| `workspace_roots` | `[]` (each: `path`, `max_depth`, `ignore`) |
| `rescan_interval` | `10m` |
| `agent_env` | the five `GIT_*` variables above |
| `usage` | `soft_percent: 90`, `hard_percent: 98`, `refresh_interval: 5m`, `windows: {}` |
| `ui` | `open_tickets_as_page: false`, `screen_reader_mode: false` |
| `branches` | `template: omo/{slug}` |
| `plugin_lists` | `[]` |
| `mcp` | `enabled: true`, `inject: {claude: true, codex: true}` |
| `lsp` | `servers: []`, `debounce: 500ms`, `force_chat: []` |
