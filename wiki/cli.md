# The command line

`omo` is one program. Without a command it starts the server; the commands
below manage that server, act on the board and connect agent programs to
omo's MCP endpoint.

| Command | What it is for |
|---|---|
| `omo` | Start the server in the foreground; Ctrl+C stops it. |
| `omo -d` | Start the server in the background and print its address when it is ready. |
| `omo stop` | Stop the running server (agents are asked for a handoff first). |
| `omo config list\|get\|set\|unset` | Read and change the start options in `server.yaml` (see [Running omo](running.md#start-options)). |
| `omo config rotate-token` | Replace the login token. |
| `omo config export\|import` | Write the settings to a zip and apply one again (see [Backup and restore](backup.md)). |
| `omo autostart register\|unregister` | Start omo when you log in (see [Running omo](running.md#start-at-login)). |
| `omo update` | Install a newer verified release (see [Running omo](running.md#updating)). |
| `omo ai …` | Board commands: read, create and move tickets, comment, ask, hand in. Agents use them; you can too. |
| `omo mcp …` | omo's MCP server for agent programs and other MCP clients (see [MCP](mcp.md)). |

Every command uses the omo home named by `OMO_HOME` (an absolute path), else
`~/.local/omo` (`%APPDATA%\omo` on Windows). Commands that talk to the server
(`omo ai`, `omo mcp`, `omo config export|import`) need it running and
otherwise say `omo is not running (start it with: omo)`.

## Using `omo ai` yourself

`omo ai` reaches the running server over a local socket (a named pipe on
Windows), never over the network, and never starts a server. In your own shell
it acts as you, the user. The variable `OMO_AGENT_ID`, which omo sets for
every agent it starts, makes it act as that agent instead.

```bash
omo ai project list
omo ai ticket create --type task --title "Add retry to uploads" --project api --description-file - <<'END'
Retry failed uploads three times.
END
omo ai ticket move 12 ready        # approve it: omo starts a developer
omo ai comments 12
omo ai comment 12 "@developer the retry count should be configurable"
```

- Ticket numbers are written `12` or `#12`.
- Long text goes on standard input: a lone `-` in place of the words of
  `comment`, `ask` and `done`, or a file flag with `-` (`--description-file -`,
  `--body-file -`, `--comment-file -`). An empty text, or one that is only `-`,
  is refused.
- `--json` on any command prints the server's JSON answer instead of text.
- Text written by people or agents is printed indented by four spaces; lines
  at the left margin come from omo.
- Approving a ticket in review is done with the ticket's buttons in the
  browser; `omo ai` has no command for it (see
  [Approval and release](concepts.md#approval-and-release)).

## Web UI forwards

`omo ai proxy add --port 5173 --name "dev server"` registers a forward and
prints its address/base path. Agents default to their own ticket; the user
defaults to global. List with `omo ai proxy list`, remove with
`omo ai proxy remove FORWARD_ID`. Start the app separately after configuring
its base. See [Web UI forwards](proxy.md) for options, examples and sign-in.

## Reference

The help text of every command, exactly as this version prints it
(`omo <command> --help`).

### `omo`

```text
Start the omo server in the foreground. One omo runs per OMO_HOME;
a second start reports the running one. The access URL grants command
execution with your permissions.

Usage:
  omo [flags]
  omo [command]

Available Commands:
  ai            Commands for agents working on the board
  autostart     Register or unregister omo for user login
  config        Read and change the server start options; export and import the settings
  mcp           omo's MCP server for agent programs
  stop          Stop the running omo server
  update        Install a verified omo release

Flags:
  -d, --detached   start in the background and print the access URL when ready
  -h, --help       help for omo
  -v, --version    version for omo

Use "omo [command] --help" for more information about a command.
```

### `omo ai`

```text
The commands agents use to work on the omo board: read tickets, create and
update them, link dependencies, comment, ask questions and hand in work. A
project that already has a branch on a ticket is taken off it with
omo ai ticket remove-project.
Every command talks to the running omo server over its local socket and never
starts one. Without OMO_AGENT_ID you act as the user; with it, as that agent.

Ticket numbers are written 12 or #12. Text arguments (descriptions, comments)
can be given as words, from a file, or from standard input: a lone "-" in
place of the words, or the file flag with "-" (for example --body-file -).
Text must be UTF-8; line endings become \n and whitespace at the very end is
dropped, everything else is sent as it is.
Add --json to any command for the server's raw JSON answer. An error prints
one line and exits with status 1.

Text written by people or agents (descriptions, comments) is indented by four
spaces; lines at the left margin come from omo. An image in a description or
comment is written ![name](omo-image:<id>); omo follows it with the local
path of the image's file, which you can open.

Usage:
  omo ai [flags]
  omo ai [command]

Examples:
  omo ai whoami
  omo ai ticket list --column ready
  omo ai ticket show 12
  omo ai comments 12
  omo ai comment 12 "@developer please check the failing test"
  omo ai image add 12 ./screenshot.png

Available Commands:
  ask         Ask a question on ticket N; the answer arrives as a reply
  comment     Comment on ticket N; @pm, @developer, @reviewer, @supervisor, @ceo or @user notify someone
  comments    Read the comment thread of ticket N, oldest first
  done        Hand in your work on your ticket (reviewers give a verdict)
  export-text Export jobs: have omo check title.txt and body.md for the pull request
  image       Images on tickets: add one, then write its reference into a comment or description
  link        Make ticket N depend on ticket M (or remove that dependency)
  model       List the models you may choose for tickets
  project     List and search the registered projects
  proxy       Register, list and remove web UI forwards
  ready       Tell omo this agent is up and print the instructions for its work
  ticket      Read, create, update and move tickets
  wait        Block until omo has something for you or the timeout passes, then print why
  whoami      Print who you are on the board and which ticket you work on

Flags:
  -h, --help   help for ai
      --json   print the server's JSON answer instead of text

Use "omo ai [command] --help" for more information about a command.
```

### `omo ai ask`

```text
Post a question on ticket N to your supervisor (default), the pm, the ceo or
the user. The text is the words after N, or a file, or standard input with a
lone - or --body-file -. The question stays open until the addressed role or
the user replies to it; read the answer with "omo ai comments N".

Usage:
  omo ai ask N [--to supervisor|pm|ceo|user] [--body-file FILE|-] [TEXT...|-] [flags]

Examples:
  omo ai ask 12 --to user "Should uploads over 2 GB be refused?"

Flags:
      --body-file string   read the text from FILE, or from standard input with -
  -h, --help               help for ask
      --to string          who should answer: supervisor, pm, ceo or user

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai comment`

```text
Post a comment on ticket N. The text is the words after N, or a file, or
standard input with a lone - or --body-file -. Read the thread, and any
reply, with "omo ai comments N". A mention of @pm, @developer, @reviewer,
@supervisor, @ceo or @user notifies the live agent in that role; if nobody is
behind a mentioned role the comment is still stored and a line says so.

Usage:
  omo ai comment N [--reply-to ID] [--body-file FILE|-] [TEXT...|-] [flags]

Examples:
  omo ai comment 12 "@developer the retry count should be configurable"

Flags:
      --body-file string   read the text from FILE, or from standard input with -
  -h, --help               help for comment
      --reply-to int       id of the comment this answers

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai comments`

```text
Print the comments of ticket N in order, one block per comment: id, author,
time, question state, what it replies to, mentioned roles, a blank line, then
the body, indented by four spaces.
System comments (status changes) are marked "system". --since ID prints only
comments with a greater id; --questions prints only questions still open.
Use it to read what you were mentioned in or the answer to your question.

Usage:
  omo ai comments N [--since ID] [--questions] [flags]

Examples:
  omo ai comments 12 --since 40

Flags:
  -h, --help        help for comments
      --questions   only questions that are still open
      --since int   only comments with an id greater than this

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai done`

```text
Report that your work is finished and print the ticket. Reviewers must pass
--approve or --reject; a rejection needs a comment saying what to fix. The
comment is the words after the flags, or a file, or standard input with a
lone - or --comment-file -. An export agent passes --url with the address of
what it published, or --failed with a message saying why it could not
(--failed - reads it from standard input); both stand alone, without
--approve, --reject or a comment.

Usage:
  omo ai done [--approve|--reject|--url URL|--failed MESSAGE|-] [--comment-file FILE|-] [TEXT...|-] [flags]

Examples:
  omo ai done "Retries implemented, tests pass"
  omo ai done --reject "The retry loop never stops on a 4xx answer"
  omo ai done --url https://github.com/org/repo/pull/7
  omo ai done --failed "no permission to push"

Flags:
      --approve               reviewers: approve the work
      --comment-file string   read the comment from FILE, or from standard input with -
      --failed string         export agents: why the export failed (- reads it from standard input)
  -h, --help                  help for done
      --reject                reviewers: reject the work (needs a comment)
      --url string            export agents: the address of what you published

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai export-text`

```text
Check the pull request's title and body that you wrote into title.txt and
body.md in your folder. omo copies each one it can use to pr-title.txt and
pr-body.md, which the command in your instructions sends; for one it cannot use
(missing, empty, too long, naming an omo ticket) its own text stays there and
the reason is printed. Fix the file and run it again if you like.

Usage:
  omo ai export-text [flags]

Examples:
  omo ai export-text

Flags:
  -h, --help   help for export-text

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai image`

```text
Images on tickets: add one, then write its reference into a comment or description

Usage:
  omo ai image [flags]
  omo ai image [command]

Examples:
  omo ai image add 12 ./screenshot.png

Available Commands:
  add         Upload an image file to ticket N and print its Markdown reference

Flags:
  -h, --help   help for image

Global Flags:
      --json   print the server's JSON answer instead of text

Use "omo ai image [command] --help" for more information about a command.
```

### `omo ai image add`

```text
Upload a PNG, JPEG, GIF or WebP file (at most 10 MiB) to ticket N, where you
may comment. omo stores it in its images folder under a new id and prints the
Markdown reference to paste into a comment or description, for example
![screenshot.png](omo-image:<id>), and the local path of the stored file.
When you read a ticket or its comments, every such reference is followed by
the local path of the file, which you can open.

Usage:
  omo ai image add N FILE [flags]

Examples:
  omo ai image add 12 ./screenshot.png

Flags:
  -h, --help   help for add

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai link`

```text
Make ticket N wait for ticket M, or with --remove drop that dependency. A
dependency that would form a cycle is refused and the cycle is named.

Usage:
  omo ai link N --depends-on M [--remove] [flags]

Examples:
  omo ai link 12 --depends-on 11

Flags:
      --depends-on int   the ticket N depends on (required)
  -h, --help             help for link
      --remove           remove the dependency instead of adding it

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai model`

```text
List the models you may choose for tickets

Usage:
  omo ai model [flags]
  omo ai model [command]

Examples:
  omo ai model list

Available Commands:
  list        List models, one per line: id name (cli) description

Flags:
  -h, --help   help for model

Global Flags:
      --json   print the server's JSON answer instead of text

Use "omo ai model [command] --help" for more information about a command.
```

### `omo ai model list`

```text
List the models that can be set on a ticket with --model, as
"<id> <name> (<cli>) <description>". Agents only see selectable models.

Usage:
  omo ai model list [flags]

Examples:
  omo ai model list

Flags:
  -h, --help   help for list

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai project`

```text
List and search the registered projects

Usage:
  omo ai project [flags]
  omo ai project [command]

Examples:
  omo ai project list

Available Commands:
  list        List projects, one per line: id name path and branches
  search      Find projects whose name or path contains Q

Flags:
  -h, --help   help for project

Global Flags:
      --json   print the server's JSON answer instead of text

Use "omo ai project [command] --help" for more information about a command.
```

### `omo ai project list`

```text
List the registered projects as "<id> <name> <path> [on <branch>, default <branch>]".
The id is what --project takes. "on" names the branch checked out in the
project folder, which a new ticket is based on; "default" the repository's
default branch. A project whose folder is not on its default branch is
marked "not the default branch"; a branch omo cannot tell is left out.
Disabled projects are only listed with --all and are marked.

Usage:
  omo ai project list [--all] [flags]

Examples:
  omo ai project list

Flags:
      --all    include disabled projects
  -h, --help   help for list

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai project search`

```text
List the projects matching Q in the same form as "project list".

Usage:
  omo ai project search Q [flags]

Examples:
  omo ai project search api

Flags:
  -h, --help   help for search

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai proxy`

```text
Register, list and remove web UI forwards

Usage:
  omo ai proxy [flags]
  omo ai proxy [command]

Examples:
  omo ai proxy add --port 5173 --name "dev server"

Available Commands:
  add         Register a web UI forward for your own ticket (global for the user), returning address and base_path; start the target separately
  list        List web UI forwards within your ticket read scope
  remove      Remove a web UI forward by id

Flags:
  -h, --help   help for proxy

Global Flags:
      --json   print the server's JSON answer instead of text

Use "omo ai proxy [command] --help" for more information about a command.
```

### `omo ai proxy add`

```text
Register a web UI that someone else runs on the omo host or another host.
Agents register only for their own ticket; --ticket defaults to that ticket.
As the user, omit --ticket for a global forward. The target process is not
started by omo. Register first, then configure path-mode apps with the printed
base_path. With --strip-prefix the upstream receives paths without that prefix;
use it for relative-address apps. Subdomain addresses always send unprefixed
paths: configure the app at /. Path URLs remain available. HTTPS targets accept
self-signed certificates by default; --verify-tls enables certificate checks.
Registration never probes the target.
Ticket forwards are removed when the ticket is done or cancelled.

Usage:
  omo ai proxy add --port N --name NAME [flags]

Examples:
  omo ai proxy add --port 5173 --name "dev server"

Flags:
  -h, --help                help for add
      --host string         target host name or IP address, including IPv6 (default "127.0.0.1")
      --name string         display name of the web UI
      --port int            target port (1-65535)
      --scheme string       target scheme: http or https (default "http")
      --start-path string   start path within the forward, optionally with a query (default "/")
      --strip-prefix        strip the proxy prefix for apps using relative addresses
      --ticket int          ticket number (default: your own ticket; global for the user)
      --verify-tls          verify an HTTPS target's certificate (default: accept self-signed)

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai proxy list`

```text
List forwards for your own ticket, or --ticket N for another ticket you
may read. As the user, omitting --ticket lists all forwards, including globals.
Targets are not probed, so an offline web UI remains listed.

Usage:
  omo ai proxy list [--ticket N] [flags]

Examples:
  omo ai proxy list

Flags:
  -h, --help         help for list
      --ticket int   ticket number (default: your own ticket; all readable forwards for the user)

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai proxy remove`

```text
Remove a forward. Agents remove only forwards of their own ticket;
the user may remove any forward, including globals. This does not stop the
target process.

Usage:
  omo ai proxy remove ID [flags]

Examples:
  omo ai proxy remove FORWARD_ID

Flags:
  -h, --help   help for remove

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai ready`

```text
Announce that the agent is ready and print the prompt that tells it what to do.

Usage:
  omo ai ready [flags]

Examples:
  omo ai ready

Flags:
  -h, --help   help for ready

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai ticket`

```text
Read, create, update and move tickets

Usage:
  omo ai ticket [flags]
  omo ai ticket [command]

Examples:
  omo ai ticket show 12

Available Commands:
  cancel         Cancel ticket N and print it as #N type column title
  create         Create a ticket and print it as #N type column title
  list           List tickets, one per line: #N type column title
  move           Move ticket N to a column (planning, ready, in_progress, review, done)
  remove-project Take a project off ticket N, with the worktree and branch omo made for it
  remove-workdir Remove the working folder of done ticket N, a task without a project
  retry          Retry the failed step of ticket N and print it as #N type column title
  show           Print one ticket with its description (default: your own ticket)
  update         Change a ticket's title, description, model or projects

Flags:
  -h, --help   help for ticket

Global Flags:
      --json   print the server's JSON answer instead of text

Use "omo ai ticket [command] --help" for more information about a command.
```

### `omo ai ticket cancel`

```text
Close ticket N as cancelled; cancelling a story also cancels its open tasks.
The user cancels every ticket, the CEO tickets it created, a product manager
tasks it created in its story. --delete-branch asks omo to also delete the ticket's
work branch. A worktree with uncommitted changes keeps the branch; only the
user may add --discard-uncommitted to throw those changes away.

Usage:
  omo ai ticket cancel N [--delete-branch [--discard-uncommitted]] [flags]

Examples:
  omo ai ticket cancel 12 --delete-branch

Flags:
      --delete-branch         also delete the ticket's work branch
      --discard-uncommitted   with --delete-branch: also discard uncommitted changes in the worktree (user only)
  -h, --help                  help for cancel

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai ticket create`

```text
Create a story or a task. The description comes from a file, or from standard
input with --description-file - (no shell quoting needed).

A task has one project (--project), or none with --no-project: a task without
a project is for work that changes no repository, such as research, an
investigation across repositories or a local proof of concept. Its agent works
in a working folder omo makes for it (and keeps after the task is done), may
read the projects but not change them, and hands in its findings; approving it
merges and exports nothing. Code changes to a project need a task with that
project.

Usage:
  omo ai ticket create --type story|task --title TITLE [flags]

Examples:
  omo ai ticket create --type task --title "Add retry" --project api --story 3 --description-file - <<'EOF'
  Retry failed uploads three times.
  EOF
  omo ai ticket create --type task --no-project --title "Compare the upload libraries" --description-file - <<'EOF'
  Which of the two libraries retries uploads, and how? Write the findings as comments.
  EOF

Flags:
      --depends-on ints           ticket number this one depends on; repeat for several
      --description-file string   read the description from FILE, or from standard input with -
  -h, --help                      help for create
      --model string              model id for its agent; leave unset so omo uses the role's configured models, set it only on request or for a reason you state on the ticket
      --no-project                create a task without a project (research, an investigation, a local experiment)
      --project stringArray       project id; repeat for several projects
      --story int                 the story a task belongs to
      --title string              ticket title (required)
      --type string               story or task (required)

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai ticket list`

```text
List the tickets you may read, one per line as "#<number> <type> <column> <title>".
Filters combine. Done tickets are limited to the most recent ones.

Usage:
  omo ai ticket list [flags]

Examples:
  omo ai ticket list --column ready --type task

Flags:
      --column string       only this column: planning, ready, in_progress, review or done
  -h, --help                help for list
      --project string      only tickets of this project
      --search string       only tickets whose title or description contain this text
      --story int           only the tasks of this story
      --supervisor string   only tickets this role supervises: user, ceo or pm
      --type string         only this type: story or task

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai ticket move`

```text
Move a ticket to another column and print it as #N type column title. Only the
moves the board rules allow are accepted; a refusal says why.

Usage:
  omo ai ticket move N COLUMN [flags]

Examples:
  omo ai ticket move 12 ready

Flags:
  -h, --help   help for move

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai ticket remove-project`

```text
Take PROJECT off ticket N and print it as #N type column title. omo removes
the worktree it made for the ticket in that project and deletes the ticket's
branch there (only a branch omo made for this ticket; any other is kept).
Agents of the ticket that only wait in that project are ended; while one works
there, the removal is refused.

Allowed while the ticket is in Planning, in Ready For Dev, or in Review waiting
for the user (also after a failed export). A ticket keeps at least one project
here: a ticket with a branch does not become one without a project (a task
without a project is created as one, or changed with omo ai ticket update
--no-project while it has no branch). A story keeps a project while a task of
it that is not done uses it; done
tasks keep the project as their history. The user removes projects from every
ticket, the CEO from tickets it created or supervises.

If the branch has commits its base branch does not contain, or the worktree has
uncommitted changes, the removal is refused and lists them. --discard deletes
them with the project; use it only when the user agreed to lose them.

Usage:
  omo ai ticket remove-project N PROJECT [--discard] [flags]

Examples:
  omo ai ticket remove-project 1 github.com-acme-preview
  omo ai ticket remove-project 1 github.com-acme-preview --discard

Flags:
      --discard   also delete the branch's commits its base branch lacks and the worktree's uncommitted changes
  -h, --help      help for remove-project

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai ticket remove-workdir`

```text
Remove the working folder omo made for ticket N, a task without a project,
with everything in it, and print the ticket as #N type column title. omo keeps
the folder after the task is done, so the user can look at what its agent left
there; it is removed only this way. Only the user removes it, only once the
ticket is done, and not while a terminal is open in it.

Usage:
  omo ai ticket remove-workdir N [flags]

Examples:
  omo ai ticket remove-workdir 12

Flags:
  -h, --help   help for remove-workdir

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai ticket retry`

```text
Start again the step of ticket N that stopped and needs a person: its agent
start, an agent that ended without handing in, or its merge. The user retries
every ticket, the CEO the tickets it supervises, a product manager the tasks of
its story.

Usage:
  omo ai ticket retry N [flags]

Examples:
  omo ai ticket retry 12

Flags:
  -h, --help   help for retry

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai ticket show`

```text
Print a ticket: a block of "key: value" lines, a blank line, then the
description, indented by four spaces. Without N, an agent sees the ticket it
works on. Every image in the description is followed by the local path of its
file.

Usage:
  omo ai ticket show [N] [flags]

Examples:
  omo ai ticket show 12

Flags:
  -h, --help   help for show

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai ticket update`

```text
Change the given fields of ticket N and print it as #N type column title.
The description comes from a file, or from standard input with
--description-file -. --model "" returns to the models configured for the
role.
--base-version N (the "description version:" of ticket show) refuses the new
description if someone changed it since you read version N. The description
is stored first; if the other changes are then refused, the error says so.
--project replaces the projects only while the ticket has no branch yet; a
project that has one is taken off with omo ai ticket remove-project.
--no-project makes a task one without a project, under the same rule; once
work on a task without a project started, its projects stay as they are.

Usage:
  omo ai ticket update N [flags]

Examples:
  omo ai ticket update 12 --title "Add retry to uploads"
  omo ai ticket update 12 --base-version 3 --description-file - <<'EOF'
  Retry failed uploads three times.
  EOF

Flags:
      --base-version int          the description version you last read; a newer one refuses the change
      --description-file string   read the new description from FILE, or from standard input with -
  -h, --help                      help for update
      --model string              model id for its agent ("": the role's configured models); set it only on request or for a reason you state on the ticket
      --no-project                make the task one without a project
      --project stringArray       replace the projects; repeat for several
      --title string              new title

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai wait`

```text
Wait for the next notice meant for you: an answer to your question, a comment
that mentions your role, a done task of your story (a product manager), or a
line from omo itself such as a stop request. Prints the notice; when DUR passes
first it prints a message saying so. DUR is a duration such as 90s or 10m.

Usage:
  omo ai wait [--timeout DUR] [flags]

Examples:
  omo ai wait --timeout 10m

Flags:
  -h, --help             help for wait
      --timeout string   longest time to wait, for example 90s or 10m (default "10m")

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo ai whoami`

```text
Print your role, name and own ticket (the user has no ticket).

Usage:
  omo ai whoami [flags]

Examples:
  omo ai whoami

Flags:
  -h, --help   help for whoami

Global Flags:
      --json   print the server's JSON answer instead of text
```

### `omo autostart`

```text
Register or unregister omo for user login

Usage:
  omo autostart [command]

Available Commands:
  register    Enable user login autostart with the current server options
  unregister  Disable user login autostart without stopping the running server

Flags:
  -h, --help   help for autostart

Use "omo autostart [command] --help" for more information about a command.
```

### `omo autostart register`

```text
Enable user login autostart with the current server options

Usage:
  omo autostart register [flags]

Flags:
  -h, --help   help for register
```

### `omo autostart unregister`

```text
Disable user login autostart without stopping the running server

Usage:
  omo autostart unregister [flags]

Flags:
  -h, --help   help for unregister
```

### `omo config`

```text
Read and change the server start options; export and import the settings

Usage:
  omo config [command]

Available Commands:
  export       Write omo's settings to a zip file
  get          Print one option
  import       Check a settings zip and, with --yes, apply it
  list         List every option
  rotate-token Replace the login token
  set          Change one option
  unset        Reset one option to its default

Flags:
  -h, --help   help for config

Use "omo config [command] --help" for more information about a command.
```

### `omo config export`

```text
Write omo's settings to a zip file: the general settings, server.yaml,
projects, agent profiles, roles, prompts, usage scripts, skill sets, plugin
settings and the MCP servers omo relays. With --with-db a consistent copy of
the database (tickets, comments, history) is added.

The file contains your tokens and environment values; keep it private.
omo must be running.

Usage:
  omo config export FILE [flags]

Flags:
  -h, --help      help for export
      --with-db   also include the database (tickets, comments, history)
```

### `omo config get`

```text
Print one option. Unlike config list, which masks basic_auth as ***, get prints basic_auth in clear text.

Usage:
  omo config get KEY [flags]

Flags:
  -h, --help   help for get
```

### `omo config import`

```text
Check a zip written by omo config export (or the Backup page) and show
which files it replaces. Nothing changes without --yes. Files the zip does
not hold are left alone; the replaced files are kept in a folder below
backups/ in the omo home.

--server also writes server.yaml (start options; they apply at the next
start). --with-db also replaces the database with the zip's, which replaces
all tickets: it is refused while agents run, omo pauses, and the database is
swapped when omo starts again. omo must be running.

Usage:
  omo config import FILE [flags]

Flags:
  -h, --help      help for import
      --server    also write server.yaml (listen address, sign-in, public_url)
      --with-db   also replace the database (all tickets) at the next start
      --yes       apply the zip
```

### `omo config list`

```text
List every option

Usage:
  omo config list [flags]

Flags:
  -h, --help   help for list
```

### `omo config rotate-token`

```text
Replace the login token

Usage:
  omo config rotate-token [flags]

Flags:
  -h, --help   help for rotate-token
```

### `omo config set`

```text
Change one option (see config list for the names).

public_url is the address omo advertises: printed at the start, shown on the
General page and used in links to the board. Use an absolute http:// or
https:// address without path, such as https://omo.example.com/. It does not
change where omo listens (host and port); omo accepts the Host and Origin of
that address, nothing else. An empty value or config unset removes it.

proxy_base_host enables Web UI subdomains: a bare DNS host such as apps.localhost
(no scheme, port, path or wildcard). Empty disables them. proxy_label_prefix and
proxy_label_suffix compose one label around the 32-character forward id, with
at most 63 characters and alphanumeric ends. For a suffix starting with a hyphen:
  omo config set -- proxy_label_suffix -user123
Wildcard DNS and any HTTPS reverse proxy must route these hosts to omo's existing
port. The base host remains the board; Open prefers each forward's subdomain.

Usage:
  omo config set KEY VALUE [flags]

Flags:
  -h, --help   help for set
```

### `omo config unset`

```text
Reset one option to its default

Usage:
  omo config unset KEY [flags]

Flags:
  -h, --help   help for unset
```

### `omo mcp`

```text
omo serves its "omo ai" commands as MCP tools, and the tools of the MCP servers it relays,
at /mcp. Agent programs reach it through "omo mcp stdio", which acts as the agent session
of OMO_AGENT_ID, as "omo ai" does. "omo mcp install" registers that bridge in the agent
programs; "omo mcp token" manages the tokens other MCP clients reach /mcp with.

Usage:
  omo mcp [command]

Available Commands:
  install     Register omo's MCP server in the agent programs
  status      Show the relayed MCP servers and what omo's MCP endpoint offers here
  stdio       Serve omo's MCP endpoint on standard input and output
  token       Manage the personal tokens MCP clients reach omo with
  uninstall   Remove omo's MCP server from the agent programs

Flags:
  -h, --help   help for mcp

Use "omo mcp [command] --help" for more information about a command.
```

### `omo mcp install`

```text
Show how omo registers its MCP server ("omo mcp stdio") in the configuration of the agent
programs: every program and profile account omo knows, the targets of one PROGRAM (claude,
codex, agy), or one TARGET by the id the MCP page shows. Nothing changes without --yes; omo
keeps a copy of every file it changes. omo must be running.

Usage:
  omo mcp install [PROGRAM|TARGET] [flags]

Flags:
  -h, --help   help for install
      --yes    apply the changes shown
```

### `omo mcp status`

```text
List the MCP servers omo relays with their states (outside an agent session), then connect
to omo's MCP endpoint as "omo mcp stdio" would in this terminal and list the tools it offers.

Usage:
  omo mcp status [flags]

Flags:
  -h, --help   help for status
```

### `omo mcp stdio`

```text
Serve omo's MCP endpoint on standard input and output, for an agent program that starts it
as an MCP server. It reaches omo over its local socket (OMO_SOCKET, else the omo home's)
as the agent session of OMO_AGENT_ID; without it the connection is tied to no omo agent
session and offers none of omo's board tools.

Usage:
  omo mcp stdio [flags]

Flags:
  -h, --help   help for stdio
```

### `omo mcp token`

```text
Manage the personal access tokens MCP clients outside omo send as bearer to /mcp. A token
is tied to no omo agent session: it reaches only the relayed servers marked global, never
omo's board tools. omo must be running.

Usage:
  omo mcp token [command]

Available Commands:
  create      Create a token and print its secret once
  list        List the personal tokens and OAuth grants
  revoke      Revoke a personal token or an OAuth grant

Flags:
  -h, --help   help for token

Use "omo mcp token [command] --help" for more information about a command.
```

### `omo mcp token create`

```text
Create a personal access token named NAME and print its secret. omo keeps only a hash: the
secret cannot be shown again. --expires ends it after a duration (90d, 12h) or on a date
(2027-01-31); without it the token lasts until it is revoked.

Usage:
  omo mcp token create NAME [flags]

Flags:
      --expires string   end the token after a duration (90d, 12h) or on a date (2027-01-31)
  -h, --help             help for create
```

### `omo mcp token list`

```text
List the personal tokens and OAuth grants

Usage:
  omo mcp token list [flags]

Flags:
  -h, --help   help for list
```

### `omo mcp token revoke`

```text
Revoke a personal token or an OAuth grant

Usage:
  omo mcp token revoke ID [flags]

Flags:
  -h, --help   help for revoke
```

### `omo mcp uninstall`

```text
Show how omo removes its MCP server ("omo mcp stdio") in the configuration of the agent
programs: every program and profile account omo knows, the targets of one PROGRAM (claude,
codex, agy), or one TARGET by the id the MCP page shows. Nothing changes without --yes; omo
keeps a copy of every file it changes. omo must be running.

Usage:
  omo mcp uninstall [PROGRAM|TARGET] [flags]

Flags:
  -h, --help   help for uninstall
      --yes    apply the changes shown
```

### `omo stop`

```text
Stop the running omo server

Usage:
  omo stop [flags]

Flags:
  -h, --help   help for stop
```

### `omo update`

```text
Install a verified omo release

Usage:
  omo update [flags]

Flags:
      --check            report whether a newer release is available without installing it
  -h, --help             help for update
      --version string   install this exact release tag, including an older release
```

