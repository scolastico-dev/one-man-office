# Troubleshooting

The messages you are most likely to meet, and what to do. omo's own log is
`OMO_HOME/run/omo.log`; each agent's transcript is in `OMO_HOME/logs/`, and an
ended agent's log is also on the ticket's Agents tab (**Show log**).

- [omo does not start](#omo-does-not-start)
- [The browser cannot open the board](#the-browser-cannot-open-the-board)
- [Nothing starts](#nothing-starts)
- [A ticket says needs attention](#a-ticket-says-needs-attention)
- [A merge is blocked](#a-merge-is-blocked)
- [An export failed](#an-export-failed)
- [Commands and agents](#commands-and-agents)

## omo does not start

| Message | What to do |
|---|---|
| `omo: /home/you/.local/omo holds the files of omo 0.11 or older. Start omo in a terminal to choose what happens to them, or set OMO_HOME to another folder` | The home belongs to omo 0.11. See [Upgrading from 0.11](upgrading-from-0.11.md). |
| `omo needs git 2.38 or newer (found 2.34)` | Update git. |
| `listen on 127.0.0.1:8090: … address already in use` | Another program (or another omo home) uses the port: `omo config set port 18090`. |
| `omo is already running: http://…` | Not an error: this home's omo runs already; open that address. |
| `omo is already running or starting (use omo stop): …` | Wait a moment, or `omo stop`. |
| `omo failed to start (…); see …/run/omo.log` | `omo -d` could not start the server; the log says why. |
| `invalid start options: …` (`host must be an IP address or localhost`, `basic_auth must be USER:PASSWORD when auth is basic`, …) | Fix the option with `omo config set` or `omo config unset`. |
| `load login token: token is not a valid login token (expected 64 hex characters); run omo config rotate-token to replace it` | Run `omo config rotate-token`. |
| `OMO_HOME must be an absolute path` | Set `OMO_HOME` to a full path. |

## The browser cannot open the board

- **"omo is locked"**: the board has no valid token. Open the full address omo
  printed (it ends with `#` and the token), or paste it into the field. `omo -d`
  prints it again. After `omo config rotate-token` and a restart, the old
  address no longer works.
- **"host or origin rejected"**: you reached omo by a name it does not know.
  Use `omo.localhost` or `127.0.0.1`, or allow the name:
  `omo config set allowed_hosts myhost` (omo prints this command when it
  listens beyond loopback), or set `public_url` for a reverse proxy. Then
  restart omo.
- **"The board could not be loaded"**: omo is not running or not reachable;
  start it again.
- **A notice page instead of the board**: the binary was built without the
  board (`make build NO_WEB=1`; omo prints a warning at start). Build with
  `make build`.

## Nothing starts

Look at the card's badge first:

| Badge | Why | What to do |
|---|---|---|
| none, ticket in Planning | Not approved. | Approve it (drag to Ready For Dev). |
| paused | omo is paused. | Switch **Paused** off in the top bar. |
| waiting for a task slot / story slot | The limits are reached. | Wait, or raise them on Settings → Limits. |
| waiting for #N | A dependency is not done. | Finish or remove the dependency. |
| waiting for usage | The role's models are near their usage limits. | Wait for the reset, add a model to the role, or **Start anyway…** on a ticket with its own model. |
| no usable model | The role has no model that can start. | Settings → Roles; the top of that page says what is missing. |

A task in a story starts only while its story is In Progress, and a story's
tasks in Planning need to be released (by its product manager, or by you when
**Tasks are released by** is **You**). The CEO starts only when you open its
tab.

## A ticket says needs attention

A step that needs a person flags the ticket: the card shows "needs attention",
the panel shows **Needs attention** with the reason, a comment mentions the
supervisor, and the bell lists it. On a ticket that the CEO or a product manager
supervises, the card and the panel say "needs the CEO" or "needs the product
manager" instead, and the bell leaves it out: that supervisor is told and may
retry it, and you can too. Fix the cause, then press the retry button
(**Retry start**, **Retry agent** or **Retry merge**; `omo ai ticket retry N`).

| Reason | Meaning |
|---|---|
| "… exited unexpectedly (exit status 1), failure 3 in a row; omo starts no further agent here until someone retries." | The agent program crashed three times. The comment has its last 20 lines of output. Often a program that is not signed in, a wrong argument in the profile, or a used-up account. |
| "… did not run omo ai ready in time" | The program started but never reached omo, usually because `omo` is not on the `PATH` it sees, or the start prompt did not arrive (see the profile's start-prompt delivery). |
| "… could not start (…)" | The program could not be started: not installed, or not on `PATH`. |
| "… has produced no output for 30m0s, also not in the 15m0s since omo reminded it. …" | The agent went silent. Retry replaces it, or ask it a question if it waits for something. |
| "no usable model for the developer role; set models in roles/developer.yaml" | Settings → Roles. |
| "project … is missing or disabled; …" | Restore the folder or switch the project on (Settings → Projects). |
| "#12 has no base branch in project … (the folder was on a detached HEAD); …" | Set the base branch in the ticket's details. |
| "the folder … already exists and is not a worktree omo recorded for #12; omo leaves it alone. Move or remove it, then retry." | Something else is in omo's worktree folder. |
| "branch … of #12 is checked out in …; omo needs it in its worktree …" | Switch that folder to another branch, then retry. |
| "omo cannot create the branch of #12 in project …: the branch … exists, so git cannot create … below it; rename that branch or change the branch template, then retry." | A branch named like the template's folder exists (`omo` blocks `omo/…`). |
| "question 41 to the user has waited 30m0s for an answer" | Answer the question. |
| "#12 has gone back and forth: its review was rejected 6 times since a person last acted on it. …" | Clarify or change the ticket, then retry, or cancel it. |
| "Export of … failed: …" | See [An export failed](#an-export-failed). |

## A merge is blocked

omo merges into your branch without touching your checkout, unless the branch
is checked out in your project folder. Then:

| Message | What to do |
|---|---|
| "Uncommitted changes to … in … block merging into main. Commit, stash or discard them there, then retry." | Clean those files in your folder, then **Retry merge**. |
| "…: rebase is in progress, which blocks merging into main. Finish or abort it there, then retry." | Finish what git is doing in your folder. |
| "New commits were added to … after #12 was handed in for review …" | Retry merges only the reviewed commit; Reject sends the new commits to review. |
| "git refused to fast-forward … merge.verifySignatures is set …" | Let omo sign its merge commits, or switch the check off. |
| "Merging into main timed out: … (omo never removes an index.lock itself)" | Something holds the repository; close it, then retry. |

A merge conflict is not a flag: the ticket goes back to In Progress and its
developer resolves it.

## An export failed

The ticket stays in Review with "Export of <project> failed: <reason>". Common
causes:

- No remote: "omo cannot export project … : … Give the repository a remote to
  push to (`git -C … remote add origin <address>`) and export again, or take
  the project off the ticket."
- A remote that is a folder on this machine has no forge to open a pull
  request on.
- The push was refused (no access, or a branch of that name exists that omo
  did not push).
- `gh`, `glab` or `tea` is missing or not signed in: the export agent's log
  (Agents tab) shows its output.

Then approve the export again (only the failed projects are tried), choose
**Merge locally instead**, or remove the project from the ticket.

## Commands and agents

| Message | Meaning |
|---|---|
| `omo: omo is not running (start it with: omo)` | `omo ai`, `omo mcp` and `omo config export|import` need the running server of this home. Check `OMO_HOME`. |
| `OMO_AGENT_ID is set but empty: this agent terminal is broken; …` | A shell that pretends to be an agent; unset `OMO_AGENT_ID`. |
| "commit or discard these changes first: …" (an agent's `omo ai done`) | The developer must commit or clean its worktree before handing in. |
| `this MCP connection is not tied to an omo agent session, so it has no omo tools; …` | An MCP client outside omo; see [MCP](mcp.md#the-board-tools). |
| "server … needs a sign-in: connect it in omo's settings (MCP servers)" | Press **Connect** on that server (Settings → MCP). |

When an agent behaves oddly, read its terminal: open the ticket's Agents tab
and type into it like into the program itself.
