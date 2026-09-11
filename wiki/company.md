# Company dashboard

`omo company` opens a local web dashboard that launches trusted offices,
shows their live TUIs through embedded xterm.js, and offers interactive shells.

```bash
omo company                          # http://127.0.0.1:8090
omo company -d                       # --detached: start in the background
omo company stop                     # stop and clean up all owned terminals
omo company --max-agents 16          # aggregate cap across launched offices
omo company --listen 127.0.0.1:0     # choose an available port
omo company --usage-cache-ttl 10m    # shared Claude/Codex usage cache freshness
omo company --mock                   # try offices with no model calls
omo company --unsafe                 # disable dashboard token authentication
omo company --basic-auth user:pass   # browser-native Basic auth
omo company --unsafe --no-origin-check # forward-auth reverse proxy
```

## Background operation and autostart

`--detached` (short form `-d`) starts the company in a new background process
and waits for it to be ready before printing the access URL. It accepts the
same dashboard flags as foreground operation. Startup failures, including an
occupied port, return an error to the launching command.

`omo company stop` requests normal shutdown and waits for cleanup of owned
offices and shells. It also works with a foreground company; running it when
nothing is active is harmless. One company may run per `OMO_HOME`, so stop
the current instance before starting another with different settings. Separate
homes can run independent companies on different ports. `Ctrl+C` and, on
Unix, `SIGTERM` use the same shutdown lifecycle.

Register these settings for **the current user's next login**:

```bash
omo company autostart register --listen 127.0.0.1:8090 --max-agents 16 --usage-cache-ttl 5m
omo company autostart unregister
```

`register` accepts the company's dashboard flags after the command name.
It saves their literal values, including defaults and explicit `false` values,
so changed defaults in a newer binary do not change the saved settings. Spaces,
quotes, and shell metacharacters in values are preserved without shell
expansion. It also saves the executable location, working directory, `PATH`,
and resolved `OMO_HOME`. Use a stable installed binary and working directory;
register again if either moves or you want to change the settings. Other
environment variables come from the login session.

Registration replaces this home's previous entry and takes effect at the next
login; it does not start or restart the current company. No `-d` is needed
in the registration command. `unregister` removes that entry and the saved
arguments without stopping a running company. Stopping a company keeps
its registration for the next login and does not cause an immediate restart.

The native per-user mechanisms are:

