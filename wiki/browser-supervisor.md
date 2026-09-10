# Browser supervisor

`omo supervisor` opens a local web dashboard that launches trusted offices,
shows their live TUIs through embedded xterm.js, and offers interactive shells.

```bash
omo supervisor                          # http://127.0.0.1:8090
omo supervisor -d                       # --detached: start in the background
omo supervisor stop                     # stop and clean up all owned terminals
omo supervisor --max-agents 16          # aggregate cap across launched offices
omo supervisor --listen 127.0.0.1:0     # choose an available port
omo supervisor --usage-cache-ttl 10m    # shared Claude/Codex usage cache freshness
omo supervisor --mock                   # try offices with no model calls
omo supervisor --unsafe                 # disable dashboard token authentication
omo supervisor --basic-auth user:pass   # browser-native Basic auth
omo supervisor --unsafe --no-origin-check # forward-auth reverse proxy
```

## Background operation and autostart

`--detached` (short form `-d`) starts the supervisor in a new background process
and waits for it to be ready before printing the access URL. It accepts the
same dashboard flags as foreground operation. Startup failures, including an
occupied port, return an error to the launching command.

`omo supervisor stop` requests normal shutdown and waits for cleanup of owned
offices and shells. It also works with a foreground supervisor; running it when
nothing is active is harmless. One supervisor may run per `OMO_HOME`, so stop
the current instance before starting another with different settings. Separate
homes can run independent supervisors on different ports. `Ctrl+C` and, on
Unix, `SIGTERM` use the same shutdown lifecycle.

Register these settings for **the current user's next login**:

```bash
omo supervisor autostart register --listen 127.0.0.1:8090 --max-agents 16 --usage-cache-ttl 5m
omo supervisor autostart unregister
```

`register` accepts the supervisor's dashboard flags after the command name.
It saves their literal values, including defaults and explicit `false` values,
so changed defaults in a newer binary do not change the saved settings. Spaces,
quotes, and shell metacharacters in values are preserved without shell
expansion. It also saves the executable location, working directory, `PATH`,
and resolved `OMO_HOME`. Use a stable installed binary and working directory;
register again if either moves or you want to change the settings. Other
environment variables come from the login session.

Registration replaces this home's previous entry and takes effect at the next
login; it does not start or restart the current supervisor. No `-d` is needed
in the registration command. `unregister` removes that entry and the saved
arguments without stopping a running supervisor. Stopping a supervisor keeps
its registration for the next login and does not cause an immediate restart.

The native per-user mechanisms are:

- Linux: an [XDG desktop autostart entry](https://specifications.freedesktop.org/autostart/latest/) in `$XDG_CONFIG_HOME/autostart` (default `~/.config/autostart`), for graphical login. Executable paths containing `%` are rejected because desktop launchers cannot reliably resolve them.
- macOS: a [LaunchAgent](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html) in `~/Library/LaunchAgents`, with `RunAtLoad` and no automatic restart.
- Windows: a [current-user Run registry value](https://learn.microsoft.com/en-us/windows/win32/setupapi/run-and-runonce-registry-keys) that launches a hidden background process. Registration rejects a launcher command exceeding Windows' 260-character limit; supervisor flag values live in a separate file and do not count toward it.

Runtime files live in `OMO_HOME/supervisor/`. `supervisor.log` contains startup
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
The sidebar shows agent capacity, office and terminal counts, and stacks above
the terminal workspace on narrow screens.

Open the access URL printed in the terminal. The dashboard lists the offices in
the global `trusted_offices` setting. Add an existing office with **Load and
trust**, create a new office in a new absolute directory, or clone a Git
repository and scaffold it. Clone sources accept HTTPS, `ssh://`, or absolute
local repository paths; authentication uses your existing Git configuration and
SSH agent. Destination parents must already exist. Failed creation leaves the
new directory for inspection. Cloned `.omo` trees containing symlinks or
special files are rejected before setup, preventing writes outside that
directory; other project symlinks are unaffected. Trust grants the office's
configuration and plugins permission to run commands as you.

Selecting a project asks for confirmation, then starts a child `omo` and
displays its live TUI. A running office is removed from the launchable project
list and appears only in the terminal list below. The child performs the same
interactive release, embedded-asset, and plugin startup checks as a direct
`omo` launch; any update prompt appears in its web terminal before the office
starts.

**Open shell** starts an independent interactive `sh` on Unix or `cmd.exe` on
Windows, initially in that trusted project. **Open home shell** starts the same
terminal in the supervisor user's home directory and does not require an office
or trust entry. The sidebar switches among live office and shell terminals.
Closing the browser keeps them running. **Estop** asks the office over its
socket to stop and clean up agents; **Force kill** terminates its owned process
tree. Exited terminals can be removed from the list. Up to 64 terminals and 16
browser terminal connections may be retained at once.

Global plugins can extend the page and run supervisor lifecycle hooks. Plugin
authors should use the complete [supervisor plugin API](plugins.md#supervisor-lifecycle).

## Aggregate agent capacity

`--max-agents` defaults to 12 and includes every role: CEOs, reviewers, safety
agents, and branch namers. Each agent process holds a lease until it exits; a
dead office releases all remaining leases. Per-office developer/freelancer
limits still apply. If capacity is full, further spawns are pending, including
an office's initial CEO. Pending reviews take priority over queued jobs. Under
capacity pressure, a completed developer may be stopped to make room for its
reviewer; its branch and worktree remain intact, and rejected work resumes with
a fresh developer and the saved findings. Shells do not consume agent capacity.
The cap covers offices launched by this supervisor process; independent `omo`
processes and other supervisors keep their own limits.

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
supervisor stops its children, with forced cleanup after a bounded grace. On
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
can then execute commands with the supervisor user's permissions. Use it only
behind access control you operate and trust, never as a substitute for
authentication.

`--basic-auth USER:PASSWORD` replaces the random access key with browser-native
HTTP Basic authentication. The dashboard and all of its assets and API routes
require those credentials. Basic auth sends reusable credentials with requests
and the supervisor serves plain HTTP, so this mode is intended only for networks
you trust. **It is not a secure option for exposing the supervisor directly to
the internet.** Do not put the password in shared shell history or process-list
captures. `--basic-auth` and `--unsafe` cannot be combined.

`--no-origin-check` disables the Origin/Sec-Fetch-Site comparison for reverse
proxies whose public origin differs from the supervisor listener. Host checking
remains enabled, so configure the proxy to pass the supervisor's expected Host.
Use this only behind a trusted reverse proxy with forward authentication (usually
with `--unsafe`); direct clients that can bypass that proxy would otherwise have
the supervisor user's command permissions.

Browser terminals use at most 256 KiB of replay per instance in server memory
and 2,000 lines of browser scrollback. The web supervisor never writes terminal
contents or input to disk. Its private lifecycle files contain the local stop
capability and access URL described above; child offices keep their normal
`.omo/logs` behavior. Assets are embedded (`@xterm/xterm` 6.0.0 and
`@xterm/addon-fit` 0.11.0); no CDN or Node.js runtime is required.
