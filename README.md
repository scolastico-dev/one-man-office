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

Agents are AI CLIs (Claude Code, Codex, Antigravity, Gemini or any command) described by profiles in `OMO_HOME/profiles/` and assigned to the roles CEO, product manager, developer and reviewer in `OMO_HOME/roles/`. You rarely edit these files: the settings pages do it (see Settings). Once a ticket is approved (moved to Ready), the server's engine does the rest: a story gets a product manager who writes and releases its tasks; each task gets a developer in its own worktree and branch under `OMO_HOME/worktrees`, then a reviewer, and is merged into its story's branch, the story into its base branch (or exported as a pull request when the ticket says so). omo starts new work only while fewer tickets of its kind are in work than `limits.tasks` (4 by default) and `limits.stories` (2) in `settings.yaml` allow. A ticket is in work from the moment it moves from Ready to In Progress until it is Done, so also while it is in review, while it waits for an answer, while its agent waits in `omo ai wait` and while no agent of it runs; a move back to Ready or Planning frees it too. A task counts against `limits.tasks` whether or not it belongs to a story; a story can also limit its own parallel tasks in its panel. Agents of work already started (reviewers, export jobs, a developer after a rejection) are never held back, and the CEO never counts. omo merges without checking anything out: the only git command it runs in your own folder is `git merge --ff-only`, and only when nothing there is in the way. An export also reads the folder's remote settings and pushes from a private repository that only borrows its objects. A step that needs a person (a blocked merge, an agent that keeps crashing) flags the ticket; fix the cause and retry it. The engine needs git 2.38 or newer. Every program omo starts (agents, the CEO, export jobs, your shells, git, plugin programs) gets omo's environment without the variables that tie a program to the IDE or terminal omo was started from (`TERM_PROGRAM`, `VSCODE_*`, `CLAUDE_CODE_*`, `TMUX`, `GIT_ASKPASS` and the like), so an omo started from a VS Code terminal does not hand that editor's connection to its agents; the agent environment and the profile's are laid over it.

`omo stop` (or Ctrl+C) asks every working agent for a handoff comment and waits for it at most `stop.handoff_timeout` (2 minutes by default) before it ends the agents; a second `omo stop` or Ctrl+C skips that wait. At the next start, every ticket that was in progress gets a fresh agent that is told to read the handoff and keep uncommitted changes.

Projects (Git repositories) are found in workspace folders (see Settings), added one by one in the browser, or added through the API; tickets are handled with `omo ai`:

```bash
TOKEN=$(cat ~/.local/omo/token)
curl -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"path":"/path/to/repo"}' http://127.0.0.1:8090/api/projects

omo ai project list
omo ai ticket create --type story --title "Login" --project my-repo
omo ai ticket create --type task --title "Form" --project my-repo --story 1
omo ai ticket create --type task --title "Compare the upload libraries" --no-project
omo ai ticket list
omo ai ticket move 1 ready
omo ai comment 1 "@pm please split this"
omo ai comments 1
```