- Linux: an [XDG desktop autostart entry](https://specifications.freedesktop.org/autostart/latest/) in `$XDG_CONFIG_HOME/autostart` (default `~/.config/autostart`), for graphical login. Executable paths containing `%` are rejected because desktop launchers cannot reliably resolve them.
- macOS: a [LaunchAgent](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html) in `~/Library/LaunchAgents`, with `RunAtLoad` and no automatic restart.
- Windows: a [current-user Run registry value](https://learn.microsoft.com/en-us/windows/win32/setupapi/run-and-runonce-registry-keys) that launches a hidden background process. Registration rejects a launcher command exceeding Windows' 260-character limit; company flag values live in a separate file and do not count toward it.

Runtime files live in `OMO_HOME/company/`. `company.log` contains startup
messages and the access URL, including after an automatic login launch; it is
replaced on each background start. `runtime.json` holds an independent local
stop capability and the dashboard access URL, and is removed after cleanup.
`autostart.json` holds the saved arguments, including any Basic auth password.
The directory and files are restricted to the user on Unix; Windows uses a
protected user/SYSTEM ACL. Keep these files private. Terminal contents are
never written to this directory. Stop authenticates to a separate loopback
control listener and never kills a process based on a stale PID file.

## Dashboard

The dashboard uses a dark, monospace Metro/TUI layout with square, labeled
panels. Purple borders highlight the panel under the pointer or containing
keyboard focus; office and terminal entries shift slightly on hover. The active
terminal keeps its purple selection marker, and running terminals have green
status text. Motion is disabled when your system requests reduced motion.
The sidebar uses the omo snail logo and shows agent capacity, office and terminal
counts. At desktop widths the sidebar stays fixed while the offices and live
terminals lists scroll independently inside their panels. On narrow screens the
page scrolls and each list keeps its 180px cap. The empty state displays a
transparent version of the logo's white artwork. The sidebar stacks above the
terminal workspace on narrow screens.

Open the access URL printed in the terminal. The dashboard lists the offices in
the global `trusted_offices` setting. Use **Edit** to enter ordered project
management mode: each row gets **↑**, **↓**, and confirmation-protected
**Remove** controls, while the normal view contains only the launch button.
Moving a row persists the full order, and the mode remains active while the
dashboard polls for updates.

Add an existing office with **Trust and load**, create with **Create**, or clone
with **Clone**. Create and clone open and select an interactive setup terminal.
The destination must be a clean absolute path with an existing parent. Create
allows an existing non-empty directory when it has no `.omo/omo.yaml`; clone
requires an absent or empty destination. Clone sources accept HTTPS, `ssh://`,
or absolute local repository paths and use your existing Git configuration and
SSH agent. Setup exit 0 trusts the canonical destination. A nonzero exit never
trusts it, keeps the terminal output available, and shows `Setup exited with
status N; inspect the terminal output`. Cloned `.omo` trees containing symlinks
or special files are rejected before setup; other project symlinks are
unaffected. Trust grants the office's configuration and plugins permission to
run commands as you.

Selecting a project asks for confirmation, then starts a child `omo` and
displays its live TUI. A running office is removed from the launchable project
list and appears only in the terminal list below. The child performs the same
interactive release, embedded-asset, and plugin startup checks as a direct
`omo` launch; any update prompt appears in its web terminal before the office
starts.

Running offices publish an in-memory heartbeat snapshot for the dashboard.
`GET /api/state` exposes each instance's `agents`, `tui`, and `actions` fields;
the snapshot is bounded to 256 agents, 128 actions, and UTF-8-safe 256-byte
strings. Agent lifecycle changes and TUI navigation wake the heartbeat, so the
dashboard normally reflects them within about one second. Shells, setup
terminals, and stopped offices expose empty agent/action lists and an empty TUI
state.

The live-terminal sidebar renders an office as a parent row with nested agent
rows. Desktop trees start expanded; narrow layouts start collapsed. Selecting
an agent selects the office and sends `POST /api/instances/{id}/tui` with
`{"agent":"<name>"}`. Selecting the office while it is in peek sends the same
route with `{"agent":""}` to return to overview. The route forwards the
authenticated user request as socket `tui.show`; local TUI navigation reports
back through the heartbeat, keeping the dashboard highlight and the terminal's
peek view synchronized. Non-running or unavailable offices reject TUI commands
with `409`.

The accessible **Triggers** menu lists only manual actions whose roles include
`user`; role-restricted actions never become browser controls. Keyboard users
can open the menu, move with the arrow keys, activate with Enter or Space, and
return focus with Escape. Actions marked `manual_args` prompt for arguments;
the entry uses shell-like whitespace splitting with single/double quotes and
backslash escapes. Cancelled, malformed, or rejected requests stay in the
status line, while an accepted trigger reports its request ID. Trigger commands
are available only for a selected running office.

**Open shell** starts an independent interactive `sh` on Unix or `cmd.exe` on
Windows, initially in that trusted project. **Open home shell** starts the same
terminal in the company user's home directory and does not require an office
or trust entry. The sidebar switches among live office and shell terminals.
Closing the browser keeps them running. **Estop** asks the office over its
socket to stop and clean up agents; **Force kill** terminates its owned process
tree. Exited terminals can be removed from the list. Up to 64 terminals and 16
browser terminal connections may be retained at once.

Paste text with **Ctrl+Shift+V** (or **Cmd+V** on macOS), or use the browser's
right-click **Paste** action. **Ctrl+V remains a terminal control key.** Large
pastes are sent in 16 KiB chunks, with each chunk acknowledged after the PTY
write completes, so pasting beyond the 64 KiB WebSocket message limit does not
disconnect the terminal. Unicode, line endings, and bracketed-paste handling
continue to pass through xterm. Subsequent typing stays behind queued paste
bytes. The browser allows up to 4 MiB of pending UTF-8 input (and 1,024 queued
input events); an input that exceeds the buffer is rejected whole with a
notice, leaving the terminal connected. Pending input is discarded on disconnect
and is never replayed automatically after reconnecting.

Global plugins can extend the page and run company lifecycle hooks. Plugin
authors should use the complete [company plugin API](plugins.md#company-lifecycle).
Browser hooks register with `window.omo.onLoad(pluginName, listener)` and are
called only for that plugin's `omo:company_load` event.

### Company plugin assets and served links

The company keeps a persistent overlay at `OMO_HOME/company/http`, owned by the
trusted company user. Unix permissions are `0700`; Windows uses a protected
current-user/SYSTEM ACL. Authenticated `GET` and `HEAD` requests check this
overlay before every dashboard route, including `/`, `/assets/`, and
`/plugins/`, so overlay files can override dashboard and plugin assets. Regular
files stream from disk, including files reached through external symlinks.
Traversal, dangling links, directories, and other non-files are rejected, and
non-`GET` routes are left to their normal handlers. The company creates and
secures the overlay but does not delete it or unrelated files during startup or
shutdown.

Global plugins declare browser assets through `company_load`; exact files,
directories, and globs are validated when the plugin loads and resolved from
immutable runtime snapshots at request time. Symlinks and snapshot escapes are
rejected, and directory exports expose regular files without directory
listings. Scripts and `omo:company_load` events run sequentially in
dependency-first order, preserving manifest order within each plugin. The
event contains the plugin name and a frozen resolved configuration snapshot.
`window.omo.trigger(null, action, args)` runs the bound global manual hook
synchronously and returns its request ID and result. `trigger(instanceID,
action, args)` forwards through the authenticated office socket, returns its
request ID after admission, and lets the office hook complete asynchronously.
Both paths enforce the caller role and per-plugin admission guard, write
request/completion/failure audits without argument contents, and return
authorization, validation, admission, or transport errors at the boundary.

The bundled filebrowser creates served download links below
`OMO_HOME/company/http/filebrowser/<id>/`. The authenticated overlay streams
the target without browser buffering or copying on POSIX. Filebrowser startup
and shutdown sweep only that subtree, removing links without touching their
targets; the root and unrelated overlay files persist. Windows uses hard-link,
symlink, then copy fallback for served links. Its POSIX and Windows listing,
search, mkdir, hidden-file, upload, and navigation adapters are injection-safe;
UNC paths are rejected. Where CI cannot run Windows, the PowerShell adapter
probe and scripts receive a manual Windows check before release.

## Aggregate agent capacity

`--max-agents` defaults to 12 and counts only product managers, developers, and
freelancers across all offices launched by this company process. CEOs, reviewers,
smoke alarms, firefighters, and branch namers remain controlled and supervised
but are exempt from the aggregate cap and never wait for aggregate capacity.
Each counted agent process holds a lease until it exits; a dead office releases
all remaining leases. Per-office developer/freelancer limits still apply.
Pending reviews take priority over queued jobs, and AI branch naming can finish
while a counted developer waits for capacity. Shells do not consume agent
capacity. Independent `omo` processes and other companies keep their own
limits.

## Shared usage checks

Children share one coalescing Claude/Codex usage cache by credential scope.
`--usage-cache-ttl` controls that freshness interval; child refresh requests
respect it. Credentials are read by the parent and never sent through the
dashboard. Registered profile definitions are fixed for a child's lifetime;
`omo reload` rejects changed provider/credential identities and added or
removed profile names until the child is restarted. Other configuration changes
still reload. `usage.enabled: false` still disables provider checks for that
office.

On macOS, supervised Claude profiles require absolute non-empty
`CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR` overrides, because
resolving a relative directory could select a different Keychain account. An
explicitly empty secure-storage override keeps its default-account meaning.

Children authenticate to a separate loopback listener using unique ephemeral
tokens. Loss of that channel fails closed: no further agent spawns or local
provider fallback, and a watchdog requests office/shell cleanup. Ctrl+C in the
company stops its children, with forced cleanup after a bounded grace. On
Unix, forced cleanup snapshots descendant processes; deliberately daemonized or
reparented commands are outside that containment. Windows uses kill-on-close
Job Objects. This is a local terminal manager, not a process sandbox.

## Security model

There are no accounts or login screen. The random access key in the printed URL
grants command execution with your permissions. It is removed from browser
history immediately and held only in page memory; use the original URL after a
reload. Keep it private.

The server enforces Host/Origin and capability checks, binds loopback by
default, and warns when bound elsewhere. For remote access, keep loopback
binding and use an SSH port-forward with the same local and remote port, for
example `ssh -L 8090:127.0.0.1:8090 host`. Plain HTTP exposure is not secure;
forwarding-header-based reverse proxies are not supported.

`--unsafe` disables only the dashboard capability check and prints a plain URL;
Host and Origin validation remain enabled. Anyone who can reach the listener
can then execute commands with the company user's permissions. Use it only
behind access control you operate and trust, never as a substitute for
authentication.

`--basic-auth USER:PASSWORD` replaces the random access key with browser-native
HTTP Basic authentication. The dashboard and all of its assets and API routes
require those credentials. Basic auth sends reusable credentials with requests
and the company serves plain HTTP, so this mode is intended only for networks
you trust. **It is not a secure option for exposing the company directly to
the internet.** Do not put the password in shared shell history or process-list
captures. `--basic-auth` and `--unsafe` cannot be combined.

`--no-origin-check` disables the Origin/Sec-Fetch-Site comparison for reverse
proxies whose public origin differs from the company listener. Host checking
remains enabled, so configure the proxy to pass the company's expected Host.
Use this only behind a trusted reverse proxy with forward authentication (usually
with `--unsafe`); direct clients that can bypass that proxy would otherwise have
the company user's command permissions.

Browser terminals use at most 256 KiB of replay per instance in server memory.
Office terminals use the TUI's alternate screen without browser scrollback;
shell terminals retain 2,000 lines of browser scrollback. The company dashboard never writes terminal
contents or input to disk. Its private lifecycle files contain the local stop
capability and access URL described above; child offices keep their normal
`.omo/logs` behavior. Assets are embedded (`@xterm/xterm` 6.0.0 and
`@xterm/addon-fit` 0.11.0); no CDN or Node.js runtime is required.
