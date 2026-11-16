# one-man-office (omo)

![one-man-office header](.github/assets/header.jpg)

A local board for running AI CLI agents on your Git repositories. `omo` is a single self-contained Go binary: one server per user that keeps tickets (stories and tasks) in SQLite and serves them through an HTTP API, an event stream, and the `omo ai` command for agents.

> [!NOTE]
> This version is a rework in progress. What works today: the server, its API and event stream, persistent settings, the board's ticket rules through the API and `omo ai`, the agent engine, and the board in the browser (live columns, creating and editing tickets, dragging them between columns, a ticket's comments, diff, agents and tasks, agent and user terminals, notifications), and the settings pages (workspace folders and projects, agents, roles, usage, prompts, skill sets, environment, limits, review, plugins and general), usage scripts with smart model assignment, skill sets, plugins and export as a pull request. The `wiki/` folder still describes the earlier design and is outdated.

> [!WARNING]
> The address `omo` prints grants command execution with your permissions. By default the server listens on localhost only and requires the login token. Keep it that way unless you control who can reach the port.

## Install

![Installation overview](.github/assets/installation.jpg)

### Linux and macOS

```bash
curl -fsSL https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/install.sh | sh
```

Or download and inspect the script before running it:

```bash
curl -fsSLO https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/install.sh
sh install.sh
```

### Windows PowerShell

```powershell
irm https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/install.ps1 | iex
```

To inspect it first:

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/scolastico-dev/one-man-office/main/install.ps1 -OutFile install.ps1
.\install.ps1
```

### What the installers do

- Detect the operating system and CPU architecture.
- Verify the latest release checksum.
- Install `omo` into a user-owned directory and add it to `PATH`.
- Keep the current release, or update it when a newer release is available, if you run the installer again.

Set `OMO_INSTALL_DIR` to choose a different installation directory. The defaults are `~/.local/bin` on Linux/macOS and `%LOCALAPPDATA%\Programs\omo` on Windows. Open a new shell after the first install so `PATH` changes take effect.

```bash
OMO_INSTALL_DIR="$HOME/bin" sh install.sh
```

```powershell
$env:OMO_INSTALL_DIR = "$HOME\bin"
.\install.ps1
```

`omo` must be on the `PATH` of the agents themselves because they invoke `omo ai` by name.

### Other ways to install

- **From source**: `make install PREFIX=~/.local`. Building the board needs Node.js 24 and npm; `make build NO_WEB=1` builds a binary without it.
- **Docker**: multi-architecture images, see `Dockerfile` and `compose.yml`. The container listens on port 8090.
- **Later updates**: `omo update` (`omo update --check` only reports).

## Quick start

```bash
omo -d             # start in the background; prints the address, the login token and the log path
omo                # or run in the foreground (Ctrl+C stops it)
omo stop           # stop the running server
```

The server prints `http://omo.localhost:<port>/#<token>` (and the same with `127.0.0.1`). The default port is 8090. The token is created at the first start, stored in `OMO_HOME/token` and reused, so the address keeps working across restarts. A second `omo` prints the running server's address. Open the address in a browser to see the board; its first steps add a project and create the first ticket.

Agents are AI CLIs (Claude Code, Codex, Gemini or any command) described by profiles in `OMO_HOME/profiles/` and assigned to the roles CEO, product manager, developer and reviewer in `OMO_HOME/roles/`. You rarely edit these files: the settings pages do it (see Settings). Once a ticket is approved (moved to Ready), the server's engine does the rest: a story gets a product manager who writes and releases its tasks; each task gets a developer in its own worktree and branch under `OMO_HOME/worktrees`, then a reviewer, and is merged into its story's branch, the story into its base branch (or exported as a pull request when the ticket says so). At most `max_running_agents` (in `settings.yaml`) agents start new work at a time; agents of work already started (reviewers, a developer after a rejection) are never held back. omo merges without checking anything out: the only git command it runs in your own folder is `git merge --ff-only`, and only when nothing there is in the way. An export also reads the folder's remote settings and pushes from a private repository that only borrows its objects. A step that needs a person (a blocked merge, an agent that keeps crashing) flags the ticket; fix the cause and retry it. The engine needs git 2.38 or newer. Every program omo starts (agents, the CEO, export jobs, your shells, git, plugin programs) gets omo's environment without the variables that tie a program to the IDE or terminal omo was started from (`TERM_PROGRAM`, `VSCODE_*`, `CLAUDE_CODE_*`, `TMUX`, `GIT_ASKPASS` and the like), so an omo started from a VS Code terminal does not hand that editor's connection to its agents; the agent environment and the profile's are laid over it.

