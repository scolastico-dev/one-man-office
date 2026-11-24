# MCP

omo serves an MCP endpoint at `/mcp`. It gives the agents omo starts two
things:

- **omo's board tools**: the `omo ai` commands as MCP tools, so an agent can
  read and change its tickets without going through its shell;
- **relayed servers**: other MCP servers you set up once in omo, with your
  credentials kept in omo, offered to the agents under the server's name.

Everything is on **Settings → MCP**, in four parts: **Servers** (with
**General switches**), **Agent programs**, **Take over** and **Access**.

- [The board tools](#the-board-tools)
- [How agents reach omo](#how-agents-reach-omo)
- [Relayed servers](#relayed-servers)
- [Signing in at a relayed server](#signing-in-at-a-relayed-server)
- [Your own sessions of the agent programs](#your-own-sessions-of-the-agent-programs)
- [Taking over servers the programs already have](#taking-over-servers-the-programs-already-have)
- [Programs on other machines](#programs-on-other-machines)
- [Limits](#limits)

## The board tools

Each `omo ai` command is a tool named after its words, with `-` written `_`:
`omo ai ticket show` is `ticket_show`, `omo ai export-text` is `export_text`.
A tool answers with the text the command prints and the server's JSON. Who
sees which tool follows the role: `done` only for product managers,
developers, reviewers and export jobs, `export_text` only for export jobs.

Two commands are no tools: `omo ai wait` (it blocks for a long time) and
`omo ai ticket remove-workdir` (it is yours). Agents run them in their
terminal.

Board tools exist only for a connection that belongs to an agent session omo
started. Any other connection (a program you started yourself, a personal
token, an OAuth grant) gets none of them, only the relayed servers marked
"Also available without an omo session". Board actions belong to an agent and
its ticket; a connection from outside has neither. In your own shell, use
`omo ai` (it acts as you).

## How agents reach omo

**Claude Code and Codex** sessions that omo starts get omo's MCP entry on their
command line: every role, nothing to set up. The entry starts `omo mcp stdio`,
a bridge that takes the session from the agent's environment (`OMO_AGENT_ID`,
`OMO_SOCKET`) and talks to omo over its local socket. It holds no token.

**Antigravity** has no such command-line option. Register omo in its
configuration under **Agent programs** (or `omo mcp install agy`); see
[below](#your-own-sessions-of-the-agent-programs). **Gemini CLI** gets no MCP
entry from omo and cannot be registered.

**Notifications.** An agent that listens receives language-server reports
(see [How agents are told](settings.md#how-agents-are-told)) as MCP
notifications instead of terminal lines. Through the bridge, a client becomes a
listener by sending `logging/setLevel`; a directly connected client opens a
`GET /mcp` stream with `Accept: text/event-stream`. Omo
sends a probe notification with a token over every new stream; the agent
answers it with `omo ai lsp confirm TOKEN` (tool `lsp_confirm`), and only then
does omo count it as a listener. Merely using the tools, or an unanswered
probe, is not listening, and a stream that closes returns the agent to
terminal lines. The tool `lsp_ignore` (`omo ai lsp ignore ID`) silences a wrong report.

**General switches**:

- **Serve omo's MCP endpoint**: off, omo answers no MCP client, starts no
  relayed server and gives its agents no MCP entry; `omo ai` keeps working.
- **Claude Code sessions get omo's MCP entry**, **Codex sessions get omo's MCP
  entry**: whether omo adds the entry when it starts these programs.

All three are on by default (`mcp.enabled`, `mcp.inject.claude`,
`mcp.inject.codex` in `settings.yaml`). In the sidebar's Agents tab, an agent
that used omo's MCP tools carries the chip **MCP**.

Check what the endpoint offers from a terminal:

```bash
omo mcp status
```

It lists the relayed servers with their state, then connects as
`omo mcp stdio` would in that terminal and lists the tools. In your own shell
that is a connection without a session, so it shows no board tools.

## Relayed servers

**Settings → MCP → Servers → Add MCP server…** asks for a name (lower-case
letters, digits and `-`, starting with a letter, at most 24 characters; fixed
once saved) and opens the server's form:

- **omo relays this server**: the server's switch.
- **Kind**:
  - **A program omo starts (stdio)**: **Command** (found through omo's
    `PATH`), **Arguments**, **Environment** (plain values; `${VAR}` and
    `${VAR:-default}` take a value from omo's environment), **Processes** and
    **Working folder**.
  - **An address (Streamable HTTP)** or **An address (SSE, older servers)**:
    **Address** (`http://` or `https://`) and **Authorization** (see
    [Signing in](#signing-in-at-a-relayed-server)).
- **Who gets it**: **Roles** (CEO, Product manager, Developer, Reviewer,
  Export; none chosen: every role) and **Also available without an omo
  session**: also programs started outside omo, personal tokens and OAuth
  grants get this server. Off, only the agent sessions omo starts, of the
  chosen roles, get it.
- **Tools and time limits**: **Only these tools**, **Never these tools**,
  **Start time limit** (default 30 s, at most 10 minutes) and **Tool call time
  limit** (default 10 minutes, at most 24 hours).

**Test** starts the form as it is once, lists the tools and stops it, without
saving. A saved server shows its state (not running, starting, running,
failed, needs login, off), its tools, the tools it left out and why, and has
**Restart**, **Log** and **Delete**. The settings are
`OMO_HOME/mcpservers/<name>.yaml`; values (environment, headers, client
secrets) are stored as you entered them, in plain text. Logs are in
`OMO_HOME/logs/mcp/`.

Agents see a relayed tool as `<server>__<tool>`; its description says that omo
relays it and that its results come from that server.

**Processes** for a stdio server:

- **One process per session, in its folder** (the default): each agent
  session gets its own process, started in the session's worktree, and it ends
  with the session. Connections without a session share one process started in
  your home folder. Use this for servers that work on files.
- **One process for every session**: one process for everybody.

An address (HTTP or SSE) always has one shared connection. **Working folder**,
when set, overrides the folder. Idle processes stop after 15 minutes (per
session) or 10 minutes (shared).

A relayed program runs as you, with omo's environment (without omo's own
`OMO_*` variables) and the environment you gave it. It sees the folders it is
started in and whatever your user can reach.

## Signing in at a relayed server

For an address, **Authorization** is one of:

- **None**.
- **Headers (a token or key)**: **Headers** are sent with every request, for
  example `Authorization: Bearer ${EXAMPLE_TOKEN}`; `${VAR}` takes a value from
  omo's environment. **Headers command** optionally names a program that prints
  a JSON object of headers, run before omo connects (for tokens that change); it
  runs again when the server answers 401.
- **OAuth (sign in with Connect)**: save the server, then press **Connect**.
  The provider's sign-in opens in a new tab; afterwards that tab says
  "Connected to <server>. You can close this tab." Leave **Client id** empty
  and omo registers itself with the provider. A client you registered yourself
  needs omo's return address, `/oauth/callback` on the address you reach omo
  by. omo keeps the sign-in in its database and refreshes it by itself.
  **Disconnect…** forgets it.

A server whose sign-in is missing or was refused shows **needs login**, and its
tools are left out until you press **Connect** again. The credentials of a
program calling omo are never passed on to a relayed server.

The OAuth sign-in at a relayed server works only when the server's sign-in
addresses are https (or on this machine).

- A sign-in belongs to the address, resource and client it was made for. When
  one of them changes, or the server is deleted, omo drops the sign-in (and
  revokes it): press **Connect** again. **Test** on an unsaved form uses the
  stored sign-in only when its address, resource and client match the saved
  server.
- The endpoints an upstream server names (its sign-in metadata, registration,
  token and revocation endpoints) must be public addresses or on the server's
  own host. omo follows a redirect of a relayed server only on the same host
  and never from https to http, and never sends its credentials elsewhere.
- Addresses of relayed servers are shown, logged and put into error texts with
  passwords and query values masked (`https://host/mcp?api_key=***`).

## Your own sessions of the agent programs

**Agent programs** registers omo in the programs' own configuration, so the
Claude Code, Codex or Antigravity sessions you start yourself reach omo's
relayed servers too. omo finds the programs' files: the default one, and one
per account folder that a profile uses.

| Program | File |
|---|---|
| Claude Code | `~/.claude.json`, or `.claude.json` in a profile's account folder |
| Codex | `~/.codex/config.toml`, or `config.toml` in a profile's account folder |
| Antigravity | `~/.gemini/config/mcp_config.json` |

omo changes these files only through each program's own command
(`claude mcp add-json`, `codex mcp add`, `agy mcp add`), adds an entry named
`omo` that runs `omo mcp stdio`, and first keeps a copy of the file in
`OMO_HOME/backups/`. The dialog shows the commands and the entry before and
after. On the command line:

```bash
omo mcp install            # shows what it would change for every program and account
omo mcp install claude     # one program, or one target by the id the MCP page shows
omo mcp install --yes      # applies it
omo mcp uninstall --yes    # removes omo's entry again
```

A session you start yourself has no omo agent session, so it gets the relayed
servers marked "Also available without an omo session" and no board tools.

## Taking over servers the programs already have

**Take over** lists the MCP servers that Claude Code, Codex and Antigravity have
in their user-level files, with values masked. Choose servers, give each a
name in omo and press **Take over**. omo stores its copy first; only then, with
**Remove the taken-over entries from the programs' files** on (the default),
it keeps a copy of each file in `OMO_HOME/backups/` and removes the entries
with the program's own command. Off, the programs keep their entries and show
the tools twice.

- Taken-over servers are created with "Also available without an omo
  session" on, as they were available to every session of the program before.
- A server that signed in with OAuth in the program needs **Connect** in omo
  afterwards; the program's own sign-in stays with the program.
- Project-level entries (a project's `.mcp.json` and the like) and WebSocket
  servers stay in the programs: omo has no per-project servers and does not
  relay WebSocket servers.

## Programs on other machines

**Access** lets a program that is no omo agent session reach `/mcp` over the
network: an agent program on another machine, in a container, or a desktop
app. Such a connection gets only the relayed servers marked "Also available
without an omo session", never board tools. It signs in one of two ways.

**A personal access token**: **Access → Personal access tokens**, or

```bash
omo mcp token create laptop --expires 90d   # prints the secret once
omo mcp token list
omo mcp token revoke <id>
```

omo keeps only a hash: the secret is shown once. The program sends it as a
bearer token; put it in the variable `OMO_MCP_TOKEN` of the program's
environment, never into a shared file. **Access → Connect a program** shows the
commands with your address:

```bash
claude mcp add --transport http -s user omo https://omo.example.com/mcp --header 'Authorization: Bearer ${OMO_MCP_TOKEN}'
codex mcp add omo --url https://omo.example.com/mcp --bearer-token-env-var OMO_MCP_TOKEN
```

**OAuth**: the program registers itself with omo and opens omo's consent page,
"Allow access to omo?", in your browser (signed in to the board). It names the
program, where the answer goes and what it gets. Approve only a sign-in you
just started yourself. A grant ends when you revoke it under **Access → OAuth
grants** or when the program has not used it for 30 days.

```bash
claude mcp add --transport http -s user omo https://omo.example.com/mcp
# then in Claude Code: /mcp, choose omo, Authenticate
codex mcp add omo --url https://omo.example.com/mcp
codex mcp login omo
```

### OAuth and plain HTTP

omo's OAuth addresses are those of the address the program reached omo by. Over
plain `http://` on another machine's address they are not https, and an MCP
client may refuse to sign in there. For OAuth from other machines, give omo an
https address with a reverse proxy and `public_url` (see
[Behind a reverse proxy](running.md#behind-a-reverse-proxy)); a personal access
token works without it. Remember that an omo reachable over the network runs
commands for whoever gets in (see [Security](security.md)).

## Limits

- Only tools are relayed; the resources and prompts of a server are not.
- At most 64 stdio processes run at once; a server has at most 512 tools; a
  tool's result of more than 4 MiB is not passed on: the agent gets an error
  ("server <name> answered with <size> MiB, more than omo relays (4.0 MiB)").
- A server that fails 5 times within 10 minutes stays failed until you restart
  it or change its settings.
- No server per project.
- Registering omo in the programs and taking servers over runs the programs'
  own `mcp add` and `mcp remove` commands; omo's tests use stand-ins for the
  programs, so check the result in the program the first time.
