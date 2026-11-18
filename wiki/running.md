# Running omo

One omo server runs per user and omo home. This page covers starting and
stopping it, who can reach it, and keeping it up to date.

- [Starting and stopping](#starting-and-stopping)
- [Start options](#start-options)
- [Sign-in: the three modes](#sign-in-the-three-modes)
- [Reaching omo from another machine](#reaching-omo-from-another-machine)
- [Behind a reverse proxy](#behind-a-reverse-proxy)
- [Start at login](#start-at-login)
- [Updating](#updating)
- [Docker](#docker)

## Starting and stopping

```bash
omo        # in the foreground; Ctrl+C stops it
omo -d     # in the background; prints the address when the server is ready
omo stop   # stops the running server
```

Both print the address with the login token:

```text
omo: http://omo.localhost:8090/#4f01f6c3…
also: http://127.0.0.1:8090/#4f01f6c3…
```

`omo -d` adds where the log goes (`OMO_HOME/run/omo.log`) and
`Stop with: omo stop`. Starting omo again while it runs only prints
`omo is already running: <address>`, so `omo -d` is also how to get the address
back. Browsers resolve every `*.localhost` name to this machine, so
`omo.localhost` needs no setup; the `127.0.0.1` address is the same server.

Stopping asks every working agent for a handoff comment and waits for them
(`asking agents for a handoff (up to 2m0s)...`), then ends all agents. A second
`omo stop`, or a second Ctrl+C, skips the wait. The time is **Settings →
Limits** (`stop.handoff_timeout`). At the next start every ticket that was in
work gets a fresh agent that reads the handoff (see
[Restarts](concepts.md#restarts)).

At its first start in a home that holds omo 0.11's files, omo asks what to do
with them instead of starting (see [Upgrading from 0.11](upgrading-from-0.11.md)).
An interrupted start of a ticket (omo crashed half way) is continued at the next
start.

omo needs git 2.38 or newer; an older git stops the start with
`omo needs git 2.38 or newer (found 2.34)`.

## Start options

The start options live in `OMO_HOME/server.yaml`. Change them with
`omo config`; a running omo applies them at its next start
(`A running omo applies this at the next start.`).

```bash
omo config list              # every option; basic_auth is shown as ***
omo config get port
omo config set port 18090
omo config unset port        # back to the default
omo config rotate-token      # a new login token (the running omo keeps the old one until it restarts)
```

| Option | Default | Meaning |
|---|---|---|
| `host` | `127.0.0.1` | The address omo listens on: an IP address or `localhost`. `0.0.0.0` listens on every interface. |
| `port` | `8090` | The TCP port. |
| `auth` | `token` | `token`, `basic` or `none` (see below). |
| `basic_auth` | empty | `user:password` for `auth: basic`. |
| `origin_check` | `true` | Refuse browser requests from other sites. Turn it off only behind a proxy that does its own access control. |
| `allowed_hosts` | empty | Extra host names the browser may use to reach omo, comma-separated, each with an optional port: `omo.lan,omo.lan:8443`. |
| `public_url` | empty | The address omo advertises, such as `https://omo.example.com/`. |
| `proxy_base_host` | empty (disabled) | Bare DNS base for Web UI forwards, for example `apps.localhost` or `apps.example.com`. Configure wildcard DNS to omo's existing listener. |
| `proxy_label_prefix` | empty | Forced prefix before the forward id, within its single DNS label. |
| `proxy_label_suffix` | empty | Forced suffix after the forward id, within its single DNS label. |

The local socket that `omo ai` and agents use is separate from these options:
it lives in `OMO_HOME/run/` and only processes of your user reach it.

## Sign-in: the three modes

| `auth` | How the browser signs in |
|---|---|
| `token` | The address carries the login token after `#`. The board keeps it in that browser and sends it with every request; the token never goes to the server in an address. Lost it? `omo -d` prints it again, or read `OMO_HOME/token`. |
| `basic` | The browser asks for user name and password (`basic_auth`). omo prints `WARNING: HTTP Basic authentication is intended only for networks you trust. It is not secure for exposing omo directly to the internet.` |
| `none` | No sign-in. omo prints `WARNING: omo token authentication is disabled. Anyone who can reach this address can run commands with your user permissions.` |

Whoever can use the board can run commands as you: agents run as your user,
and the board opens shells. Keep `token` unless something else in front of omo
decides who gets in. See [Security](security.md).

When the board's token is missing or wrong, the board shows "omo is locked":
open the address omo printed, or paste it (or the token) into the field and
press **Unlock**.

## Reaching omo from another machine

The safe way is an SSH port forward with the same port on both sides; omo
keeps listening on loopback:

```bash
ssh -L 8090:127.0.0.1:8090 you@server
```

Then open the address omo printed on the server in your local browser.

To listen on the network instead, set `host` (for example
`omo config set host 0.0.0.0`). omo then prints:

```text
WARNING: omo is exposed beyond loopback over plain HTTP. Anyone with its access URL can run commands with your user permissions. Prefer loopback with an SSH port-forward using the same local and remote port.
```

The board answers only to the host names it knows: `omo.localhost`,
`localhost`, IP addresses, the entries of `allowed_hosts` and the host of
`public_url`. Any other name is refused ("host or origin rejected"), which
keeps other web pages from reaching omo through DNS tricks. omo prints the
command to allow the machine's own name:

```text
To open omo as http://myhost:8090, run: omo config set allowed_hosts myhost
```

Over plain HTTP on a name other than `localhost` the browser treats the page
as not secure: copying from a terminal then works only during a click or key
press (see [Terminals](terminals.md#copy-and-paste)), and MCP clients may refuse
to sign in with OAuth (see [MCP](mcp.md#oauth-and-plain-http)).

## Behind a reverse proxy

omo speaks plain HTTP. To give it an https address, put a proxy that holds the
certificate in front of it and tell omo the address:

```bash
omo config set public_url https://omo.example.com/
```

`public_url` is printed first at the start, shown on **Settings → General**,
and used in links to the board (plugins, notifications). It does not change
where omo listens. omo accepts exactly the host and port of that address, and
an `https` Origin for it. It must be an absolute `http://` or `https://`
address without path, query, fragment or user name: omo cannot be served below
a path prefix.

The proxy must:

- pass the `Host` header through unchanged;
- pass websocket upgrades (the terminals use them);
- not buffer `/api/events`, the board's live event stream (omo sends
  `X-Accel-Buffering: no` for nginx).

Keep `host` at `127.0.0.1` when the proxy runs on the same machine.

`origin_check: false` turns the Origin check off and prints
`WARNING: omo Origin verification is disabled. Use this only behind a trusted reverse proxy that enforces access control.`
With `public_url` set it is not needed.

## Start at login

```bash
omo autostart register     # omo autostart registered for next login.
omo autostart unregister   # omo autostart unregistered.
```

`register` records the current `OMO_HOME`, `PATH` and folder in
`OMO_HOME/run/autostart.json` and adds a login entry: an XDG autostart file
(in `~/.config/autostart/`) on Linux, a LaunchAgent
(in `~/Library/LaunchAgents/`) on macOS, and a Run entry of your user on
Windows. At login omo starts in the background with that environment, so run
`register` from a shell whose `PATH` finds the agent programs. On Linux the
entry needs a desktop session that runs XDG autostart entries; a headless
server needs its own service manager (run `omo` from it).

`unregister` removes the entry and does not stop a running omo.

## Updating

```bash
omo update --check            # only says whether a newer release exists
omo update                    # download, verify and replace the binary
omo update --version TAG      # install exactly this release, also an older one
```

`omo update` fetches the release for your system from GitHub, checks it
against the release's `SHA256SUMS` and replaces the `omo` binary it was started
from. It does not restart a running server: it says "omo is running with the
version it started with: restart it (omo stop, then omo) to use the new one."
Run `omo stop` and start it again. Running the installer again does the same.
Updating an omo that still has the files of 0.11 is covered in
[Upgrading from 0.11](upgrading-from-0.11.md).

After an update, compare edited usage scripts with the new defaults (see
[Usage scripts](agents.md#usage-scripts)): omo never overwrites a script you
changed.

## Docker

The repository has a `Dockerfile` and a `compose.yml`. The image runs omo as
a user `omo` (its uid and gid from `OMO_UID` and `OMO_GID`, default 1000), with
your repositories mounted at `/workspace` and the user's home (agent logins
and `~/.local/omo`) persisted in `./docker-data/home`. At each start the
container installs the agent programs named in `OMO_AGENT_CLIS` (default
`claude,codex,agy,gemini`) that are missing, runs the optional `INIT_SCRIPT` or
`INIT_SCRIPT_PATH`, sets `host` to `0.0.0.0` inside the container and starts
omo on port 8090. The compose file publishes it on `127.0.0.1` of the host only
and adds a separate Docker-in-Docker service for agents that need Docker.

```bash
OMO_UID=$(id -u) OMO_GID=$(id -g) OMO_WORKSPACE=~/code docker compose up -d
docker compose logs omo        # the address with the login token
```

When the persisted home still holds the files of omo 0.11, run
`docker compose run --rm omo` once, choose, stop it, then `docker compose up -d`
(see [Upgrading from 0.11](upgrading-from-0.11.md#docker)). Without a terminal
(`docker compose up -d` before the choice) omo explains and exits.

Inside the container your repositories are below `/workspace`: add that folder
on **Settings → Projects**.

Sign the agent programs in once inside the container
(`docker compose exec -u omo omo bash -l`, then `claude`, `codex` and so on);
their logins stay in the persisted home. Because omo listens on `0.0.0.0`
inside the container, it prints the warning about plain HTTP; the port
mapping is what keeps it on the host's loopback.


## Optional Web UI subdomains

The [Web UI forwards guide](proxy.md) covers registration, app bases,
fallback limits, sign-in and [proxy streaming deployment](proxy.md#behind-an-https-reverse-proxy).

By default a forward uses `/proxy/<id>/`. Set `proxy_base_host` to serve each
forward at `<prefix><id><suffix>.<base>` and have Open prefer that address.
The path URL and its advertised `base_path` remain available.

```sh
omo config set proxy_base_host apps.localhost
omo config set proxy_label_prefix dev-
omo config set -- proxy_label_suffix -user123
```

After restarting omo, a forward opens as
`http://dev-<id>-user123.apps.localhost:8090/start-path`. Browsers resolve
`*.localhost` to the local machine; for remote access use a DNS base with a
wildcard record pointing to the omo host. The base without the forward label
serves the board. No extra port is opened. When using an HTTPS reverse proxy,
route the base and wildcard hosts to omo and supply a matching certificate;
set `public_url` to the external board address so Open uses its scheme and port.
The board may use a different host, such as `https://board.example.com/`.
Set a canonical `public_url` for reliable Back to board links and sign-in
storage. A base used as a board address behind HTTPS also needs `public_url`
or an `allowed_hosts` entry for board Origin checks. Use a base that accepts
Domain cookies, not a public suffix. Subdomains do not isolate trusted apps
from shared Domain cookies; see [Security](security.md#web-ui-forwards).

The base must contain at least two DNS labels, without scheme, port, path,
trailing dot, IP literal or wildcard. Prefix + 32-character hexadecimal id +
suffix must form one DNS label of at most 63 characters (letters, digits,
hyphens; no hyphen at either end); the whole hostname cannot exceed 253
characters. All three settings match without case sensitivity. An explicit
board alias in `allowed_hosts` or `public_url` must not match a composed forward
host; other exact board aliases retain their behavior, including below the
base. No other wildcard host acceptance is enabled.
Stage prefix/suffix before enabling the base if desired; unset
`proxy_base_host` to disable subdomains again.

Open exchanges its one-use grant on the forward host and sets HttpOnly cookies
for the base and all its forward subdomains. If the board is outside that cookie
domain, its authenticated Open response also sets a valid host-local proxy
session for path URLs. Upstream apps receive neither omo's cookies nor its
recognized credentials. Basic and none use the board's normal sign-in rules.