`omo stop` (or Ctrl+C) asks every working agent for a handoff comment and waits for it at most `stop.handoff_timeout` (2 minutes by default) before it ends the agents; a second `omo stop` or Ctrl+C skips that wait. At the next start, every ticket that was in progress gets a fresh agent that is told to read the handoff and keep uncommitted changes.

Projects (Git repositories) are found in workspace folders (see Settings), added one by one in the browser, or added through the API; tickets are handled with `omo ai`:

```bash
TOKEN=$(cat ~/.local/omo/token)
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"path":"/path/to/repo"}' http://127.0.0.1:8090/api/projects

omo ai project list
omo ai ticket create --type story --title "Login" --project my-repo
omo ai ticket create --type task --title "Form" --project my-repo --story 1
omo ai ticket list
omo ai ticket move 1 ready
omo ai comment 1 "@pm please split this"
omo ai comments 1
```

`omo ai` talks to the running server over a local socket and never starts one. Without `OMO_AGENT_ID` it acts as the user; with it, as that agent. Every `omo ai` command takes `--json`; text written by people or agents is printed indented by four spaces. `omo ai ticket update --base-version N` refuses a description change when someone else changed it after you read version N. `omo ai ticket cancel N [--delete-branch [--discard-uncommitted]]` cancels a ticket (a worktree with uncommitted changes and its branch are kept unless the user also passes `--discard-uncommitted`), `omo ai ticket retry N` retries its failed step, and `omo ai ticket list --supervisor user|ceo|pm` lists the tickets a role supervises. Agents use `omo ai ready`, `omo ai wait` and `omo ai done` (export agents with `--url` or `--failed`). Long or multi-line text goes on standard input: a lone `-` in place of the words of `omo ai comment`, `ask` and `done` (or `--body-file -`, `--comment-file -`, `--failed -`) reads it from there, for example `omo ai comment 4 - <<'EOF'`; a text that is empty or only `-` is refused. Run `omo ai --help` for all verbs.

### Trying it

To try the current build, next to an omo you already run, build it from a checkout and give it its own home and port. You need Go, git 2.38 or newer, Node.js 24 and npm.

```bash
make build
export OMO_HOME=$(mktemp -d)            # a throw-away home, in this shell only
./bin/omo config set port 18123         # any free port; never the port of your other omo
PATH="$PWD/bin:$PATH" ./bin/omo         # prints the address with the login token
```

Open the printed address. `omo config` and `omo stop` use `OMO_HOME` too, so run `./bin/omo stop` in the same shell to stop this one. Delete the folder when you are done. Another omo on its own home and port is not touched. Agents call `omo ai` by name, which is why `bin` is put on `PATH` above.

At the first start of a home, omo looks for `claude`, `codex` and `gemini` on `PATH` and writes a profile for each one it finds into `OMO_HOME/profiles/`. These profiles start the program with the flag that skips its permission prompts, so an agent works without asking you. Their models: Claude Code `fable`, `opus`, `sonnet`, `haiku`; Codex `astra` (`gpt-6-astra`), `sol` (`gpt-6.1-sol`), `luna` (`gpt-5.6-luna`); Gemini `carbon` (`gemini-4-carbon`), `argon` (`gemini-4-argon`), `hydrogen` (`gemini-4-hydrogen`), `flash` (`gemini-3.8-flash`). The strongest one (`fable`, `astra`, `carbon`) goes to the CEO and is not offered to agents for tickets. In a throw-away home this still means real agents with real accounts once you start a ticket. Nothing found: no profile is written, and Settings, Agents has "Detect agent programs".

What to look at first:

