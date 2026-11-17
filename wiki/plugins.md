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
| [pause-notice](../plugins/pause-notice/README.md) | Tells every running agent to halt when you switch on **Paused**, and to continue when you switch it off. Without it the pause only keeps omo from starting new agents (see [Pause and stop all agents](concepts.md#pause-and-stop-all-agents)). |

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
  default) or `ticket` (a button on every ticket).

The manifest is read strictly: unknown fields are refused. Fields and events
of plugins written for omo 0.11 (`requires`, `roles`, `manual_args`,
`javascript`, `files`, `prompt_render`, `cron`, `startup` and the like) are
refused with a message saying what to use instead.

### Events

A command hook gets the event as JSON on standard input,
`{"event": "<name>", "at": "<RFC 3339 time>", "data": {...}}`; a Lua hook gets
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
| `timer` | `at` |
| `manual` | `action`, `ticket` (null for a global button) |

A `ticket` carries `number`, `type`, `title`, `column`, `story`, `projects`,
`supervisor`, `attention` and `url` (the ticket's address on the board, without
the login token). No event carries a session id, terminal id, environment
value or file path. Text written by people or agents (titles, comments,
attention texts) is only ever in `data`, never in arguments, environment values
or file names.

Plugins see the events from omo's start on, never older ones. A plugin's hooks
run one at a time, in order. At most 60 events of one kind a minute run a
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
| `omo.sleep(seconds)` | waits at most 10 s, within the run's time limit |
| `omo.store_get(key)`, `omo.store_set(key, value)`, `omo.store_delete(key)`, `omo.store_keys()` | the plugin's own storage in omo's database: keys of 1 to 200 bytes, values up to 64 KiB as JSON, at most 1,000 keys |
| `omo.data_dir()` | the plugin's data folder |
| `omo.agents()` | the agents running now, the CEO included: a list of tables with `terminal` (the id of its web terminal), `name`, `role`, `model`, `purpose`, `project` and `ticket` (`nil` for the CEO) |
| `omo.send_to_agent(terminal, text)` | hands that agent a message the way omo's own notifications do: one line with `omo: ` in front (line breaks become spaces, at most 1,000 characters), submitted once nobody types in that terminal. Returns `true`, or `nil` and why |
| `omo.json_decode`, `omo.json_encode`, `omo.sha256_hex`, `omo.nfc`, `omo.time_parse`, `omo.now`, `omo.log` | helpers |

A run ends at the hook's time limit, and memory is watched. Every configuration
string of six characters or more is replaced by `[redacted]` in the log, in
error texts and in answers, and so are the header values the hook sent with
`omo.http`.

### Buttons (manual hooks)

A manual hook with `scope: ticket` adds a button to every ticket; one with
`scope: global` adds a button on the plugin's page. Only you press them. The
hook's answer shows in a dialog over the ticket, or under the button on the
plugin's page: from Lua a string or `{message = "…"}`, from a command its
standard output (plain text, a JSON string or `{"message": "…"}`), at most 1,000
characters. One button of a plugin runs at a time.

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
