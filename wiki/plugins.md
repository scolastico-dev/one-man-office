# Plugins

A plugin reacts to what happens on the board: a ticket that needs you, a
question, an agent that starts or ends, a timer, or a button you press. omo
lists plugins on **Settings → Plugins** and installs them from a git source.

Plugins run as you, with your rights. Install only plugins whose code you
trust; the tags below say who looked at it, nothing more.

- [Installing a plugin](#installing-a-plugin)
- [The plugins omo ships](#the-plugins-omo-ships)
- [Additional plugin lists](#additional-plugin-lists)
- [Writing a plugin](#writing-a-plugin)

## Installing a plugin

**Settings → Plugins** has three parts:

- **Installed**: every installed plugin with its state, a **Runs its hooks**
  switch, its source and version, its configuration, its log and its global
  buttons. **Update** fetches the plugin again from the same source.
  **Install plugin…** installs from a source you type.
- **Available plugins**: omo's plugin list, with an **Install** button per
  plugin.
- **Additional plugin lists**: lists other people publish (see
  [below](#additional-plugin-lists)).

Each listed plugin has a tag:

| Tag | Meaning | What omo does on Install |
|---|---|---|
| `official` | Kept in omo's repository by its maintainers and released with omo. | Installs at once. |
| `recommended` | Someone else's plugin that the maintainers looked at, at the listed version. | Warns that it is someone else's code and asks first. |
| `community` | Listed, not reviewed. | Warns more strongly and asks first. |

A source you type under **Install plugin…** always counts as `community`, even
when omo's list names it. omo decides the tag on the server: changing a
plugin's source, folder or branch, or updating it from a source other than the
installed one, asks again.

The **Install plugin…** dialog takes:

- **Source**: an `https://` or `ssh://` address, a `host:path` address, or an
  absolute path to a folder on this computer. `http://`, `git://` and
  `file://` are not accepted.
- **Folder in the repository**: the plugin's folder; empty for the top.
- **Branch or tag**: empty for the default branch. Pin a tag or commit you
  read: a plugin that follows a branch changes when its author pushes.
- **Name**: the plugin's name in omo.

omo fetches the source into `OMO_HOME/plugins/<name>/` and checks the plugin
before it replaces an earlier version; a failed update keeps the earlier one.
Nothing is fetched when omo starts. The plugin's switch and configuration are
stored in `OMO_HOME/plugins/<name>.yaml`.

Where the list comes from: omo reads `plugins/index.yaml` from the `release`
branch of its repository when the page asks, and keeps it for ten minutes. When
it cannot be read, the page says "omo's plugin list could not be loaded" with
**Retry**; installed plugins keep working. The variable
`OMO_PLUGIN_CATALOG=<source>[#<ref>]`, set when omo starts, makes it read the
list from another repository or folder (a fork, for example); the official
plugins then install from there too.

How a plugin gets onto omo's list is described in
[plugins/README.md](../plugins/README.md).

### A plugin's state

The list shows whether each plugin is ready, running, switched off, failed in
its last run ("The last run failed"), does not load ("It does not load"), or is
"Not installed" (its settings are there, its files are not; **Install** fetches
it). The plugin's log keeps its last 500 lines, newest last.

## The plugins omo ships

| Plugin | What it does |
|---|---|
| [pushover](../plugins/pushover/README.md) | A Pushover notification on your phone when a ticket needs your attention or an agent asks you a question. Button: "Send a test notification". |
| [discord](../plugins/discord/README.md) | The same as a message in a Discord channel through a webhook. Button: "Send a test message". |
| [vscode](../plugins/vscode/README.md) | Opens ticket worktrees and sidebar project repositories in code-server or OpenVSCode through omo’s sign-in; downloads verified pinned builds and stops idle editors. Microsoft’s choice is visible but unavailable pending confirmation of its terms for this integration. |
| [pause-notice](../plugins/pause-notice/README.md) | Tells every running agent to halt when you switch on **Paused**, and to continue when you switch it off. Without it the pause only keeps omo from starting new agents (see [Pause and stop all agents](concepts.md#pause-and-stop-all-agents)). |

The VS Code plugin edits folders directly alongside agents, with no locking.
Its default is code-server, its idle setting defaults to 30 minutes after the
last connected browser disconnects, and its task output/Stop control lives on
its settings page. First-use progress completes in the tab reserved by that
same click; the editor has no separate sign-in. The Microsoft option would
launch a verified official **server-web** payload directly, rather than
literally invoking `code serve-web`, but is currently blocked: Microsoft's
[terms](https://code.visualstudio.com/license/server) and
[FAQ](https://code.visualstudio.com/docs/remote/faq#_can-i-use-vs-code-server-to-build-a-service)
have not established permission for this integration. A saved acceptance value
cannot unlock it; no licence is accepted or Microsoft payload downloaded/run.
The [plugin README](../plugins/vscode/README.md) lists exact pins/checksums,
platform/runtime limitations, direct-edit behavior and opt-in real probes.

Each plugin's README lists its configuration keys. Pushover and Discord send at
most 20 messages an hour by default (`max_per_hour`).

## Additional plugin lists

Under **Additional plugin lists** (or `plugin_lists` in `settings.yaml`) you
can add up to 16 addresses of list files that someone else publishes:

```yaml
plugin_lists:
  - https://raw.githubusercontent.com/someone/omo-plugins/main/index.yaml
```

- An address is `http://` or `https://` and points at a plain file (no git; a
  file in a GitHub repository works through its raw.githubusercontent.com
  address), without user name, password or `#` part.
- omo fetches each list when the Plugins page asks (at most 15 seconds, 64 KiB,
  5 redirects), keeps it ten minutes (a failure one minute) and shows its
  plugins under the list's address. A list that fails shows its error there;
  the other lists are not affected.
- Every plugin of an additional list is treated as `community`, whatever tag
  the file gives.
- A plugin whose name, or whose source, folder and ref, omo's own list or an
  earlier list already has is not shown again; the list says so in a note.

The file has the format of omo's own list, except that every entry needs its
own `source`:

```yaml
plugins:
  - name: standup-notes           # letters, digits, ".", "_" or "-"; at most 64
    tag: community                # shown as community either way
    description: Posts a stand-up summary every morning.   # one line, at most 200 characters
    source: https://github.com/someone/omo-standup-notes.git   # https://, ssh:// or host:path
    ref: v1.2.0                   # optional: a branch, tag or commit; empty is the default branch
    path: plugin                  # optional: the plugin's folder in that repository
```

An entry that breaks a rule is left out with a note naming it; the rest of the
list still shows.

## Writing a plugin

A plugin is a folder with a `plugin.json` and the files its hooks name. Put it
in a git repository, or develop it in a local folder and install that folder by
its absolute path (press **Update** after each change).

### plugin.json

```json
{
  "name": "standup-notes",
  "version": "1.0.0",
  "description": "Posts a stand-up summary every morning",
  "default_config": { "webhook_url": "" },
  "hooks": [
    { "event": "timer", "interval": "1h", "lua": "standup.lua" },
    { "event": "ticket_needs_attention", "command": ["./notify.sh"], "timeout": "45s" },
    { "event": "manual", "name": "test", "description": "Send a test message", "scope": "global", "lua": "standup.lua" }
  ]
}
```

| Field | Rule |
|---|---|
| `name` | Letters, digits, `.`, `_` or `-`, starting with a letter or digit, at most 64 characters. |
| `version`, `description` | Shown on the Plugins page; the description is at most 1,000 bytes. |
| `default_config` | A JSON object: the configuration a fresh install starts with. You edit it on the plugin's page as **Configuration (JSON)**. |
| `hooks` | At most 64. |
| `buttons` | Optional list of up to 64 button declarations; see below. |
| `frontend` | Optional `{script, assets?}` with confined public paths; see below. |
| `config_fields` | Optional list of up to 64 configuration controls; see below. |

Every hook has an `event` and exactly one of:

- `lua`: a Lua file inside the plugin's folder (at most 64 KiB, no
  precompiled Lua), run in omo's sandbox (see [Lua hooks](#lua-hooks)).
- `command`: a program and its arguments as a list (at most 64 parts), started
  without a shell in the plugin's folder.

Optional fields:

- `timeout`: the time limit of one run, `1s` to `5m`, default `30s`.
- Timer hooks: `interval` (a duration of at least `1m`), or `interval_config`
  naming the configuration key that holds it.
- Manual hooks: `name` (the button's id), `description` (its text, at most 200
  characters) and `scope`: `global` (a button on the plugin's page; the
  default) or `ticket` (a button on every ticket). Optional `hidden: true`
  suppresses automatically generated legacy ticket/settings action controls;
  it defaults to false. Explicit declared buttons may still reference a hidden
  hook, and the owning frontend may invoke it with `runHook`. This is presentation
  metadata, not an authorization boundary. `hidden` must be a boolean, is only
  valid on manual hooks, and rejects null/other types and nonmanual use (including
  `hidden: false`). Existing plugins keep their implicit controls.

The manifest is read strictly: unknown fields are refused. Fields and events
of plugins written for omo 0.11 (`requires`, `roles`, `manual_args`,
`javascript`, `files`, `prompt_render`, `cron`, `startup` and the like) are
refused with a message saying what to use instead.

### Button, frontend and configuration declarations

The manifest reader validates these additive declarations and exposes them in
`PluginInfo`. The board renders these controls for enabled, loaded plugins. The JSON
configuration editor remains available alongside primitive fields.

```json
{
  "hooks": [
    { "event": "manual", "name": "open", "description": "Open editor", "scope": "ticket", "lua": "open.lua" },
    { "event": "manual", "name": "repository", "description": "Open repository", "scope": "global", "lua": "open.lua" },
    { "event": "omo_started", "lua": "start.lua" }
  ],
  "buttons": [
    { "label": "Open editor", "place": "ticket", "action": "open", "per_worktree": true },
    { "label": "Open repository", "place": "project", "action": "repository" },
    { "label": "Editors", "place": "sidebar", "action": "repository" }
  ],
  "frontend": { "script": "ui/main.js", "assets": "ui/assets" },
  "default_config": { "editor": "code", "accept_terms": false, "idle_minutes": 30, "version": "1.0" },
  "config_fields": [
    { "key": "editor", "label": "Editor", "type": "select", "choices": [
      { "value": "code", "label": "Code" },
      { "value": "open", "label": "Open editor" }
    ] },
    { "key": "accept_terms", "label": "Accept licence terms", "type": "boolean" },
    { "key": "idle_minutes", "label": "Idle minutes", "type": "number" },
    { "key": "version", "label": "Version", "type": "string" }
  ]
}
```

All nested objects reject unknown keys and wrong types, including `null`.
Labels contain 1–200 visible characters on one line. A button's `action` must
name an existing manual hook. `ticket` requires ticket scope; `sidebar` and
`project` require global scope. Each place/action pair is unique.
`per_worktree` is optional (default false), and true is valid only at `ticket`:
it describes one button per available project worktree.

`frontend.script` is required when `frontend` is present. `frontend.assets` is
an optional directory. `ParseManifest` checks their syntax; `ReadManifest`
requires the script to be a regular file and assets to be an existing directory,
both when installing/updating in staging and when loading installed plugins.
Every component and every asset must be a real directory or regular file,
without symlinks, even links within the plugin. Public paths use `/`, at most
512 bytes, with no empty, `.` or `..` components, absolute/drive/UNC paths,
backslashes, colons, hidden/control characters, Windows device names or trailing
spaces/dots. Dot-prefixed components (including `.data`, `.git` and the install
record), `settings`, `settings.yaml`, `settings.yml`, `settings.json` and
`plugin.json` are private at every depth and cannot appear in public paths.
The script is at most 1 MiB; the script plus assets are at most 64 MiB and
10,000 entries (files and directories, including the script and assets root).
Loading records a bounded public-file allowlist with sizes and SHA-256 digests.
Serving rechecks path/link/regular-file bounds, the small manifest and only the
requested file’s recorded size and digest. It never rewalks unrelated assets
per request; changed bytes are refused until a runtime reload publishes their
new identity. Only
the declared entry and files under the asset directory are public, at
`/plugin-assets/<installed-name>/<content_id>/<declared-path>`. GET and HEAD
need no sign-in; only these bundled files are public. Configuration, the plugin
manifest, hook source outside assets, settings and data are never served there.
Disabled, removed, invalid and outdated identities return 404. Responses use
`no-store` so browser caches cannot keep disabled code available. Host/Origin
checks, MIME types and the board’s existing CSP still apply.

A configuration field names a top-level JSON key. Keys and select values use
1–64 letters, digits, `.`, `_` or `-`, starting with a letter or digit. Keys
are unique; `select` requires 1–100 choices with unique stable `value` strings
and human `label` strings. Other types are exactly `boolean`, `string` and
`number`; `choices` is refused on those types. Defaults come exclusively from
`default_config`: a declared default must have the matching JSON type, a finite
number, or a listed select value. Missing defaults are allowed and do not invent
a value (in particular, no licence acceptance is implied). Unlisted JSON keys
are kept, and the user's existing values win over defaults on install/update.
These declarations validate defaults, not arbitrary existing user configuration.
The settings page shows selects, boolean switches, text and number inputs.
Absent booleans display unchecked; choosing an editor never accepts its terms.
Non-finite numeric input, including overflow in JSON text, blocks saving
instead of becoming null. Each edit retains unknown JSON keys and uses the same Save, Discard, unsaved
leave guard and revision-conflict controls as JSON edits. Invalid JSON keeps
its text and disables primitive controls until corrected. Choice labels and
the plugin description can explain unavailable platforms; a plugin may also
show a frontend panel or return an explanatory hook message.

`GET /api/plugins` and `plugin.changed` include `buttons`, `frontend`,
`config_fields` and `content_id` in each `PluginInfo`. Empty declarations are
`[]` or `{}`, never null; non-select fields omit `choices`. `content_id` is a
64-character SHA-256 of the exact manifest bytes and the public script/assets
(paths and contents in sorted traversal order), recomputed on runtime load.
It changes with script/asset edits even when `version` and installed `commit`
stay the same. It is independent of staging/install location and user config;
it is empty for unavailable or invalid manifests. Files outside the public
frontend and manifest do not affect it. A consumer can use it to invalidate its
frontend cache after reload.

`omo_started` runs once per enabled lifecycle, after omo's HTTP API and local
socket are serving, and when a plugin is installed or enabled while omo runs.
An unrelated plugin reload or configuration save does not repeat it. Disabling
and enabling starts a new lifecycle. Startup uses a reserved delivery slot, so a
full ordinary hook queue cannot drop it. Legacy `startup` and `load` remain errors pointing
to `hooks` with `event: omo_started`; `javascript`, `files` and `company_load`
point to `frontend.script`/`frontend.assets`. The other removed names remain
refused with their replacement guidance.

### Events

A command hook gets the event as JSON on standard input,
`{"event": "<name>", "at": "<RFC 3339 time>", "host": {...}, "data": {...}}`; a Lua hook gets
the same as the global table `event`.

| Event | `data` |
|---|---|
| `ticket_created` | `ticket` |
| `ticket_moved` | `ticket`, `from`, `to`, `action` |
| `comment_written` | `ticket`, `comment` (author, body up to 4 KiB, question, to, mentions) |
| `question_asked` | the same, for questions only |
| `ticket_needs_attention` | `ticket`, `reason` |
| `agent_started`, `agent_exited` | `ticket` (null for the CEO), `agent`; for exits also `reason`: `finished`, `stopped`, `crashed`, `usage` or `omo stopped` |
| `omo_paused`, `omo_resumed` | nothing (`{}`): **Paused** was switched on (also by **Stop all agents**) or off |
| `omo_started` | `at` (readiness/enable time) |
| `timer` | `at` |
| `manual` | `action`, `ticket` (null without a ticket), `project` (selected folder context below, otherwise null) |

A `ticket` carries `number`, `type`, `title`, `column`, `story`, `projects`,
`supervisor`, `attention`, `url` (without the login token) and `folders`.
`projects` remains an array of project **identifiers**, compatible with existing
plugins. `folders` is an ordered array, one entry per project:
`{project: string, kind: "worktree", path: string, available: boolean, reason: string}`.
Available entries contain the absolute, resolved recorded worktree path and
`reason: ""`. Unavailable entries have `path: ""` and a fixed reason:
`project_unavailable` (unknown/disabled project) or `folder_unavailable` (not
started, removed, closed ticket, or failed worktree/gitfile checks). Empty
projects produce `folders: []`. Delayed events retain their historical project
IDs/order; current folders are matched by ID, with removed associations marked
unavailable. A project's repository is never substituted for
a missing ticket worktree. Paths use the same trusted records, confinement and
`.git` checks as **Open terminal**; the server never follows an agent-written
`.git` lookup. Folder availability is a snapshot; validate readiness before
launch and handle a folder disappearing afterwards.

Every event has `host: {os: string, arch: string, path_separator: string,
task_process_trees: true}`. `os` and `arch` are Go platform names, for example
`linux` / `amd64`, `darwin` / `arm64`, `windows` / `amd64`. This describes omo's
host, not the browser. It does not certify native editor/libc/OS-version
compatibility; the plugin checks its distribution's prerequisites. No event
carries a session id, terminal id or arbitrary inherited environment values. Text written by people or agents (titles, comments,
attention texts) is only ever in `data`, never in arguments, environment values
or file names.

Plugins see the events from omo's start on, never older ones. Queued event hooks
run one at a time, in order. Manual actions exclude other manual actions for
that plugin (a concurrent call receives a busy 409), but run on the request
goroutine and can overlap queued hooks, including timers and startup. Plugins
must coordinate shared state and resource cleanup accordingly.
At most 60 events of one kind a minute run a
plugin's hooks, and a full queue (256 events) drops events with a log line;
this also keeps a hook that causes its own event (a `comment_written` hook that
comments) from running for ever.

### Command hooks

A command gets omo's environment (without omo's own `OMO_*` variables) plus:

| Variable | Value |
|---|---|
| `OMO_SOCKET` | omo's local socket; `omo ai` commands run from the hook reach the server through it |
| `OMO_PLUGIN_NAME` | the plugin's name |
| `OMO_PLUGIN_EVENT` | the event's name |
| `OMO_PLUGIN_CONFIG` | the configuration as JSON |
| `OMO_PLUGIN_DATA` | a data folder of the plugin's own |

`omo ai` called from a plugin acts as you: omo cannot tell it from your own
commands. Output beyond 1 MiB is cut.

### Lua hooks

A Lua hook's file is the body of a function: `return` ends it and gives its
answer. It runs in a sandbox with the base functions (without loaders),
`string`, `table`, `math`, `coroutine`, `os.time`, `os.date` and `os.clock`; there
is no `io`, no other `os` function, no `require`, `load` or `dofile`, and no
bytecode. `print` writes to the plugin's log. The globals:

| Name | What it is |
|---|---|
| `event` | the event (see above) |
| `config` | the plugin's configuration |
| `omo.exec(program, args...)` | runs a program without a shell in the plugin's folder, with the environment of a command hook; returns its output (standard output and error, at most 1 MiB) and `""` or an error text |
| `omo.http{method, url, headers, json \| form \| body, timeout}` | an HTTP request: https to any host, http only to localhost; at most 8 per run, bodies at most 1 MiB, 10 s by default and at most 30 s, redirects only to the same host. Returns `{status, body, headers}`, or `nil` and an error text |
| `omo.artifact_start{ key, url, sha256, format, strip_components, retry }` | starts or reuses an asynchronous plugin-owned artifact install; returns a status or `nil, error`; see Artifact installs |
| `omo.artifact_status(key)` | polls this plugin's in-memory artifact status, or `nil, error` |
| `omo.runtime_info()` | `{os, arch}` using Go platform names, e.g. `linux`, `darwin`, `windows` and `amd64`, `arm64`; no external command |
| `omo.task_temp_dir()` | allocates a private short temporary directory, returning `{id, path}` or `nil, error`; pass its opaque ID to `task_start` immediately |
| `omo.task_start{ name, command, args, cwd, env, ticket, temp_dir }` | starts a named supervised process and returns its task record promptly, or `nil, error`; see Background tasks |
| `omo.task_list()` | the current plugin’s retained task records, oldest first, or `nil, error` |
| `omo.task_stop(name)` | stops the latest run under this plugin-local name and waits for cleanup; `true`, or `nil, error` |
| `omo.sleep(seconds)` | waits at most 10 s, within the run's time limit |
| `omo.store_get(key)`, `omo.store_set(key, value)`, `omo.store_delete(key)`, `omo.store_keys()` | the plugin's own storage in omo's database: keys of 1 to 200 bytes, values up to 64 KiB as JSON, at most 1,000 keys |
| `omo.data_dir()` | the plugin's data folder |
| `omo.agents()` | the agents running now, the CEO included: a list of tables with `terminal` (the id of its web terminal), `name`, `role`, `model`, `purpose`, `project` and `ticket` (`nil` for the CEO) |
| `omo.send_to_agent(terminal, text)` | hands that agent a message the way omo's own notifications do: one line with `omo: ` in front (line breaks become spaces, at most 1,000 characters), submitted once nobody types in that terminal. Returns `true`, or `nil` and why |
| `omo.json_decode`, `omo.json_encode`, `omo.sha256_hex`, `omo.nfc`, `omo.time_parse`, `omo.now`, `omo.log` | helpers |

A run ends at the hook's time limit, and memory is watched. Every configuration
string of six characters or more is replaced by `[redacted]` in the log, in
error texts and in answer messages, and so are the header values the hook sent with
`omo.http`.

### Artifact installs

Lua plugins can install standalone tools without a host Node, Python, shell,
curl, tar or zip program. The plugin chooses every tool URL, version, platform,
checksum, executable path, argument and licence policy. `omo.http` retains its
existing request/redirect/body limits; artifact downloads use a separate Go
helper that outlives the short hook.

```lua
local job, err = omo.artifact_start{
  key = "worker", url = config.pinned_url, sha256 = config.pinned_sha256,
  format = "tar.gz", strip_components = 1
}
if not job then return {message = err} end
-- Return now, then poll omo.artifact_status("worker") from later hooks.
-- Only a ready job has path; use job.path .. "/bin/worker" with task_start.
return {message = "Installing: " .. job.phase}
```

`artifact_start` accepts this strict options table:

| Field | Meaning |
|---|---|
| `key` | required plugin-local name: 1–64 ASCII letters/digits/dots/underscores/hyphens, starting alphanumeric |
| `url` | required HTTPS URL, at most 8192 bytes; no user information or fragment |
| `sha256` | optional expected 64-hex SHA-256 (case insensitive); omitted/empty explicitly means no publisher checksum |
| `format` | required `tar.gz` or `zip`, on every OS |
| `strip_components` | optional integer 0–32, default 0; removes leading archive path components after path validation |
| `retry` | optional boolean, default false; explicitly retries a failed/cancelled job with the same identity |

Unknown fields and wrong types fail with `nil, error`. No destination, headers,
other plugin name or general filesystem operation is accepted. Keys containing
known configuration or URL query secrets are refused. Every call is bound to
the executing plugin; another plugin cannot inspect its jobs.

Both helpers return a snapshot with these fields:

| Field | Meaning |
|---|---|
| `id`, `key` | opaque job identity and plugin-local key |
| `state` | `running`, `ready`, `failed` or `cancelled` |
| `phase` | while running: `checking`, `downloading`, `verifying` or `extracting`; otherwise the terminal state |
| `bytes` | compressed bytes received so far, zero before downloading and for cache-only checks |
| `total_bytes` | advertised compressed length, or zero when unknown |
| `verification` | `sha256` when an expected digest was supplied; `unverified` otherwise (describes the requested check while running; only ready means it completed) |
| `path` | present only when ready: absolute extracted tool folder |
| `error` | present on failure/cancellation: bounded fixed diagnostic without URL, response body, headers or filesystem paths |

Simultaneous calls with the same plugin/key and URL/digest/extraction options
reuse the active job and its id. Different options under an active key return
an identity-conflict error. Once terminal, changed options start a fresh job;
the former valid installation stays intact. Failed/cancelled jobs keep their
id/error until an explicit `retry=true` or changed identity. There is no retry
loop. At most four jobs and 64 retained keys belong to one plugin. Removal
forgets its in-memory records; disable and server stop retain terminal status
until the manager ends. No artifact polling endpoint or persisted job queue is
added: a frontend polls through a normal hook.

Calling `artifact_start` on a ready key also returns promptly in
`running/checking`, without a path, while the full cache is checked. Poll
`artifact_status` until it is ready. Polling reports the most recent result;
it does not itself rehash the tree. Request a new `artifact_start` and await
ready before reusing an installed executable. New servers have no job status;
an explicit start can discover and validate a completed disk cache.

Every install has a 30-minute overall deadline, including cache checks, download
and extraction. Downloads allow five redirects, including cross-host HTTPS
CDNs, and refuse an HTTP downgrade or credentials in redirects. No cookies or
Authorization headers are forwarded. The default transport bounds response
headers to 64 KiB and waiting for headers to 30 seconds. Compressed bytes are
at most **512 MiB**, extracted file bytes **2 GiB**, one regular file **512 MiB**,
and archive/tree entries **50,000**, including implicit directories (archive
headers are counted too). Paths are at most 4096 bytes, components 255 bytes.
ZIP central-directory metadata is preflighted before entry allocations: at most
32 MiB, with actual counts checked independently of the advertised count;
bounded single-disk ZIP64 directories are supported.
These bounds admit the researched standalone editor pins. Expanded metadata and
trailing tar data are also bounded. A supplied digest is checked over downloaded
bytes **before extraction**; mismatch fails closed. Without a publisher checksum,
ready remains explicitly `unverified`, even though local integrity is recorded.

Downloads/extraction use private staging under
`plugins/.data/<plugin>/.artifacts-v1/<key>/`. Completed generations contain
`tree/` and `complete.json`; an identity-hashed reference selects a completed
generation. Metadata stores identity and full-tree hashes, never source URLs.
The tree and completion publish together by rename on the same filesystem,
then the reference updates atomically. Cache checks require matching completion
and reference metadata and rehash **every** file's bytes, paths, file types,
permissions and link targets. Extra, missing or altered files/executables and
incomplete metadata force a fresh download before ready. Metadata is local
integrity bookkeeping, not a signature or a security boundary against someone
who can rewrite both data and metadata as the user.

Extraction rejects absolute/drive/UNC paths, `..`, percent-encoded path ambiguity,
Windows device/ADS names, case aliases, link traversal during writes, devices,
FIFOs, dangling/cyclic links and targets outside the extracted root. Files use
private 0600 or executable 0700 modes; directories use 0700, with no archive
ownership/setuid/setgid/sticky bits imported. Contained **file** symlinks and
hardlinks are resolved only after regular files exist. Unix preserves relative
symlinks (including Node realpath semantics); Windows copies their target bytes
and executable mode without requiring symlink privileges. That fallback can
change Node's realpath/relative-import behavior, so plugins must launch a
verified real entry point rather than assume a copied `.bin` shim has identical
semantics. Directory links are refused. Tests cover the twelve contained
`node_modules/.bin` file links observed in the code-server archive; other
platform archive layouts still need native validation by the plugin.

Failure/cancellation removes only its private staging and never replaces an
existing valid generation or deletes unrelated plugin data. Completed/obsolete
generations remain on disk for explicit user maintenance; disk use can therefore
grow with explicit version/identity changes. Installs are cancelled and reaped
on disable, removal, code/install replacement and shutdown; ordinary
configuration refresh preserves them. Ending a hook does not cancel its job.
Nothing resumes automatically after restart. Artifact completion authorizes no
licence acceptance or process start: the plugin handles those before invoking
`task_start` with the ready path.

### Background tasks

A task runs independently of the hook that started it. The hook can return or
reach its time limit while the process continues. omo never restarts a task or
persists its process state. A new omo server starts with an empty task list;
startup hooks may explicitly start fresh tasks.

For runtime sockets or temporary files, allocate an owned directory immediately
before starting the task:

```lua
local temporary, err = omo.task_temp_dir()
if not temporary then return {message = err} end
local task, err = omo.task_start{
  name = "worker", command = "my-worker",
  args = {"--runtime-dir", temporary.path}, temp_dir = temporary.id
}
if not task then return {message = err} end
```

`task_temp_dir()` returns exactly `{id: string, path: string}`. Its opaque ID
belongs to the allocating plugin runtime and is single-use. `temp_dir` on
`task_start` accepts that ID, never a filesystem path. The start consumes it
before validating or spawning the process; a failed start releases it. A valid
start binds cleanup to that stable task ID, independently of reusable names or
hook cancellation. Cleanup follows process-tree reaping on natural exit, Stop,
ticket lifecycle, idle/watched-forward removal, disable/remove/replacement and
server shutdown. Configuration-only refresh preserves running tasks and their
directories. Normal task output and API records are unchanged.

Allocations are mode 0700 on Unix and use short `/tmp/omo-*` paths, independent
of inherited `TMPDIR`; Windows uses its OS temporary directory. At most 16
unattached allocations per runtime are allowed, and unused allocations expire
after 60 seconds or when that runtime ends. Allocate immediately before
`task_start`, rather than before a download or other long work. Only directories
created by this helper can be removed; it exposes no arbitrary-path cleanup.
Removal retries transient failures for up to one second; a persistent operating
system deletion error is recorded in omo's server log.
Persistent state belongs in `omo.data_dir()`.

```lua
local task, err = omo.task_start{
  name = "worker",
  command = "my-worker",
  args = {"--listen", "127.0.0.1:9090"},
  cwd = omo.data_dir(),
  env = {WORKER_MODE = "local"},
  ticket = event.data.ticket and event.data.ticket.number or nil
}
if not task then return {message = err} end
return {message = "Worker started"}
```

`name` and `command` are required strings. Names are 1–64 ASCII letters,
digits, dots, underscores or hyphens, starting alphanumeric. They are unique
among running tasks of a plugin, including simultaneous starts. The same name
can be used by another plugin, and reused after a process ends; each run has a
fresh opaque `id`. Names containing redacted configuration/environment values
are refused. `command` is an executable, with no implicit shell. `args` is an
optional array of at most 64 strings (each argument and the command at most
4096 bytes, no NUL). `cwd` defaults to the plugin folder; relative folders
resolve there, absolute folders are allowed (at most 4096 bytes, no NUL).
`env` is an optional object of at most 64 literal overrides: keys match
`[A-Za-z_][A-Za-z0-9_]*`, at most 128 bytes, case-insensitively unique; values
are strings up to 32 KiB without NUL. Every `OMO_*` override is refused in any
case. Processes inherit the cleaned command-hook environment, with
`OMO_PLUGIN_EVENT=task`; they receive no agent session identifier. Unknown
options and wrong types are refused. Helpers are bound to the executing
plugin: there is no `plugin` option or command-side task endpoint.

`ticket` is an optional positive ticket number (omitted/zero means global).
The server validates that it exists and is open before and after process
registration. Every committed transition to Done, including cancel and story
cascade, stops bound tasks directly from the reliable event-log follower;
cleanup does not pass through a plugin's droppable hook queue.

Each task record has `id`, `name`, optional `ticket`, `started_at` (RFC 3339),
`state` (`running` or `exited`) and `output` (an array, never null). A terminal
record also has `ended_at`, `reason` and, for a failed natural exit, `error`.
Reasons are `exited`, `stopped`, `ticket_done`, `plugin_disabled`,
`plugin_removed`, `plugin_replaced`, `shutdown`, `idle` and `forward_removed`;
the first stop reason wins. The last two are the forward-watch policies below.
No commands, arguments, environments or PIDs appear in records. A failed start
returns a fixed error and retains no task or child. A quick nonzero exit keeps
its status and diagnostic output in the terminal record.

At most 32 tasks run or await their exit callback per plugin. Pending callbacks
keep their identity and remain under that cap; they cannot be evicted as old
history. The newest 64 completed terminal records remain until
they age out or the plugin is removed. Each combined stdout/stderr tail holds
100 lines of at most 1024 characters. Complete lines are redacted before
truncation, including secrets split across writes; raw lines over 64 KiB are
discarded whole with a marker. An unfinished line appears only on process
exit, to avoid exposing a secret prefix before its rest arrives. Configuration
strings, task environment values, inherited credential values and HTTP secrets
known to the calling Lua hook use a private copy of omo's existing redactor (values shorter than
six bytes are ignored; multiline secrets also register their CR/LF-separated
fragments). Only credential-like inherited keys (token/password/secret/API or
private/access key/credential names, common auth/PAT/password suffixes and
DSN/database URLs) register inherited values; HOME, PATH, locale and user names
stay useful in diagnostics. Task-specific secrets do not mutate the hook's
redactor. Hidden characters are escaped visibly. Unbounded
output cannot grow stored logs or lines without limit.

Stop immediately kills the process tree (SIGKILL on Unix, TerminateJobObject
on Windows), then waits for cleanup; there is no graceful shutdown period. The same
cleanup also runs after natural parent exit, plugin disable/removal, code or
installed-tree replacement, and server shutdown. Unix uses a process group;
Windows assigns the suspended process to a kill-on-close job before resuming
it. Configuration refresh cancels old hooks and timers as needed, preserves
background tasks, and keeps the startup lifecycle. An unchanged runtime is
kept intact. Code replacement (manifest/frontend, loaded Lua source, install
record, source/ref/subpath) stops old tasks with `plugin_replaced` and does not
repeat a startup event already delivered. Removal followed by install, re-enable and an actual server restart are
explicit new lifecycles.

The user-only API is:

| Route | Result |
|---|---|
| `GET /api/plugins/{name}/tasks` | `200 {"tasks": [...]}`, including an empty array |
| `POST /api/plugins/{name}/tasks/{task}/stop` with `{}` | `204`; `{task}` is the plugin-local name |

Agent sessions receive 403. Unknown plugins/names or a wrong owner give
`not_found`; stopping a retained exited name succeeds. After name reuse, a
name-based stop targets the latest run. JSON/content-type/size rules are the
normal API rules. `plugin.tasks_changed` carries `{name}`. Starts and exits are batched within
one second. Output-driven invalidations are persisted at most once per plugin
per ten seconds, only after a complete line changes the visible tail; partial
lines and identical tails cause no invalidation. This caps continuous output
at six refresh events per minute per plugin. Lifecycle changes bypass the
output throttle. Clients refetch GET after an invalidation, reconnect or
event-stream resync. TypeScript
clients have `getPluginTasks` and `stopPluginTask`. The plugin settings page
shows each task’s name, ticket, elapsed time, state/error and bounded redacted
output, with Stop for a running task and Refresh for recovery. It refetches on
task invalidation, reconnect and snapshot/resync; failed reads retain the last
known records and offer Refresh. Elapsed time uses the board’s shared clock.
Output stays in a keyboard-accessible scroll box on narrow screens.

Runtime adapters use `Manager.StartTask(ctx, plugin, TaskStart)`,
`Tasks(plugin)`, `StopTask(plugin, name)`, `StopTaskID(plugin, id, reason)` and
`StopTicketTasks(ticket)`. The context bounds creation only. Use the stable
`id` for resources attached to a single run, such as forwards.
`Options.TaskExited func(plugin string, task api.PluginTask)` receives every
successful start's terminal record outside manager locks, before Stop returns.
It must not wait for its own task or call manager shutdown. Configure callbacks
before use; adapters should serialize resource attachment with their exit
callback and verify the task is still running. `Options.TasksChanged func(string)` is a concurrent, nonblocking start/exit
invalidation callback; `Options.TaskOutputChanged func(string)` separately
reports visible output changes for throttled refresh.
`Options.TaskTicketLive func(context.Context, int) bool` is the board adapter.
Server integration lives in `plugin_tasks.go`: ticket cleanup runs before hook
Dispatch; `Server.PluginsReady()` is called after launching both listeners in
`Run`. Embedders call it after serving their own handlers. No automatic task
restart, persistence, editor management or command-side task identity is added.

### Frontend entry and the browser omo facade

Every authenticated board document loads enabled entries, including settings,
ticket pages and terminal windows; OAuth consent pages exclude entries. A classic script must capture its own facade
synchronously and call `initialize` once. The script element’s facade property
is deleted when loading finishes or fails; other entries cannot retrieve it
afterward. There is no shared `window.omo`:

```js
(() => {
  const omo = document.currentScript.omo;
  omo.initialize(() => {
    omo.addButton({
      id: 'open', label: 'Open editor', place: 'ticket', per_worktree: true,
      run: (context) => omo.runHook('open', {
        ticket: context.ticket.number, project: context.project,
      }),
    });
    const unsubscribe = omo.onRouteChange((context) => {
      omo.addPanel({
        id: 'context', place: 'sidebar', title: 'Editor',
        text: `Current page: ${context.page}`,
        image: omo.assetUrl('logo.png'),
      });
    });
    return unsubscribe;
  });
})();
```

Entries are external same-origin scripts, without eval, blob script URLs or a
CSP relaxation. They run with the board user's rights; the existing plugin
installation confirmation covers this. Direct board DOM access is unsupported.
The stable facade methods are:

| Member | Contract |
|---|---|
| `signal` | Read-only `AbortSignal`, aborted when this enabled code lifetime ends or fails. Use it to cancel polling/fetches. |
| `initialize(setup)` | Calls `setup(omo)` once; setup may be async and return a cleanup function. Rejection removes partial controls and offers Retry. A late cleanup runs immediately if the plugin has already ended. |
| `addButton(spec)` | Adds or replaces this plugin's button by `id`; returns a disposer for that registration. Spec: `{id,label,place,per_worktree?,action?,run?}`. `place` is `ticket`, `sidebar` or `project`; `per_worktree` belongs only to ticket. Supply exactly one manual `action` name or `run(context)` callback. Callbacks may return a structured hook result, synchronously or by Promise; void means no message/navigation. |
| `removeButton(id)` | Removes only this plugin's button. |
| `addPanel(spec)` | Adds or replaces `{id,place,title,text,image?}`; returns its registration's disposer. Place is `ticket` or `sidebar`. Text is plain text with hidden characters escaped. Optional image must use this plugin's versioned asset URL. |
| `removePanel(id)` | Removes only this plugin's panel. |
| `context()` | Returns a detached copy of the current route context. |
| `onRouteChange(callback)` | Immediately supplies current context, then each route/context change; returns unsubscribe. Identical context replacements are ignored. Suspended under the lock screen, then delivers changed context on unlock. A failing listener removes that plugin's frontend without affecting others. |
| `runHook(action, context?)` | Runs only this plugin's own named manual hook; resolves `{message,url?,forward_id?}`. Explicit context is `{ticket?,project?}`. Without it, ticket hooks use the current ticket/project; global hooks use the selected project only. No automatic retries, including on a lost response. |
| `assetUrl(path)` | Builds a same-origin versioned URL relative to `frontend.assets`. Refuses traversal/private/Windows paths; throws if no assets are declared. |
| `openForward(id)` | From a user gesture, reserves a detached tab and opens a forward through the authenticated proxy Open API. Resolves void; rejects and closes the unused tab on failure or expired plugin lifetime. |
| `open(callback)` | From a user gesture outside a registered button callback, reserves a tab before calling the async callback, then processes its final structured result. Returns that result. |

IDs are 1–64 letters/digits/underscore/dot/hyphen. At most 64 buttons and 64
panels per plugin; titles/labels are 1–200 visible characters and panel text is
at most 16,000 UTF-16 code units. A disposer removes its own registration even
if the same id was replaced later. Each plugin's facade is frozen and bound to
its installed name; it cannot select another plugin through `runHook`.

Route context is `{page, ticket?, project?}`. Page is `board`, `ticket`,
`settings` or `terminal`. Ticket, when present, is
`{number,title,projects:[{id,worktree}],workdir}`; project is an optional selected
registered project id. Button callbacks receive the clicked ticket and selected
worktree/project, even while another ticket is open. Context never contains
login tokens, agent session identities, route fragments or arbitrary queries.

Registered buttons reserve their tab **before** invoking the hook/callback.
Return the final structured result from `run`; do not call `open` or
`openForward` inside it. For a first-use download, the callback may start once,
show progress with `addPanel`, and poll a separate idempotent status hook until
ready, using `signal` to stop when disabled. Only its final `forward_id` obtains
the short-lived Open grant. A failed/lost action is surfaced without rerunning
it; message-only answers and failures close unused blank tabs. A page the user
navigates in the reserved tab is left alone. Blocked popups still permit a
message-only action; a navigation answer then reports that the action ran and
the browser blocked its tab. Board credentials never enter destinations.

Route changes preserve registrations and the entry's initialization. Disable,
removal, invalidation, a changed installed commit or content identity removes controls/listeners,
aborts signal and runs cleanup; obsolete asynchronous initialization cannot
register again. While locked, controls and hook/open calls are suspended.
Load/initialization/listener failures affect only that plugin and show Retry.
An active board context initializes each installed commit/content identity once
per enabled lifetime (except an explicit retry). Entering OAuth consent ends
that frontend context; returning to the board initializes it again. Updating
assets or entry bytes gives a new address/lifetime.

### Owned Web UI forwards

These Lua helpers are bound to the executing plugin. A plugin cannot select an
owner, list another plugin's forwards, or attach another plugin's task. Command
hooks keep their existing user-level socket access; there is no additional
command-side plugin identity.

| Helper | Result |
|---|---|
| `omo.forward_add(options)` | forward record, or `nil, error` |
| `omo.forward_list()` | own forward array (including activity), or `nil, error`; empty is `{}` in Lua / `[]` in JSON |
| `omo.forward_remove(id)` | `true`, or `nil, error` for unknown/wrong owner |
| `omo.forward_attach(id, task_id)` | `true`, or `nil, error`; associates an existing forward with a running stable task ID |
| `omo.forward_idle_watch(id, idle_seconds)` | `true`, or `nil, error`; schedules idle stop for the attached running task |
| `omo.free_loopback_port()` | integer port candidate on IPv4 `127.0.0.1`, or `nil, error` |

`forward_add` strictly accepts `{name: string, port: integer, ticket?: integer|null,
host?: string, scheme?: "http"|"https", start_path?: string,
strip_prefix?: boolean, verify_tls?: boolean, task_id?: string}`. Name and port
are required. Omitted/null ticket is global (it does **not** inherit the hook's
ticket); pass `event.data.ticket.number` explicitly when binding. Defaults and
normal host/port/name/path validation are the [forward registry's](proxy.md).
Credentials, known hook secrets and authentication query keys are refused in
destinations. Registration never starts or probes a target.

Each record contains `{id, ticket, host, port, scheme, name, start_path,
strip_prefix, verify_tls, base_path, address, plugin, task_id?, activity}`.
`id` is the existing opaque forward ID. `base_path` stays `/proxy/ID/`; `address`
includes `start_path` and prefers a configured subdomain. It contains no omo
token or grant and is an address hint, not a sign-in link. Return
`{forward_id = f.id}` from the manual hook; the frontend must obtain the opaque
URL from the existing **user-only Open API** and navigate that URL unchanged.

Task attachment requires the **stable task ID**, not a reusable name. Forward
and task ticket bindings must match exactly (global means no ticket for both).
A forward's task binding is immutable; attaching the same live ID is idempotent.
Either supply `task_id` at creation, or register first to obtain a base path,
start the task with that base, then call `forward_attach(f.id, task.id)`. If the
task exits during attachment, the attachment fails or its exit callback removes
the forward. Remove a still-unattached forward if task startup fails.

Natural exit and every stop reason remove all forwards attached to that run,
including idle, Stop, ticket completion/cancellation, disable/remove, replacement
and shutdown. Unattached owned forwards also disappear on plugin disable,
removal, code replacement and shutdown. Configuration-only reloads preserve
forwards and tasks. Restart removes **all** stale plugin-owned forwards before
loading plugins; persistent generic user/agent forwards retain their normal
behavior. Removing an **unwatched** forward alone does not stop its task.
Removing a **watched** forward stops its attached stable task with reason
`forward_removed`, even while WebSockets remain open or a deadline callback is
pending. User DELETE and `forward_remove` wait for task reaping; direct board
removal is handled by the reliable event follower, before droppable hook delivery.
Done/cancel/cascade cleanup retains reason `ticket_done`. The first termination
reason wins if exit/expiry/removal are already racing. Task-exit and plugin
lifecycle callbacks release registrations without waiting on their own task;
repeated cleanup cannot stop a later task reusing the name.

Ordinary already open proxy streams retain the transport's existing behavior;
stopping the watched task terminates its process tree and thus any connections
it serves. An independent upstream may keep an old stream open, but the task
is already reaped and closing that stream cannot resurrect its removed watch.

`activity` is `{active_requests: integer, active_websockets: integer,
ever_connected: boolean, no_client_since: string, revision: integer}`.
Counters begin only after proxy authentication and a live lookup, in both path
and subdomain modes. `active_requests` includes pending/upgraded/streaming
requests until the reverse-proxy handler finishes. `active_websockets` counts
only accepted upstream WebSocket upgrades with successful downstream hijacks,
until that full handler ends (including disconnect, cancellation or failure).
Failed authentication and refused upgrades cannot extend the idle interval.
HTTP/SSE/health/status requests are counted as requests but never reset that
interval. This policy fits browser tools which maintain WebSockets; tools whose
presence uses another protocol must implement their own policy.

At registration, `ever_connected=false`, `active_websockets=0` and
`no_client_since` is the registration timestamp. On upgrade, it becomes empty
and `ever_connected=true`; any live socket prevents idle stop regardless of its
age. Only the **last** socket closing starts a new no-client interval. Reconnect
clears it and invalidates any prior deadline. `revision` increases on WS
open/close and watch-policy changes. The timestamp is RFC 3339 nanoseconds for
inspection; elapsed time/deadlines use Go's monotonic clock, not wall-clock
subtraction in Lua. Counts reflect **observed** connection closure, not the
instant a laptop loses physical connectivity; upstream protocol heartbeats or
network failure detection determine when a dead connection is observed.

`forward_idle_watch` accepts a finite duration from **0.01 to 604800 seconds**
(seven days). It creates/replaces one watch per forward, measured from its
current no-client interval, and requires a running attached task. Call it after
readiness; if registration/startup already consumed the interval, it expires
immediately. No watch or idle default exists until the plugin chooses one.
It uses a deadline timer, not the one-minute sweep. At expiration, revision,
zero sockets and the deadline are checked atomically with closing upgrade
admission, then the stable task is stopped with reason `idle`. A reconnect that
wins admission invalidates the old timer; one arriving after stop was committed
is refused. Scheduling may run late under load, but never stops early against
an obsolete revision. Policy replacement preserves the current interval;
repeated calls/polls do not reset it. A watch stops its attached task even if
that task has another forward, so plugins should choose one presence forward
per task. Removal stops a watched task, then cancels its watch; exit/unload also
cancels watches. Deletion and watched identity capture serialize with expiry;
`StopTaskID` waits outside that lock. Expiry rechecks persisted registration,
including before removal events are consumed, and applies `forward_removed`
when the registration has vanished rather than dropping the task's policy.

The loopback helper closes its temporary listener before returning: it is
**not a reservation**. Launch on `127.0.0.1:PORT`, detect bind collisions and
retry with a new candidate a bounded number of times. Never substitute a public
listener or claim that a closed candidate is race-free.

Runtime adapters: `Options.Forwards` implements `plugins.ForwardDesk`;
`Options.Unloaded(plugin, reason)` releases unbound resources after cancellation
and in-flight helper operations, outside manager locks. Stable task cleanup
uses `Options.TaskExited`. Persisted registry ownership is assigned only by
system board adapters; public create JSON accepts neither `plugin` nor `task_id`.
The board emits ordinary add/remove events and `proxy.forward_updated` on first
task attachment. All carry the public record with a relative address.

### Buttons (manual hooks)

A manual hook with `scope: ticket` adds a legacy button to every ticket unless
a declared ticket button uses that hook. A hook with `scope: global` stays
available on the plugin’s settings page, including when it is also declared
in the sidebar. Declared sidebar controls appear in the expanded sidebar and
its mobile sheet; project controls have one row for each enabled project.
`per_worktree` ticket buttons expand for recorded nonempty worktree paths,
sending both ticket and selected project. A projectless ticket with a recorded
working folder sends ticket alone. Without a usable folder the control is
disabled with an explanation, never redirected to another repository. Project
sidebar controls send project alone. Only you press them. One
action per plugin runs at a time, bounded by its timeout.

`POST /api/plugins/{name}/actions/{action}` accepts `{ticket?, project?}`.
`ticket` is a positive ticket number when supplied (zero/omitted means none);
`project` is an optional registered project id. With a ticket it must belong
to that ticket. Ticket-scope actions still require a ticket. The hook receives
`event.data.project = {id: string, kind: "worktree"|"repository", path: string,
available: true, reason: ""}` or null. With a ticket this selects its checked
worktree; without one it selects the enabled project's configured repository
folder, resolved through its user-owned links. Unknown/disabled projects return
404, ticket/project mismatches 422, and unavailable selected folders 409 before
the hook runs. A request accepts identifiers, never a filesystem path.
Existing request bodies remain valid.

Lua may return a string, nil, or a table containing `message`, `url`
and/or `forward_id` string fields. Objects with navigation fields are strict:
unknown fields or invalid navigation fail the action. A command's standard
output may be plain text, a JSON string, or the same JSON object. Legacy
message objects still ignore extra metadata; other command JSON without
navigation fields still displays as plain text. The response always contains
`message` (empty when absent); `url` and `forward_id` are optional and mutually
exclusive. A message may accompany either navigation result:

```lua
return {message = "Editor ready", forward_id = "0123456789abcdef0123456789abcdef"}
-- Or an ordinary external link:
-- return {url = "https://example.org/help?topic=editor#setup"}
```

Messages are cleaned of secrets and hidden characters and cut to 1,000
characters. Navigation URLs are validated separately and preserved exactly,
including escaping/query/fragment bytes: never truncated, redacted or rebuilt
into another address. They must be absolute HTTP(S), at most 8,192 bytes, with
no userinfo, backslashes, whitespace, hidden/control characters or known
configuration/HTTP-request secrets (including URL-encoded secrets). Conventional
authentication query/fragment keys (`token`, `access_token`, `refresh_token`,
`id_token`, `api_key`, `apikey`, `key`, `password`, `passwd`, `authorization`,
`auth`, `bearer`, `grant`, `code`, `__omo_proxy_grant`, case-insensitive with
hyphens treated as underscores), `omo-token.` and bare 64-hex login fragments,
and internal proxy bootstrap URLs (including normalized traversal paths) are
refused. The server also rejects its current login token anywhere in a URL,
including encoded paths or ordinary query values. Errors do not repeat the
unsafe address.

`forward_id` must be the proxy registry's lowercase 32-hex-character identifier.
An integration opening it must call the existing authenticated
`POST /api/proxy/forwards/{id}/open` flow and navigate its opaque returned URL
unchanged; a hook must return the id rather than a proxy login/grant URL. Hook
results contain no board login token. The existing message dialogs display the
`message`; navigation consumers use the additional result fields.

### Example

```lua
-- notify.lua: post tickets that need attention to a webhook
local t = event.data.ticket
local res, err = omo.http{
  method = "POST",
  url = config.webhook_url,
  json = { text = "#" .. t.number .. " " .. t.title .. ": " .. (event.data.reason or "") },
}
if not res then
  omo.log("send failed: " .. err)
elseif res.status >= 300 then
  omo.log("the webhook answered " .. res.status)
end
```

Hooked to `ticket_needs_attention` with `"lua": "notify.lua"`, it runs each time
a ticket is flagged.

The shipped plugins in [plugins/](../plugins/) are complete examples:
`discord` escapes text for its target and retries, `pushover` keeps an hourly
cap in its store, and `pause-notice` uses `omo.agents()` and
`omo.send_to_agent()`.