1. The first-steps list on the empty board: add a workspace folder (Settings, Projects). omo finds the Git repositories in it.
2. Settings, Roles: which models the CEO, product manager, developer and reviewer use.
3. Create a ticket (a task in one project), approve it by moving it to Ready, and watch the Agents tab of the sidebar.

### The board

Opening a ticket shows its description and fields, and tabs for Comments, Diff, Agents and Tasks (stories only). Comments are Markdown; `@user`, `@pm`, `@developer`, `@reviewer`, `@supervisor` and `@ceo` notify someone, and a comment can be at most 64 KiB. A question from an agent shows in Comments with an Answer button. The Diff tab shows the branch's changes and the changes not yet committed in the worktree; large diffs are cut and say so. It compares with the project's local base branch. When that branch is behind its remote-tracking branch (as of the repository's last fetch; omo never fetches), the tab says so ("local main is 4 commits behind origin/main, so changes from origin/main can show up here as changes of this ticket") and offers "Compare with origin/main"; the choice holds per ticket and project until the page is reloaded. The Agents tab shows a running agent's terminal (typing there types into the agent) or the saved log of one that ended.

A ticket also has a page of its own, `/ticket/<number>` (the panel's "Open as page", or key `p`; "Open in panel" or `p` goes back). The page uses the room better: the tabs Overview, Comments, Diff, Agents and Tasks stand at the top, the fields sit in a column on the right (Hide details; the choice is kept in that browser), and the Diff tab is a file browser with the folder tree at the left (per project, committed and uncommitted changes, a filter, one file at a time with Previous and Next; the panel keeps the stacked diff). The Agents tab's terminal fills the height left. "Back to board" in the top bar, or `b`, returns to the board with its filters. Every ticket link carries the page's address, so a middle click or Ctrl-click opens the page in a new tab, while a plain click stays in the view you are in. The keys `p` and `b` do nothing while you type in a field, a search or a terminal. With the general setting "Open tickets on their own page" (Settings, General; `ui.open_tickets_as_page` in `settings.yaml`, off by default) a click on a ticket from the board, a link, a notification or the sidebar opens the page instead of the panel; "Open in panel" still opens the panel for that visit, and links followed from the panel stay in it. The addresses keep their meaning: `/<number>` is always the panel.

Every terminal (the CEO's, an agent's, your shells) has a pop-out button. It opens the terminal alone in a window of its own (`/terminal/agent/<id>` or `/terminal/user/<id>`, never with the login token in the address); the board shows "This terminal is open in its own window." with "Bring it back" and "Show the window" in its place and holds no connection to it until the window closes. The window remembers its size per kind of terminal. The button is not shown below 768 px or for an ended terminal, and a blocked popup leaves a link to the window's address.

The sidebar next to the board has three tabs: CEO, Agents and Terminals. The CEO starts only when you open its tab. Agents lists the running agents and their slots. Terminals holds your own shells, in a project's folder, in a ticket's worktree (the ticket's Open terminal action) or in your home folder. "New terminal" is a search over the projects (type part of a name or folder; "Home folder" is always first), because a list of many projects is awkward to scroll. A shell in a worktree keeps that worktree: the ticket can still merge and finish, and omo removes the folder when the last shell in it ends. On a narrow screen the sidebar opens as a sheet from the top bar.

The bell in the top bar lists what waits for you (questions, reviews, flagged tickets) and mentions of you. Desktop notifications are off until you switch them on in that browser.

Only you can open shells and read terminals and agent logs; an agent session gets 403 for them. Text from agents and other people is shown with hidden and control characters written out, and text pasted into a terminal is cleaned of control characters first.

### Settings

The gear in the top bar opens the settings. Everything that was a file in `OMO_HOME` can be changed there; the files stay the source, and a hand edit shows up on an open page within a few seconds.

- **Projects**: workspace folders and the projects in them. Add a folder (type its path or pick it with Browse) and omo looks in it for Git repositories, down to a search depth you set (4 by default, at most 16), at start, at an interval you set, when the folders change, and on Rescan. Every repository found becomes an enabled project. It does not look inside a repository, does not follow links, and skips folders that match an ignore pattern (`node_modules` and folders starting with a dot by default). A scan has time and size limits; the report on the page says what was cut. You can switch a found project off; to drop it for good, add an ignore pattern. Projects can still be added one by one. "Select…" turns on a box per project (shift-click or shift with an arrow key selects a range, "Select all" takes everything the filter shows) and "Switch on", "Switch off" and "Remove…" act on the selection; each project is answered on its own, so a project a workspace folder would find again is named with its two ways out (switch it off, or add an ignore pattern) while the others go through. Each project also has its merge modes (see Merging).
- **Agents**: profiles (the program, its arguments, environment and account folder) and their models. "Detect agent programs" looks for `claude`, `codex` and `gemini` on `PATH` and writes a profile for each one that has none. The same happens once at the very first start of omo on a home. The profiles it writes start the programs with the argument that skips their permission prompts, so agents can work without asking you; look at them before you start agents on a repository you care about. A profile of Claude Code, Codex or Gemini has "Add missing models": it adds the models omo suggests for that program which the profile lacks, by name, and leaves the models it has as they are (save or discard your own edits first). Deleting a profile that roles use asks a second time, names the roles and, when you agree, takes its models out of them; the Roles page then names a role left without a model. A profile whose models open tickets use cannot be deleted.
- **Roles**: which models each role uses, in which order, and how one is picked: `failover`, `round_robin` or `smart` (see Usage and limits).
- **Usage**: the soft and hard limit and the usage scripts (see Usage and limits).
- **Prompts**: your own instructions for each role and the messages omo sends; a preview shows the result with sample data, including the text of skill sets.
- **Skill sets**: Git repositories of skills that agents are pointed at (see Skill sets).
- **Environment**: variables every agent starts with. Values are plain text, shown as they are stored. These can hold `$VAR` and backtick commands, which omo expands (and runs) when an agent starts; "Check" runs them and tells you if each variable ends up set. Variables set in a profile or a model are used as written.
- **Plugins**: programs that react to events on the board (see Plugins).
- **Limits**, **Review** and **General**: how many agents run at once and the time limits; the review and release defaults for new tickets, for three kinds of ticket: stories, tasks without a story and tasks in a story (a fresh home: Agent, then human for stories and tasks without a story, Agent for tasks in a story; a task in a story always merges into the story's branch); and the CEO switch, the theme and server facts.

A save is refused, with the reason under the field, when the file changed on disk since the page loaded it. An open page keeps your unsaved edits when the file changes and offers the new value. Leaving a page with unsaved edits asks first.

**Merging.** A local merge uses one of two modes: `merge` (a merge commit) or `squash` (one commit titled "<title> (#N)"). There are three kinds of merge and each has its own mode: a story into its base branch, a standalone task into its base branch and a task into its story's branch (in `settings.yaml` as `merge_defaults.story`, `.task` and `.story_task`, all `merge` by default; Settings, General, "Merge defaults"). A project's own `merge` entry (`story`, `task`, `story_task` in its project file) overrides the default for that kind; empty means "Use global default (<mode>)" and the project row says "merge: overridden" when one is set. A project file with the older `story_merge: squash` is read as the story override.

Prompts, the environment and the profiles are commands that run as you, so only you can read or change them through the API; an agent can only list the projects and the models it may choose. This is a guard rail and not a security boundary: agents run as your user, can talk to the socket without their session header and can write the files in `OMO_HOME`. Do not run an agent you do not trust.

### Usage and limits

A usage script is a Lua file, `OMO_HOME/usage/<name>.lua`, that reports how full an account's usage windows are. A profile names its script (`usage_script`) and each of its models lists the windows it counts (`usage_windows`). omo runs each script once per account at start, on an interval (5 minutes by default) and on Refresh, and keeps the last readings in `run/usage.json`. Profiles with the same script, account folder and environment are one account. A profile without a script is unmetered.

```lua
function usage(account)
  -- account.profile, account.cli, account.account_dir (empty when none), account.home, account.os
  local text, err = omo.read_file("~/my-usage.json")
  if not text then error("no readings: " .. tostring(err), 0) end
  local data = omo.json_decode(text)
  return {
    { name = "session", used = data.session, resets_at = data.session_resets }, -- resets_at is optional
    { name = "weekly",  used = data.weekly },
  }
end
```

The result is a list of at most 32 windows. A name is lowercase letters, digits, dashes and underscores (at most 40 characters) and appears once. `used` is a number from 0 to 100 (more than 100 counts as 100). `resets_at` is RFC 3339 text or Unix seconds. A script that fails or answers something else is an error; omo shows its message (up to 300 characters, with every secret it touched replaced by `[redacted]`).

What a script can use, all under `omo.`: `http{method, url, headers, json | form | body, timeout}` (https anywhere, http only to localhost; at most 8 calls, 1 MiB per answer, 30 s per call), `read_file(path)` (an absolute or `~/` path; regular files up to 1 MiB, never through a link), `env(name)`, `keychain(service)` (macOS only), `codex_refresh(auth_path)` (Codex profiles only: runs the profile's own command until the login file changes), `rate_limited(retry_after)` (ends the run as rate limited: omo waits what the answer's Retry-After asks, from 1 minute to 1 hour, else 2, 4, 8 … minutes up to the refresh interval, before it runs the script again), `json_decode`, `json_encode`, `sha256_hex`, `nfc`, `time_parse`, `now` and `log`. There is no shell, no other file access and no `load`. A run ends after 60 seconds. `omo.env` returns omo's own environment, the literal entries of the Environment page (entries with `$` or a backtick are left out; no command runs), the account folder variable of the profile and the profile's environment.

omo ships two scripts. `claude` reads the login token from `.credentials.json` in the profile's account folder, else in `~/.claude` (on macOS also from the keychain), and asks `api.anthropic.com` for the windows `session`, `weekly` and one `<model>_weekly` per model (`seven_day_fable` is `fable_weekly`). Both call `omo.rate_limited` when the service answers 429. `codex` reads `auth.json` in `CODEX_HOME`, else in `~/.codex`, asks `chatgpt.com`, and has `codex` refresh an expired login once. Gemini has none. Both were written against recorded answers; the tests never call these services, so use "Test" on the Usage page with your own account first. You can edit them, and "Reset" restores omo's version. Scripts run as you with your accounts' credentials. omo never writes a CLI's credential file and never stores, logs or shows a token. A script can send what it reads to any https host: it is your code. Anything that runs as you can also edit the files in `usage/`; the page then says the file changed on disk.

Two limits apply to every window, a soft limit (90 % by default) and a hard limit (98 %); both, the refresh interval and per-window overrides are on the Usage page. A model is `ok`, `soft` (at or above the soft limit), `hard` (at or above the hard limit), `unmetered` or `unknown`. After a failed run the windows of the last successful run keep counting for 30 minutes from the first failed run; the Agents tab shows them as old ("as of 23:12; the last refresh failed: …"). After that, and for a script that never answered, a reading older than 24 hours or a model with none of its windows present, the model is `unknown`; it stays available and the error shows in the Agents tab. A window past its reset time counts as 0 %. A model with an empty `usage_windows` list counts every window of its script except `extra_usage` (paid usage beyond the plan), which counts only when it is listed. A listed window the account does not have (a plan without it) is ignored; only a model none of whose listed windows was reported is `unknown` with a note saying so, in the Agents tab and beside its windows on the Agents page.

- A new ticket is not started on a `soft` or `hard` model. With another model of the role below its limits it uses that one; with none, the card shows "waiting for usage" and starts by itself when a reading drops.
- A ticket that names a model of its own and waits for it shows "Start anyway…" in its panel. It asks first and starts the agent on that model. At the hard limit the button is off, because omo would stop the agent again at once.
- At the hard limit a working agent gets one handoff request, and omo ends it when it answers or after the handoff time limit (Settings, Limits). A parked agent and the CEO end at once. The column, worktree and commits stay, a comment says why, and the ticket goes on with another model of its role or waits for the reset.
- `smart` assignment picks the metered model with the lowest usage and takes turns with models that are unmetered or unknown; without readings it behaves like `round_robin`.

### Skill sets

A skill set is a Git repository of skills (an address over https or ssh, or a folder on this computer; an optional branch or tag). omo keeps one read-only checkout per set in `OMO_HOME/skills/<name>`, never touches it with anything but a fetch, and tells each role where it is: in Settings, Skill sets you can give each role (CEO, product manager, developer, reviewer, export) a text, and an agent of that role gets the text with the checkout's path above it. A set updates at every start of omo or stays pinned; "Update now" fetches again, and a failed fetch keeps the last good checkout. The page shows the commit. The sets are files in `OMO_HOME/skillsets/<name>.yaml`. "Add Superpowers" fills in the Superpowers repository as an offer; no skill set is ever required. Agents run as you and can change the checkout; the next update replaces it. A set that follows a branch hands whoever can push there a say in what your agents are told, so pin a commit you trust.

### Plugins

A plugin is a folder with a `plugin.json` (name, version, description, `default_config` and a list of hooks) that omo installs from a Git repository or a folder on this computer into `OMO_HOME/plugins/<name>`. One set of plugins runs for the whole board; each is switched on or off and has a JSON configuration, both stored in `OMO_HOME/plugins/<name>.yaml`. Plugins run as you, with your rights: install only plugins you trust. Nothing is fetched when omo starts, and omo does not load plugins written for the earlier engine (it says what is not supported).

A hook has an `event` and either a `command` (a list of arguments, started without a shell in the plugin's folder) or a `lua` file. The event is written as JSON (`{"event": ..., "at": ..., "data": ...}`) on standard input of a command and is the table `event` of a Lua hook. Text written by people or agents (titles, comments, attention texts) is in the data only; it is never put into arguments, environment values or file names. The events are:

| Event | Data |
|---|---|
| `ticket_created` | `ticket` |
| `ticket_moved` | `ticket`, `from`, `to`, `action` |
| `comment_written` | `ticket`, `comment` (author, body up to 4 KiB, question, to, mentions) |
| `question_asked` | the same, for questions only |
| `ticket_needs_attention` | `ticket`, `reason` |
| `agent_started`, `agent_exited` | `ticket` (null for the CEO), `agent`, and for exits `reason` (`finished`, `stopped`, `crashed`, `usage`, `omo stopped`) |
| `timer` | `at` (an interval of at least a minute) |
| `manual` | `action`, `ticket` (null for a global action) |

A `manual` hook with a name is a button: on every ticket panel (scope `ticket`) or on the plugin's page (scope `global`). The hook's answer (at most 1,000 characters) shows in a dialog over the ticket panel, or under the button on the plugin's page. A `ticket` carries `number`, `type`, `title`, `column`, `story`, `projects`, `supervisor`, `attention` and `url` (the board's address without a token); no session id, terminal id, environment value or path is in any payload. Command hooks get `OMO_SOCKET`, `OMO_PLUGIN_NAME`, `OMO_PLUGIN_EVENT`, `OMO_PLUGIN_CONFIG` and `OMO_PLUGIN_DATA` but no `OMO_*` variable of omo's own. A hook runs at most 30 seconds by default (1 second to 5 minutes), and at most 60 times a minute per event; a full queue drops events and the log says how many.

Lua hooks run in a sandbox: no `io`, no shell, no `load` or `require`, limits on time, memory and output. They have `event`, `config`, `omo.exec(program, args...)` (no shell), `omo.http`, `omo.store_get|store_set|store_delete|store_keys`, `omo.data_dir()`, `omo.json_decode|json_encode`, `omo.log` and `omo.now`. Every configuration string of six or more bytes is hidden in logs, errors and answers. A plugin's program that calls `omo ai` through `OMO_SOCKET` acts as you; omo cannot tell it from your own commands. Only pushover is bundled (Settings, Plugins offers it: it notifies when a ticket needs you or an agent asks you something, at most 20 messages an hour by default). The log of a plugin shows on its page.

### Reviews

A review round starts when the work is handed in. A reviewer works per project, and its session ends once its verdict is recorded. A project whose approval of an earlier round still holds for the commit handed in now (the approved commit is unchanged) needs no reviewer: its approval carries over. So does a story's project whose branch has no commits of its own compared with its base branch (a task's branch without commits still gets a reviewer). One comment per round names these projects. A rejection is never carried over. The round ends only when every project has a verdict; then one comment says that the round is over, lists the approvals and quotes each rejection for the developer or product manager. A reviewer that stops without a verdict and without an open question of its own is reminded once after `health.silent_after`; after the same time again its step fails and the ticket is flagged.

### Export

A ticket whose "when approved" setting is Export, or the "Export…" button on a ticket in review, does not merge locally. omo exports project by project. A project whose branch has no commits of its own compared with its base branch is left out (one comment names those projects). For every other project omo pushes the commit that was reviewed (the one pinned when the work was handed in) to the remote the project's repository names (`branch.<base>.pushRemote`, `remote.pushDefault`, `branch.<base>.remote`, then `origin`; its `pushurl` first), as a new branch `omo/...`, never forced and without hooks, and writes a comment saying which commit went where. Then an export agent runs for that project in a folder omo made under `OMO_HOME/run/export/`, not in the worktree. omo wrote the pull request's title (the ticket title, up to 200 characters) and body (the description and "Ticket #n on omo.") into that folder; the agent runs `gh` for GitHub, `glab` for GitLab (a host whose first name is `gitlab`) or `tea` for any other host with them, and reports the address with `omo ai done --url` or the failure with `--failed`. omo itself never runs these tools. The address must be https, on the pushed remote's host and inside its repository; with a remote that is a folder on this computer there is no pull request to report and the job ends with its failure. A project that cannot be exported (a push that fails, no remote, a failed or crashing job) does not stop the others. Once every project is done: when each project that was not left out has its pull request, the ticket is Done with the links; when one failed, the ticket stays in Review, flagged with "Export of <project> failed: <reason>" for each (credentials hidden), and the projects exported keep their pull request. Exporting again then handles only the projects without one, and "Merge locally instead" merges only those. When every project was left out there is nothing to export and the ticket is Done. The ticket panel shows each project's pull request or where its export stands beside its branch.

Agents run as you. The guards in these settings keep an agent that goes through the API from drifting; they do not stop one that sets out to act as you.

### Commands

| Command | What it does |
|---|---|
| `omo`, `omo -d` | start the server in the foreground or the background |
| `omo stop` | stop the running server after the agents' handoff (a second `omo stop` skips the wait) |
| `omo config list\|get\|set\|unset` | start options: `host`, `port`, `auth` (`token`, `basic`, `none`), `basic_auth`, `origin_check`, `allowed_hosts`; changes apply at the next start |
| `omo config rotate-token` | replace the login token (applies at the next start) |
| `omo autostart register\|unregister` | start omo at user login |
| `omo update [--check] [--version TAG]` | install a verified release |
| `omo ai ...` | board commands for agents: `ticket`, `link`, `comment`, `comments`, `ask`, `done`, `whoami`, `project`, `model`, `ready`, `wait` |

### Where state lives

Everything is in `OMO_HOME` (default `~/.local/omo`, `%APPDATA%\omo` on Windows; an absolute `OMO_HOME` overrides): `omo.db` (tickets), `server.yaml`, `token`, `settings.yaml`, `projects/`, `profiles/`, `roles/`, `prompts/`, `usage/` (usage scripts), `skillsets/` and `skills/` (skill set settings and their checkouts), `plugins/` (plugin settings and installed plugins), `run/` (lock, log, socket, last usage readings, export folders), `worktrees/` (the agents' worktrees), `ceo/` (the CEO agent's folder) and `logs/` (agent transcripts). Nothing is written into your projects. If `OMO_HOME` holds an installation of an older omo, this version refuses to use it; set `OMO_HOME` to another folder.

## Documentation

The [wiki](wiki/README.md) documents the earlier office-based design and is outdated; it is being rewritten. [CLAUDE.md](CLAUDE.md) describes the current architecture for contributors.

## License

Copyright (C) 2026 Joschua Becker EDV.

Licensed under the [GNU Affero General Public License v3.0 or later](LICENSE).