`omo ai` talks to the running server over a local socket and never starts one. Without `OMO_AGENT_ID` it acts as the user; with it, as that agent. Every `omo ai` command takes `--json`; text written by people or agents is printed indented by four spaces. `omo ai ticket update --base-version N` refuses a description change when someone else changed it after you read version N. `omo ai ticket cancel N [--delete-branch [--discard-uncommitted]]` cancels a ticket (a worktree with uncommitted changes and its branch are kept unless the user also passes `--discard-uncommitted`), `omo ai ticket remove-project N PROJECT [--discard]` takes a project off a ticket that already has a branch there (in the browser: Remove from ticket under Branches; while the ticket is in Planning, Ready For Dev or waits for the user in Review; omo removes its worktree and deletes the ticket's own branch, and asks for `--discard` before it deletes commits the base lacks or uncommitted changes), `omo ai ticket remove-workdir N` removes the working folder of a done task without a project, `omo ai ticket retry N` retries its failed step, and `omo ai ticket list --supervisor user|ceo|pm` lists the tickets a role supervises. Agents use `omo ai ready`, `omo ai wait` and `omo ai done` (export agents with `--url` or `--failed`, after `omo ai export-text`). Long or multi-line text goes on standard input: a lone `-` in place of the words of `omo ai comment`, `ask` and `done` (or `--body-file -`, `--comment-file -`, `--failed -`) reads it from there, for example `omo ai comment 4 - <<'EOF'`; a text that is empty or only `-` is refused. Run `omo ai --help` for all verbs.

### Trying it

To try the current build, next to an omo you already run, build it from a checkout and give it its own home and port. You need Go, git 2.38 or newer, Node.js 24 and npm.

```bash
make build
export OMO_HOME=$(mktemp -d)            # a throw-away home, in this shell only
./bin/omo config set port 18123         # any free port; never the port of your other omo
PATH="$PWD/bin:$PATH" ./bin/omo         # prints the address with the login token
```

Open the printed address. `omo config` and `omo stop` use `OMO_HOME` too, so run `./bin/omo stop` in the same shell to stop this one. Delete the folder when you are done. Another omo on its own home and port is not touched. Agents call `omo ai` by name, which is why `bin` is put on `PATH` above.

At the first start of a home, omo looks for `claude`, `codex`, `agy` and `gemini` on `PATH` and writes a profile for each one it finds into `OMO_HOME/profiles/`. These profiles start the program with the flag that skips its permission prompts, so an agent works without asking you. Their models: Claude Code `fable`, `opus`, `sonnet`, `haiku`; Codex `astra` (`gpt-6-astra`), `sol` (`gpt-6.1-sol`), `luna` (`gpt-5.6-luna`); Antigravity `opus` (`claude-opus-4-6-thinking`), `sonnet` (`claude-sonnet-4-6`), `flash-high` (`gemini-3.8-flash-high`), `flash-low` (`gemini-3.8-flash-low`), `gpt-oss` (`gpt-oss-120b-medium`); Gemini `carbon` (`gemini-4-carbon`), `argon` (`gemini-4-argon`), `hydrogen` (`gemini-4-hydrogen`), `flash` (`gemini-3.8-flash`). The strongest one (`fable`, `astra`, Antigravity's `opus`, `carbon`) goes to the CEO and is not offered to agents for tickets. Google now recommends its Antigravity CLI (`agy`) in place of Gemini CLI; omo supports both. An Antigravity profile starts `agy` with `--dangerously-skip-permissions`, gives the start prompt with `--prompt-interactive` and trusts the agent's worktree for that session only (`--settings` with `trustedWorkspaces`, which agy never saves); its login stays in `~/.gemini/antigravity-cli`, as agy reads no variable for another account folder. Its models come from two quota groups that each share a weekly limit, so the profile's models carry both: the PM and the reviewer get `sonnet`, the developer `flash-high`. In a throw-away home this still means real agents with real accounts once you start a ticket. Nothing found: no profile is written, and Settings, Agents has "Detect agent programs".

What to look at first:

1. The first-steps list on the empty board: add a workspace folder (Settings, Projects). omo finds the Git repositories in it.
2. Settings, Roles: which models the CEO, product manager, developer and reviewer use.
3. Create a ticket (a task in one project, or in none), approve it by moving it to Ready, and watch the Agents tab of the sidebar.

### The board

Opening a ticket shows its description and fields, and tabs for Comments, Diff (not for a task without a project), Agents and Tasks (stories only). Comments are Markdown; `@user`, `@pm`, `@developer`, `@reviewer` (or `@reviewer:<project id>` for one project's reviewer), `@supervisor` and `@ceo` notify someone, and a comment can be at most 64 KiB. On a ticket with several projects typing `@rev` in the comment box lists `@reviewer` (all projects' reviewers) and one `@reviewer:<project id>` for each project, with the project's name and the reviewer behind it; with one project only `@reviewer` is offered. A comment shows `@reviewer:<project id>` as one mention. A question from an agent shows in Comments with an Answer button. The Diff tab shows the branch's changes and the changes not yet committed in the worktree; large diffs are cut and say so. As on a pull request, the lines hidden between, above and below the changes can be shown ("Show 20 hidden lines below line 99", "Show all 57 hidden lines between lines 99 and 157", "Show the rest of the file below line 253"), or a file whole with "Show the whole file"; not for binary, new, deleted or untracked files, nor for a file whose diff with that much context would be over 1 MiB (it says so in place). What you showed stays while the tab is open and across refreshes as long as the file's changes stay the same. It compares with the project's local base branch. When that branch is behind its remote-tracking branch (as of the repository's last fetch; omo never fetches), the tab says so ("local main is 4 commits behind origin/main, so changes from origin/main can show up here as changes of this ticket") and offers "Compare with origin/main"; the choice holds per ticket and project until the page is reloaded. The Agents tab lists every agent of the ticket; a parked one that omo keeps for questions (a reviewer after its verdict, the developer during the review) says so, and an ended one says why ("a new review round of #4 began", "#4 was merged"). It shows a running agent's terminal (typing there types into the agent) or the saved log of one that ended.

A ticket also has a page of its own, `/ticket/<number>` (the panel's "Open as page", or key `p`; "Open in panel" or `p` goes back). The page uses the room better: the tabs Overview, Comments, Diff, Agents and Tasks stand at the top, the fields sit in a column on the right (Hide details; the choice is kept in that browser), and the Diff tab is a file browser with the folder tree at the left (per project, committed and uncommitted changes, a filter, one file at a time with Previous and Next; the panel keeps the stacked diff). The Agents tab's terminal fills the height left. "Back to board" in the top bar, or `b`, returns to the board with its filters. Every ticket link carries the page's address, so a middle click or Ctrl-click opens the page in a new tab, while a plain click stays in the view you are in. The keys `p` and `b` do nothing while you type in a field, a search or a terminal. With the general setting "Open tickets on their own page" (Settings, General; `ui.open_tickets_as_page` in `settings.yaml`, off by default) a click on a ticket from the board, a link, a notification or the sidebar opens the page instead of the panel; "Open in panel" still opens the panel for that visit, and links followed from the panel stay in it. The addresses keep their meaning: `/<number>` is always the panel.

In a diff, the lines between the changes are hidden. A row "Show 20 hidden lines" above, between or below the changed parts reads more of the file, "Show all N hidden lines" opens a whole gap, and "Show the whole file" in a file's header shows every line (on the page and in the panel). It is offered for files that were changed, not for binary, added, deleted or untracked files, and a file over 1 MiB with that much context says so in place; what you opened stays while the file's changes stay the same, also over Refresh. With "Compare with <remote branch>" on, the lines are read from that branch's side too.

Every terminal (the CEO's, an agent's, your shells) has a pop-out button. It opens the terminal alone in a window of its own (`/terminal/agent/<id>` or `/terminal/user/<id>`, never with the login token in the address); the board shows "This terminal is open in its own window." with "Bring it back" and "Show the window" in its place and holds no connection to it until the window closes. The window remembers its size per kind of terminal. The button is not shown below 768 px or for an ended terminal, and a blocked popup leaves a link to the window's address.

The sidebar next to the board has three tabs: CEO, Agents and Terminals. The CEO starts only when you open its tab. Agents lists the running agents and how many tasks and stories are in work against their limits. Terminals holds your own shells, in a project's folder, in a ticket's worktree or the working folder of a task without a project (the ticket's Open terminal action) or in your home folder. "New terminal" is a search over the projects (type part of a name or folder; "Home folder" is always first), because a list of many projects is awkward to scroll. A shell in a worktree keeps that worktree: the ticket can still merge and finish, and omo removes the folder when the last shell in it ends. On a narrow screen the sidebar opens as a sheet from the top bar.

### Images

Comments and descriptions can carry images: "Add image…" under the text box (several files at once), a paste, or a drop onto the box. While a file uploads a placeholder line shows where it goes; then the line becomes `![screenshot.png](omo-image:<id>)`, a normal Markdown image whose alt text is the name the file was uploaded with. A refusal (not a PNG, JPEG, GIF or WebP, larger than 10 MiB, a ticket's 100 images or omo's 2 GiB reached) is shown next to the box with the reason, and nothing is inserted. A new ticket gets images once it exists: edit its description or comment on it. Images show in the thread, the description and its history, in the panel and on the page, fitted to the column; a click (or Enter) opens one full size. An image that expired or was deleted shows its name with "image expired" or "image missing".

omo keeps the files in `OMO_HOME/images/` under a random id (`<id>.png`), never under the uploaded name, and decides from the bytes what a file is; SVG is refused, because an SVG can carry scripts. The board fetches a picture with its login token in the request header and shows it from memory, so the token never goes into an image address. Agents add images with `omo ai image add <ticket> <file>` (where they may comment) and paste the reference it prints; when they read a ticket or its comments with `omo ai`, every image is followed by the local path of its file (`(local file: /home/you/.local/omo/images/<id>.png)`), which their CLI can open. An agent only gets the path of an image whose ticket it may read. The images of a ticket are deleted this many days after it is done or cancelled: Settings, Limits, "Keep images after Done" (60 by default, 0 keeps them for ever); the text then says "image expired". Only you delete an image (`DELETE /api/images/<id>`). Files in `images/` that omo did not write are left alone. An export does not upload images: the pull request names each as "[image: <name>, not exported]".

The bell in the top bar lists what waits for you (questions, reviews, flagged tickets) and mentions of you. Desktop notifications are off until you switch them on in that browser.

Only you can open shells and read terminals and agent logs; an agent session gets 403 for them. Text from agents and other people is shown with hidden and control characters written out, and text pasted into a terminal is cleaned of control characters first.

Copying from a terminal works in every terminal view (the CEO, an agent, your shells, the ticket's Agents tab, a terminal's own window). A program that holds the mouse (Claude Code, Codex, tmux) selects in its own screen and hands the selection to the terminal (OSC 52); omo puts that text into the system clipboard and says "Copied" in the terminal's corner. It accepts this only within 5 seconds of a key or click of yours in that terminal, never from replayed history, and never answers a program that asks to read the clipboard. Over plain HTTP on another host name the browser has no clipboard API and copies only during a click or key press: when it refuses, a "Copy selection" button shows in the corner, and the next key or click in the terminal copies the text too. The terminal's own selection (Shift and drag while a program holds the mouse, or a plain drag in a shell) is copied with Ctrl+C (the selection is then cleared, so the next Ctrl+C interrupts the program again; without a selection Ctrl+C is the interrupt), Ctrl+Insert or Ctrl+Shift+C (Firefox may open its inspector on Ctrl+Shift+C; use Ctrl+Insert there). Ctrl+V, Ctrl+Shift+V and Shift+Insert paste, as a bracketed paste when the program asked for it. On a Mac, Cmd+C and Cmd+V copy and paste and Ctrl keeps its meaning for the program.

Dropping image files onto a terminal, or pasting an image copied in another program (Ctrl+V with no text in the clipboard), stores each image in `OMO_HOME/images/terminal/` and pastes its absolute path into the program, as a file manager pastes a copied file (quoted when it holds spaces, several separated by a space, no line break). Claude Code and Codex then attach the image. Only PNG, JPEG, GIF and WebP up to 10 MiB are taken (2 GiB for all terminal images together); other files are refused with a note in the terminal. A read-only terminal, an ended one and the placeholder of a terminal shown elsewhere take nothing. These images belong to no ticket: they are deleted the "Keep images after Done" number of days after their upload (0 keeps them).

### Settings

The gear in the top bar opens the settings. Everything that was a file in `OMO_HOME` can be changed there; the files stay the source, and a hand edit shows up on an open page within a few seconds.

- **Projects**: workspace folders and the projects in them. Add a folder (type its path or pick it with Browse) and omo looks in it for Git repositories, down to a search depth you set (4 by default, at most 16), at start, at an interval you set, when the folders change, and on Rescan. Every repository found becomes an enabled project. It does not look inside a repository, does not follow links, and skips folders that match an ignore pattern (`node_modules` and folders starting with a dot by default). A scan has time and size limits; the report on the page says what was cut. You can switch a found project off; to drop it for good, add an ignore pattern. Projects can still be added one by one. "Select…" turns on a box per project (shift-click or shift with an arrow key selects a range, "Select all" takes everything the filter shows) and "Switch on", "Switch off" and "Remove…" act on the selection; each project is answered on its own, so a project a workspace folder would find again is named with its two ways out (switch it off, or add an ignore pattern) while the others go through. Each project also has its merge modes (see Merging).
- **Agents**: profiles (the program, its arguments, environment and account folder) and their models. "Detect agent programs" looks for `claude`, `codex`, `agy` and `gemini` on `PATH` and writes a profile for each one that has none. The same happens once at the very first start of omo on a home. The profiles it writes start the programs with the argument that skips their permission prompts, so agents can work without asking you; look at them before you start agents on a repository you care about. A profile of Claude Code, Codex, Antigravity or Gemini has "Add missing models": it adds the models omo suggests for that program which the profile lacks, by name, and leaves the models it has as they are (save or discard your own edits first). Deleting a profile that roles use asks a second time, names the roles and, when you agree, takes its models out of them; the Roles page then names a role left without a model. A profile whose models open tickets use cannot be deleted.
- **Roles**: which models each role uses, in which order, and how one is picked: `failover`, `round_robin` or `smart` (see Usage and limits).
- **Usage**: the soft and hard limit and the usage scripts (see Usage and limits).
- **Prompts**: your own instructions for each role and the messages omo sends; a preview shows the result with sample data, including the text of skill sets.
- **Skill sets**: Git repositories of skills that agents are pointed at (see Skill sets).
- **Environment**: variables every agent starts with. Values are plain text, shown as they are stored. These can hold `$VAR` and backtick commands, which omo expands (and runs) when an agent starts; "Check" runs them and tells you if each variable ends up set. Variables set in a profile or a model are used as written.
- **Plugins**: programs that react to events on the board (see Plugins).
- **Limits**, **Review** and **General**: how many tasks and how many stories may be in work at once, the time limits and how long images are kept after their ticket is done (see Images); the review and release defaults for new tickets, for three kinds of ticket: stories, tasks without a story and tasks in a story (a fresh home: Agent, then human for stories and tasks without a story, Agent for tasks in a story; a task in a story always merges into the story's branch); and the CEO switch, the theme and server facts.

A save is refused, with the reason under the field, when the file changed on disk since the page loaded it. An open page keeps your unsaved edits when the file changes and offers the new value. Leaving a page with unsaved edits asks first.

**Branches.** omo names a ticket's branch by a template (Settings, General, "Branch name template"; `branches.template` in `settings.yaml`), `omo/{slug}` by default. Placeholders: `{slug}` (the title in lower case with dashes, at most 40 characters; `ticket` for a title without letters or digits), `{number}` (the ticket number), `{type}` (`story` or `task`), `{project}` (the project), `{host}` (this machine's name) and `{user}` (your user name on it); any other text is kept as typed, so `feature/{number}-{slug}` or `{user}/{type}/{slug}` work too. The template must contain `{slug}` or `{number}` and give valid git branch names of printable ASCII; the page shows an example name while you type and refuses a template it cannot use with the reason. Names are compared without regard to case, as macOS and Windows do. A name that is taken (a local branch, a remote-tracking branch, one recorded on another ticket of any project in the same repository, the base branch, `main`, `master` or a remote's default branch) gets `-2`, `-3`, ...; a ticket that already has a branch keeps it. The name is picked and the branch created under the repository's lock, so two tickets started together never get one name. omo pushes or deletes only the branch recorded for that ticket, never a base or default branch, and never pushes onto a remote branch of the same name that it did not push itself.

**Merging.** A local merge uses one of two modes: `merge` (a merge commit) or `squash` (one commit titled "<title>"; a story's squash lists its completed tasks by title). A merge commit is titled "Merge: <title>". No message omo writes holds a ticket number, which a forge would read as one of its own issues. There are three kinds of merge and each has its own mode: a story into its base branch, a standalone task into its base branch and a task into its story's branch (in `settings.yaml` as `merge_defaults.story`, `.task` and `.story_task`, all `merge` by default; Settings, General, "Merge defaults"). A project's own `merge` entry (`story`, `task`, `story_task` in its project file) overrides the default for that kind; empty means "Use global default (<mode>)" and the project row says "merge: overridden" when one is set. A project file with the older `story_merge: squash` is read as the story override.

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
    { name = "weekly",  used = data.weekly, label = "Weekly, all models" },     -- so is label
  }
end
```

The result is a list of at most 32 windows. A name is lowercase letters, digits, dashes and underscores (at most 40 characters) and appears once. `used` is a number from 0 to 100 (more than 100 counts as 100). `resets_at` is RFC 3339 text or Unix seconds. `label` is what people read instead of the name: the Agents tab shows it on the meter (the name as its tooltip), the lines for the models agents are kept off and the wait sentence on a ticket ("claude/fable is at its usage limit (Weekly, Fable at 99 %) until 23:40"), and the comment omo leaves when it stops agents for usage, name the fullest window by it (by its name where there is no label), the Usage page's test result and limits table and the usage-windows hint on the Agents page show "label (name)". It is made one line (hidden characters become spaces) and cut to 60 characters; one that holds a secret of the run is refused, as a name is. Models (`usage_windows`) and per-window limits keep referring to the name. A script that fails or answers something else is an error; omo shows its message (up to 300 characters, with every secret it touched replaced by `[redacted]`).

What a script can use, all under `omo.`: `http{method, url, headers, json | form | body, timeout}` (https anywhere, http only to localhost; at most 8 calls, 1 MiB per answer, 30 s per call), `read_file(path)` (an absolute or `~/` path; regular files up to 1 MiB, never through a link), `env(name)`, `keychain(service)` (macOS only), `codex_refresh(auth_path)` (Codex profiles only: runs the profile's own command until the login file changes), `agy_usage()` (Antigravity profiles only: runs the profile's own command as `agy -p /usage --output-format json` in an empty folder, at most 30 seconds, and returns what it printed), `rate_limited(retry_after)` (ends the run as rate limited: omo waits what the answer's Retry-After asks, from 1 minute to 1 hour, else 2, 4, 8 … minutes up to the refresh interval, before it runs the script again), `json_decode`, `json_encode`, `sha256_hex`, `nfc`, `time_parse`, `now` and `log`. There is no shell, no other file access and no `load`. A run ends after 60 seconds. `omo.env` returns omo's own environment, the literal entries of the Environment page (entries with `$` or a backtick are left out; no command runs), the account folder variable of the profile and the profile's environment.

omo ships three scripts. `claude` reads the login token from `.credentials.json` in the profile's account folder, else in `~/.claude` (on macOS also from the keychain), and asks `api.anthropic.com` for its limits: the session limit is `session` ("Session (5 hours)"), the weekly limit of all models `weekly` ("Weekly, all models"), and each weekly limit of one model `<model>_weekly` ("Weekly, Fable" is `fable_weekly`, which only `claude/fable` counts); a limit of a kind the script does not know is skipped with a line in its log. An answer without the `limits` list is read from its older keys (`five_hour`, `seven_day`, `seven_day_<model>`). `extra_usage` is not a model's limit but the paid extra usage beyond the plan: its window is labelled "Extra usage credits (1.32 of 5.00 EUR)" and counts only for a model that lists it. `codex` reads `auth.json` in `CODEX_HOME`, else in `~/.codex`, asks `chatgpt.com` and has `codex` refresh an expired login once. It names each window of a rate limit (the primary and the secondary one) by its length: `session` ("Session") for 1 to 12 hours, `weekly` ("Weekly") for a week give or take 10 %, any other length `window_<hours>h` ("24 hours"); a window that does not say its length is the session as the primary and the weekly window as the secondary one. Each additional limit reports its windows as `<limit name>_session` ("Session, <limit name>"), `<limit name>_weekly` ("Weekly, <limit name>") and so on. A `reset_at` is Unix seconds or RFC 3339 text; one that cannot be read is left out and the run's log says so. An answer with no usable window is an error. Both call `omo.rate_limited` when the service answers 429. `agy` reads agy's own `/usage` command through `omo.agy_usage` (agy asks Google with its own login; the command runs no model turn) and reports each limit of each quota group as a window named by agy's id for it: `gemini-weekly` ("Gemini models, weekly") and `3p-weekly` ("Claude and GPT models, weekly"); the log names the models of each group. Antigravity's `opus`, `sonnet` and `gpt-oss` count `3p-weekly`, `flash-high` and `flash-low` count `gemini-weekly`; a model you add counts every window unless you list its group's window. An answer that names a 429 or `RESOURCE_EXHAUSTED` ends the run as rate limited. Gemini has none. All three were written against recorded answers; the tests never call these services, so use "Test" on the Usage page with your own account first. You can edit them, and "Reset" restores omo's version. An edited script replaces the shipped one for good: a newer omo with a changed default (such as the `limits` reading above) does not touch it and does not tell you, so after an update compare it with "Show default" on the Usage page, or use "Reset". Scripts run as you with your accounts' credentials. omo never writes a CLI's credential file and never stores, logs or shows a token. A script can send what it reads to any https host: it is your code. Anything that runs as you can also edit the files in `usage/`; the page then says the file changed on disk.

Two limits apply to every window, a soft limit (90 % by default) and a hard limit (98 %); both, the refresh interval and per-window overrides are on the Usage page. A model is `ok`, `soft` (at or above the soft limit), `hard` (at or above the hard limit), `unmetered` or `unknown`. After a failed run the windows of the last successful run keep counting for 30 minutes from the first failed run; the Agents tab shows them as old ("as of 23:12; the last refresh failed: …"). After that, and for a script that never answered, a reading older than 24 hours or a model with none of its windows present, the model is `unknown`; it stays available and the error shows in the Agents tab. A window past its reset time counts as 0 %. A model with an empty `usage_windows` list counts every window of its script except `extra_usage` (paid usage beyond the plan), which counts only when it is listed. A listed window the account does not have (a plan without it) is ignored; only a model none of whose listed windows was reported is `unknown` with a note saying so, in the Agents tab and beside its windows on the Agents page.

Per-window limits are keyed by a window name, which means that window of every script (`weekly` applies to Claude's weekly window and to Codex's alike), or by `<script>/<window>`, which means that script's window only (`claude/weekly`, `codex/weekly`). For each value the script's own limit comes first, then the one for the window of every script, then the general soft and hard limit. On the Usage page the window of a limit is chosen from a list: the windows the scripts report now, by script and label ("Claude: Weekly, all models (weekly)"), "weekly of every script" for a window several scripts report, the windows models list that nothing reports now, and "Other…" to type a name before any script has reported. Settings files with bare window names keep working as before; a refusal names the key as written (`usage.windows.claude/weekly.soft_percent`).

- A new ticket is not started on a `soft` or `hard` model. With another model of the role below its limits it uses that one; with none, the card shows "waiting for usage" and starts by itself when a reading drops.
- A ticket that names a model of its own and waits for it shows "Start anyway…" in its panel. It asks first and starts the agent on that model. At the hard limit the button is off, because omo would stop the agent again at once.
- At the hard limit a working agent gets one handoff request, and omo ends it when it answers or after the handoff time limit (Settings, Limits). A parked agent and the CEO end at once. The column, worktree and commits stay, a comment says why, and the ticket goes on with another model of its role or waits for the reset.
- `smart` assignment picks the metered model with the lowest usage and takes turns with models that are unmetered or unknown; without readings it behaves like `round_robin`.

### Skill sets

A skill set is a Git repository of skills (an address over https or ssh, or a folder on this computer; an optional branch or tag). omo keeps one read-only checkout per set in `OMO_HOME/skills/<name>`, never touches it with anything but a fetch, and tells each role where it is: in Settings, Skill sets you can give each role (CEO, product manager, developer, reviewer, export) a text, and an agent of that role gets the text with the checkout's path above it. A set updates at every start of omo or stays pinned; "Update now" fetches again, and a failed fetch keeps the last good checkout. The page shows the commit. The sets are files in `OMO_HOME/skillsets/<name>.yaml`. "Add Superpowers" fills in the Superpowers repository as an offer; no skill set is ever required. Agents run as you and can change the checkout; the next update replaces it. A set that follows a branch hands whoever can push there a say in what your agents are told, so pin a commit you trust.

### Plugins

A plugin is a folder with a `plugin.json` (name, version, description, `default_config` and a list of hooks) that omo installs from a Git repository or a folder on this computer into `OMO_HOME/plugins/<name>`. One set of plugins runs for the whole board; each is switched on or off and has a JSON configuration, both stored in `OMO_HOME/plugins/<name>.yaml`. Plugins run as you, with your rights: install only plugins you trust. Nothing is fetched when omo starts, and omo does not load plugins written for the earlier engine (it says what is not supported).

Settings, Plugins lists omo's plugin list under "Available plugins": the file `plugins/index.yaml` on the `release` branch of omo's repository, read when the page asks and kept for ten minutes (Retry reads it again; when it cannot be read the page says so and everything else works). Each plugin has a tag: `official` plugins are kept in omo's repository by its maintainers and install at once; `recommended` plugins are someone else's code the maintainers looked at, `community` plugins are listed but not reviewed, and for both omo asks first with a warning that names the source. A source you type under "Install plugin…" counts as community. omo decides the tag itself from its list: an install, a change of source or an update from a source other than the installed one is refused without the acknowledgement (`"acknowledge": "recommended"` or `"community"` in the request); an update from the same source needs none. [plugins/README.md](plugins/README.md) says how a plugin gets listed. `OMO_PLUGIN_CATALOG=<source>[#<ref>]` makes omo read the list from another repository and branch (a fork, a local folder); its official plugins then install from there too.

A hook has an `event` and either a `command` (a list of arguments, started without a shell in the plugin's folder) or a `lua` file. The event is written as JSON (`{"event": ..., "at": ..., "data": ...}`) on standard input of a command and is the table `event` of a Lua hook. Text written by people or agents (titles, comments, attention texts) is in the data only; it is never put into arguments, environment values or file names. The events are:

| Event | Data |
|---|---|
| `ticket_created` | `ticket` |
| `ticket_moved` | `ticket`, `from`, `to`, `action` |
| `comment_written` | `ticket`, `comment` (author, body up to 4 KiB, question, to, mentions) |
| `question_asked` | the same, for questions only |
| `ticket_needs_attention` | `ticket`, `reason` |
| `agent_started`, `agent_exited` | `ticket` (null for the CEO), `agent`, and for exits `reason` (`finished`, `stopped`, `crashed`, `usage`, `omo stopped`) |
| `omo_paused`, `omo_resumed` | nothing (`{}`): the "Paused" switch was switched on (also by "Stop all agents") or off |
| `timer` | `at` (an interval of at least a minute) |
| `manual` | `action`, `ticket` (null for a global action) |

A `manual` hook with a name is a button: on every ticket panel (scope `ticket`) or on the plugin's page (scope `global`). The hook's answer (at most 1,000 characters) shows in a dialog over the ticket panel, or under the button on the plugin's page. A `ticket` carries `number`, `type`, `title`, `column`, `story`, `projects`, `supervisor`, `attention` and `url` (the board's address without a token); no session id, terminal id, environment value or path is in any event's payload. Command hooks get `OMO_SOCKET`, `OMO_PLUGIN_NAME`, `OMO_PLUGIN_EVENT`, `OMO_PLUGIN_CONFIG` and `OMO_PLUGIN_DATA` but no `OMO_*` variable of omo's own. A hook runs at most 30 seconds by default (1 second to 5 minutes), and at most 60 times a minute per event; a full queue drops events and the log says how many.

Lua hooks run in a sandbox: no `io`, no shell, no `load` or `require`, limits on time, memory and output. They have `event`, `config`, `omo.exec(program, args...)` (no shell), `omo.http`, `omo.sleep(seconds)` (at most 10 s, within the hook's time limit), `omo.store_get|store_set|store_delete|store_keys`, `omo.data_dir()`, `omo.agents()`, `omo.send_to_agent(terminal, text)`, `omo.json_decode|json_encode`, `omo.log` and `omo.now`. `omo.agents()` lists the agents omo runs right now, the CEO included, each a table with `terminal` (the id of its web terminal, never its session id), `name`, `role`, `model`, `purpose`, `project` and `ticket` (nil for the CEO). `omo.send_to_agent(terminal, text)` hands that agent a message the way omo's own notifications reach it: one line (line breaks become spaces, at most 1,000 characters) with `omo: ` in front, typed into its terminal and submitted once nobody is typing there (an agent parked in `omo ai wait` gets it as the wait's answer, one still starting with its first prompt); it answers `true`, or `nil` and why (`no running agent has this terminal`). An agent in the middle of a turn reads it as typed input at its next step. Every configuration string of six or more bytes is hidden in logs, errors and answers. A plugin's program that calls `omo ai` through `OMO_SOCKET` acts as you; omo cannot tell it from your own commands. The official plugins are pushover ([plugins/pushover](plugins/pushover/README.md)) and discord ([plugins/discord](plugins/discord/README.md)): a Pushover notification or a Discord channel message through a webhook when a ticket needs you or an agent asks you something, at most 20 an hour by default, each with a "Send a test …" button; and pause-notice ([plugins/pause-notice](plugins/pause-notice/README.md)): when you switch the pause on it tells every running agent to halt and wait, and when you switch it off it tells them to continue where they stopped (the pause itself only keeps omo from starting new agents). The log of a plugin shows on its page.

### Tasks without a project

A task can have no project: for work that changes no repository, such as research, an investigation across repositories or a local proof of concept. Choose "No project" in the create dialog or the ticket's Project field (`--no-project` with `omo ai ticket create` and `update`; a task without either flag is refused, so a project is never left out by accident). It can stand alone or belong to a story; a story itself still needs a project. Its developer works in a working folder omo makes for it, `OMO_HOME/workdirs/<ticket>`, a plain folder rather than a git worktree; it is told the folders of the projects omo knows, to read but never change, to put what it makes into its working folder and its findings into the ticket. It hands in with `omo ai done "summary"`; the review (the ticket's review mode as usual, with one reviewer for the ticket itself) judges the summary, the comments and the files in the folder, and an approval moves the ticket to Done: nothing is merged or exported, so "When approved", the base branch, Export and "Merge locally instead" do not apply and are refused. The card shows "no project" where a project would be, the board's project filter has a "No project" entry, and the ticket shows its working folder in place of its branch, without a Diff tab. Open terminal opens a shell in the folder, also after the ticket is done. The folder stays after Done and after a cancel, so a proof of concept does not vanish; "Remove folder" on the done ticket (`omo ai ticket remove-workdir N`) deletes it, and not while a terminal is open in it. Like a branch, the folder locks the ticket's projects and story once work started: such a task stays one without a project.

### Reviews

A review round starts when the work is handed in. A reviewer works per project. After its verdict it stays, parked, so the developer, the product manager or you can ask it about its review: mention `@reviewer` in a comment (on a story with several projects, `@reviewer:<project id>` reaches only that project's reviewer) and it answers in the thread. It ends when the next review round's reviewer of its project starts, or when the ticket is done; a reviewer whose project's approval carries over into the next round stays. The developer of a task (the product manager of a story) likewise keeps its session from In Progress until the ticket is done: parked while the ticket is in review, woken by a rejection, a merge conflict or a mention, so it keeps what it knows. Parked agents hold no slot. A kept agent ends early only when its ticket goes back to Planning or Ready For Dev, its process ends, its model reaches its usage limit, all agents are stopped, or omo stops; after a restart omo starts none of them just to wait (the next round gets a fresh reviewer, and a ticket that goes back to work a fresh developer that reads the thread). A project whose approval of an earlier round still holds for the commit handed in now (the approved commit is unchanged) needs no reviewer: its approval carries over. So does a story's project whose branch has no commits of its own compared with its base branch (a task's branch without commits still gets a reviewer). One comment per round names these projects. A rejection is never carried over. The round ends only when every project has a verdict; then one comment says that the round is over, lists the approvals and quotes each rejection for the developer or product manager. A reviewer that stops without a verdict and without an open question of its own is reminded once after `health.silent_after`; after the same time again its step fails and the ticket is flagged.

### Export

A ticket whose "when approved" setting is Export, or the "Export…" button on a ticket in review, does not merge locally. omo exports project by project. A project whose branch has no commits of its own compared with its base branch is left out (one comment names those projects). For every other project omo pushes the commit that was reviewed (the one pinned when the work was handed in) to the remote the project's repository names (`branch.<base>.pushRemote`, `remote.pushDefault`, `branch.<base>.remote`, then `origin`; its `pushurl` first), as the ticket's branch (named by the branch template), never forced, never onto a branch of that name omo did not push there (also not one created or moved while omo pushes), and without hooks, and writes a comment saying which commit went where. Then an export agent runs for that project in a folder omo made under `OMO_HOME/run/export/`, not in the worktree, and writes the pull request for this repository: omo gives it `material.md` (the ticket's title and description, a story's task titles, this repository's commits and changed files compared with its base, and the other repositories of the ticket with the pull requests they already have) and lets it read the repository. The agent writes a title (conventional-commit style when the repository's history uses it) and a description of what this repository's change does and why, how to verify it and what the reviewer should know, following the repository's pull request template if it has one, with one line naming the other repositories when the ticket has several, and never an omo ticket number. `omo ai export-text` checks the text: what is missing, empty, too long (a title over 200 characters, a description over 60 KiB) or names an omo ticket is replaced by omo's own text (the ticket title; the description, with this repository's commit subjects and the other repositories when there are several), so a failed text never stops the export. Images are named "[image: <name>, not exported]". The agent then runs `gh` for GitHub, `glab` for GitLab (a host whose first name is `gitlab`) or `tea` for any other host, which read the title and body from files, updates the pull request instead when one exists already for the branch (after new commits, for example), and reports the address with `omo ai done --url` or the failure with `--failed`. A comment links each pull request with its title and says whether the export agent or omo wrote the text. omo itself never runs these tools. The address must be https, on the pushed remote's host and inside its repository; with a remote that is a folder on this computer there is no pull request to report and the job ends with its failure. A project that cannot be exported (a push that fails, no remote, a failed or crashing job) does not stop the others. Once every project is done: when each project that was not left out has its pull request, the ticket is Done with the links; when one failed, the ticket stays in Review, flagged with "Export of <project> failed: <reason>" for each (credentials hidden), and the projects exported keep their pull request. Exporting again then handles only the projects without one, and "Merge locally instead" merges only those. When every project was left out there is nothing to export and the ticket is Done. The ticket panel shows each project's pull request or where its export stands beside its branch.

Agents run as you. The guards in these settings keep an agent that goes through the API from drifting; they do not stop one that sets out to act as you.

### Commands

| Command | What it does |
|---|---|
| `omo`, `omo -d` | start the server in the foreground or the background |
| `omo stop` | stop the running server after the agents' handoff (a second `omo stop` skips the wait) |
| `omo config list\|get\|set\|unset` | start options: `host`, `port`, `auth` (`token`, `basic`, `none`), `basic_auth`, `origin_check`, `allowed_hosts`, `public_url` (the address omo advertises, such as `https://omo.example.com/`: printed at the start, shown in Settings, General and used in links to the board; it does not change where omo listens, and omo accepts the Host and Origin of that address only); changes apply at the next start |
| `omo config rotate-token` | replace the login token (applies at the next start) |
| `omo autostart register\|unregister` | start omo at user login |
| `omo update [--check] [--version TAG]` | install a verified release |
| `omo ai ...` | board commands for agents: `ticket`, `link`, `comment`, `comments`, `ask`, `image add`, `done`, `whoami`, `project`, `model`, `ready`, `wait` |

### Where state lives

Everything is in `OMO_HOME` (default `~/.local/omo`, `%APPDATA%\omo` on Windows; an absolute `OMO_HOME` overrides): `omo.db` (tickets), `server.yaml`, `token`, `settings.yaml`, `projects/`, `profiles/`, `roles/`, `prompts/`, `usage/` (usage scripts), `skillsets/` and `skills/` (skill set settings and their checkouts), `plugins/` (plugin settings and installed plugins), `run/` (lock, log, socket, last usage readings, export folders), `worktrees/` (the agents' worktrees), `workdirs/` (the working folders of tasks without a project), `ceo/` (the CEO agent's folder), `logs/` (agent transcripts) and `images/` (uploaded images; `images/terminal/` holds the images dropped into terminals). Nothing is written into your projects. If `OMO_HOME` holds an installation of an older omo, this version refuses to use it; set `OMO_HOME` to another folder.

## Documentation

The [wiki](wiki/README.md) documents the earlier office-based design and is outdated; it is being rewritten. [CLAUDE.md](CLAUDE.md) describes the current architecture for contributors.

## License

Copyright (C) 2026 Joschua Becker EDV.

Licensed under the [GNU Affero General Public License v3.0 or later](LICENSE).
