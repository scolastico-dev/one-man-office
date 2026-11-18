# Security

omo runs AI agents that execute commands on your computer. This page says in
plain words what that means and what omo does and does not protect.

## Agents run as you

Every agent is a program running as your operating system user, with your
files, your SSH keys, your git credentials and your network. The profiles omo
writes start the agent programs with their permission prompts switched off
(`--dangerously-skip-permissions`, `--yolo` and the like), so an agent runs
commands without asking. Anything an agent can do, you could have done.

omo's own rules (which agent may move or change which ticket, which settings an
agent may read) keep a well-behaved agent on track. They are not a wall: an
agent can talk to omo's socket without saying who it is, which makes it act as
you, and it can edit the files in the omo home. Do not run an agent you do not
trust, and do not let agents work on something you cannot afford to lose
without a backup.

What omo does keep apart:

- omo writes nothing into your projects and changes your project folders only
  with `git merge --ff-only`; agents work in their own worktrees.
- omo pushes only the reviewed commit of an exported ticket, never with force,
  only to the ticket's own branch.
- Text from agents and other people is shown with hidden and control
  characters written out; text pasted into terminals is cleaned first.
- Uploaded images are checked by their content, SVG is refused, and image
  addresses never carry the login token.

To limit what agents can reach, run omo itself somewhere limited: as a separate
user, in a virtual machine, or in the [Docker image](running.md#docker).

## What the login token protects

Whoever can use the board can run commands as you: open a shell, type into an
agent, change a profile's command. The login token (or Basic sign-in) is what
keeps other people and other web pages out.

- By default omo listens on `127.0.0.1` only and requires the token.
- The token is in the address after `#`, which browsers do not send to
  servers; the board keeps it in that browser. Treat the address like a
  password. `omo config rotate-token` replaces it.
- omo answers only to the host names it knows and refuses requests from other
  sites, so a web page you visit cannot reach it through your browser.
- Over plain HTTP on a network, anyone who can read your traffic can read the
  token. Use an SSH port forward or a reverse proxy with https (see
  [Running omo](running.md#reaching-omo-from-another-machine)).
- `auth: none` removes all of this; use it only when something in front of omo
  decides who gets in.

The local socket that `omo ai` uses is reachable by processes of your user
only, without a token.

## Web UI forwards

See [Web UI forwards](proxy.md) for commands, app configuration and HTTPS setup.

Registered web UIs run through omo's existing browser address under
`/proxy/<id>/`. With token sign-in, open a forward through the board's Open API
(`POST /api/proxy/forwards/<id>/open`): its one-use sign-in grant expires after
60 seconds. The sign-in page clears it from the address before exchanging it
for an HttpOnly cookie, then replaces the page with the forward's start path.
The proxy session lasts 12 hours; restarting omo requires opening it again.
With optional [subdomain routing](running.md#optional-web-ui-subdomains), the
proxy cookie covers the configured base and forward subdomains. If the board
uses an unrelated host, authenticated Open also sets a host-local proxy session
there so path URLs remain usable. Forward subdomains accept only the configured
prefix/id/suffix composition on the listener or external public port; the
board's Origin checks remain unchanged.
Direct addresses without a valid session link back to the board. Basic sign-in
checks the board credentials on each request; `auth: none` requires no sign-in.

omo removes its own cookies and recognized credentials before sending requests
to the app. Forwarded apps are trusted code: in path mode they share the board's
origin and can access its browser storage. Only register apps you trust. The
app controls its own response headers and cross-origin policy; the board keeps
its own checks. In path mode, `Service-Worker-Allowed` can let an app install
a service worker covering the board's origin, and `Clear-Site-Data` can clear
origin data. omo passes these headers through; these origin effects can
persist after removal of a forward. omo itself installs no service worker.
Its generated proxy error/sign-in pages use `no-referrer` and `no-store` so
their board links avoid the app Referer fallback.
Subdomains also carry trusted apps: upstream Domain cookies
may cover the base, and JavaScript can plant duplicate reserved cookies, which fail closed.
Choose a base that supports Domain cookies (not a public suffix). Use a
canonical board `public_url` so Back to board reaches the origin where you
signed in; a different base has separate browser token storage. A base used
for board access behind HTTPS needs `public_url` or `allowed_hosts` for the
board's existing Origin checks.

Removal of a forward or a ticket reaching Done/cancelled blocks new proxied
requests. It does not forcibly close existing WebSocket, SSE or other streaming
connections; those may continue until the app or client closes them.

Use an HTTPS reverse proxy with `public_url` set to its external
HTTPS address so proxy cookies are Secure and Open uses the right host and port.

## Plugins, scripts and relayed servers

All of these are code that runs as you:

- **Plugins**: a command hook is any program; a Lua hook runs in a sandbox but
  can start programs with `omo.exec` and reach the network with `omo.http`.
  A plugin that calls `omo ai` acts as you. Install only plugins you trust;
  the tags on the Plugins page say who looked at a plugin, not that it is
  safe.
- **Usage scripts** read your accounts' credentials and can send what they
  read to any https host.
- **Relayed MCP servers** run as you (programs) or receive what agents send
  them (addresses), with the credentials you gave omo. A server marked "Also
  available without an omo session" is reachable by everyone who has a
  personal token or OAuth grant for omo's MCP endpoint.
- **Skill sets** and **prompts** are instructions to agents: whoever can change
  them steers what your agents do.
- **Environment** values with backticks run commands when an agent starts.

## Secrets

- Profiles, the agent environment, plugin configurations and relayed MCP
  servers store their values as you entered them, in plain files in the omo
  home, which only your user can read. Agents can read them too. Use a
  backtick command (`` `pass show …` ``) in the agent environment to keep a key
  in your password manager instead.
- A settings export contains these values: keep the zip private.
- omo replaces configuration values of plugins and secrets of usage scripts by
  `[redacted]` in logs and error texts. It never stores, logs or shows the
  tokens a usage script reads.
- Personal MCP tokens are stored as hashes only; the secret is shown once.
