# omo ui

`ui/` contains a Tauri 2 application that opens an omo server's board in a
native window. Its package contains only connection/setup assets and branding;
the selected server supplies the board, plugins and forwarded applications.

## Connect

Start the app, choose **An external omo host**, and enter its origin, for
example `http://localhost:8090` or `https://office.example`. Enter the login
token separately, or select username/password or no authentication to match
the server. The address must contain no path, query, fragment or credentials.
The server's normal Host, Origin and TLS checks still apply. Native HTTP calls
send credentials in headers, omit browser Origin headers and refuse redirects.

**Remember credentials** uses the OS credential store: Secret Service on
Linux, Credential Manager on Windows, Keychain on macOS and Keystore-backed
storage on Android. If that store is unavailable, the app reports the failure;
uncheck Remember to connect for this session. Public settings and delivery
cursors are stored separately in Tauri's app configuration directory for
`dev.scolastico.omo.ui`. Desktop board windows use ephemeral browser storage;
the token is supplied to the board's existing `omo.token` mechanism at document
start. Basic authentication uses the native WebView's authentication challenge.

Use **Connection…** in the native menu or the board's Connection button to
change hosts or credentials, reconnect, disconnect or change notifications.
Closing Connection leaves the board open; the menu/button recreates it when needed.
Reconnect preserves notification history for the same origin, auth mode and
username. **Treat as a new server** creates a fresh history when its database
was replaced. Switching connections closes its board/forward windows. Closing
the main desktop board exits the app and leaves the external server running.

## Local omo and closing the window

On Linux, Windows and macOS, choose **Local omo** and Connect. The app first
checks the normal `OMO_HOME` (`~/.local/omo`, `%APPDATA%\omo` on Windows).
If an omo started by autostart, `omo -d` or a terminal already answers its
private control endpoint, the app attaches even when no executable is on its
GUI `PATH`. Closing the main window then exits the UI, leaves that server
running and keeps no tray icon. **Stop an omo server started by this app when
closing its window** never grants ownership of an attached or direct server.

When nothing runs, the app reuses a normal installation from `OMO_INSTALL_DIR`,
the installer's default directory, `PATH` or the saved autostart search path.
If no executable exists, it downloads the matching amd64/arm64 release and
verifies `SHA256SUMS` before installing into `~/.local/bin` or
`%LOCALAPPDATA%\Programs\omo` (overridden by `OMO_INSTALL_DIR`). It adds that
directory to the user's shell profile or Windows user PATH when necessary.
The resulting `omo`, `omo -d`, `omo stop` and autostart commands work without
the app. Existing executable installations are reused and unrelated files are
never overwritten. An older CLI without the native launch interface needs
`omo update`; legacy omo homes must be migrated with the normal CLI first.

The app starts omo through its normal detached service, using the same home,
configuration, database and logs. Only the launch that actually started the
live instance owns it; losing a concurrent start means attaching. Ownership
is held in memory as the instance's random authenticated control capability,
never as a PID or a claim on whichever server runs later. A restart loses
ownership, and closing the UI cannot stop the replacement.

For an owned instance, the default main-window close hides the window and
retains omo with a tray icon. **Open omo** in the tray reopens the window.
Enable **Stop an omo server started by this app when closing its window** to
stop that exact instance and exit instead. Tray **Exit omo and stop local
server**, the app's Exit menu, Disconnect, and an explicit switch to an
external host also stop the owned instance. To switch directly to the same
owned host, disconnect first, then start omo independently. A stop waits for
normal agent handoff/cleanup; failures leave the app and its ownership proof
available for retry. Closing Connection while the board is visible leaves
the board open. Ownership loss removes the tray and restores Connection if
all windows were hidden. If a crash leaves runtime metadata behind, an unlocked
normal service lifecycle proves the old instance is gone and allows close,
Exit or Disconnect to finish. A failed control request while that lock is held
keeps ownership and reports the error for retry.

Android exposes only direct connection. Native validation and the local
runtime refuse local start/install there.

## Notifications and shortcuts

Desktop system notifications are enabled in the connection screen. Capability
exchange uses the [Native UI API](ui-api.md); an older server without that
endpoint still opens, with native polling disabled. Server and app versions do
not have to match. No Android background/foreground notification capability is
advertised by this implementation.

A new connection starts after current server history. Polling runs in native
code every five seconds after catching up, independently of WebView visibility.
Empty pages with more events are drained immediately. Received IDs and the
string cursor are persisted per connection; the browser bell's seen marker is
never changed. If history is unavailable, Connection offers **Replay retained
notifications** or **Notify from now**. A click on a system notification brings
the main board forward and opens its ticket/comment. Old-connection clicks are
ignored. Titles/bodies are bounded plain text; Linux body markup is escaped.
Submission to the system does not guarantee the OS will display a toast, and a
crash between submission and persistence can replay a notification.

While native polling is enabled, the connected board's browser notification API
is suppressed, without changing its saved browser preference. The bell remains
available. Polling errors and history recovery are shown in Connection.

Desktop opened pages stay in native child windows, including blank reservations
used by the board's authenticated proxy Open flow. They retain related WebView
state and the same key settings. There are no browser/developer menu
accelerators. Windows explicitly disables WebView2 browser accelerators; Linux
uses an embedded WebKit view; macOS retains its standard Edit menu for normal
clipboard/undo. No JavaScript keyboard interceptor rewrites page shortcuts.
OS/window-manager shortcuts remain subject to the operating system. Android
has one WebView and opens board/proxy links in that view.

## Build and develop

Install [Tauri's prerequisites](https://v2.tauri.app/start/prerequisites/),
Rust 1.90 or newer, and Node/npm. Linux needs GTK 3 and WebKitGTK 4.1 development
packages; Windows needs the WebView2 runtime and build tools; macOS needs Xcode
command-line tools. Run from `ui/` so its Cargo Android configuration is read:

```sh
cd ui
npm ci
npm run dev
npm run build                       # local installers; no publishing
npm run build -- --debug --no-bundle # compile and embed setup assets only
npm run check                       # focused Rust protocol and bridge tests
cargo fmt --all --check
```

There is no Vite server or board build step for this app. Setup is plain bundled
HTML/CSS/JavaScript. Board development remains the separate `web/` workflow.

For Android, install the SDK, NDK and JDK and set `ANDROID_HOME` and `NDK_HOME`:

```sh
npm run android:init -- --ci
npm run android:dev
npm run android:build
```

The Rust library has the mobile entry point. `tauri android init` generates the
native Gradle project after prerequisites are available; this task does not add
release CI, APK signing or background notification services. The scoped Wry
class extension in `ui/.cargo/config.toml` adds Basic authentication without
replacing its navigation/resource handlers. The JNI package matches the app
identifier, so changing that identifier also requires updating the JNI export.

## Native interfaces

- `ui/core`: `Config` validates modes/origins/auth inputs; `BoardClient` performs
  authenticated native HTTP, feature exchange and incremental polling.
  `Delivery::accept` validates a complete page, persists accepted IDs after
  submissions, then advances its cursor. `Recovery::apply` persists an explicit
  recovery choice. Mobile scheduling can reuse this protocol with its own
  supported feature list and delivery adapter.
- `ui/src-tauri/src/lib.rs`: the bundled setup's commands are `get_setup`,
  `connect({settings})`, `disconnect`, `recover({replay})` and `local_connect`.
  Settings have `mode`, `origin`, `auth`, `username`, `secret`, `notifications`,
  `remember`, `reset_identity` and `stop_with_window` (default false). Results
  contain public configuration, platform,
  connection/polling status, errors and pending recovery, never credentials.
  `local_connect({settings:{notifications,remember,stop_with_window}})` discovers,
  installs, attaches or starts the normal desktop omo. Android rejects it.
- `windows.rs`: remote views, related child windows, the connection menu/button
  and window-close dispatch. `desktop.rs` owns the captured local instance,
  serialized native operations, tray, explicit Exit and conditional shutdown.
- `ui/core/src/local.rs`: normal executable/home discovery, bounded native
  downloads, verified installation and captured-instance control.
  `LocalManager::connect_with_capture` hands its `LocalControl` to the shell
  before fallible board/authentication preparation, so an unsuccessful connection
  still has a native cleanup path. Launch capture files stay outside `OMO_HOME`
  until the normal CLI initializes its layout.
- `omo ui-local start` is a hidden native CLI interface. It emits one JSON object
  with `endpoint`, `token`, `url`, `launch_id`, and `owned`; it emits no human
  startup messages. `owned` is true only for this call's newly ready detached
  child. The endpoint/token are a private per-run capability; do not log or
  expose this response to loaded pages. `launch_id` alone is not proof of ownership.
  Control uses authenticated loopback `GET /status` (204) and `POST /stop` (202),
  rejecting browser Origin/fetch headers and redirects. Shutdown always uses
  the captured endpoint/token, never a newly read replacement capability.
- `platform.rs`, `macos_auth.rs`, `android_auth.rs`: scoped native WebView settings
  and authentication callbacks. `credentials.rs` is the OS secret-store adapter.
- The notification worker and action listener use the Rust notification plugin;
  they do not depend on a JavaScript timer or a visible setup/board page.

Only the bundled `setup` document has the five explicit command permissions.
Board/proxy views have no remote capabilities, shell/process/filesystem plugins,
or arbitrary native HTTP endpoint. Their injected bridge exposes a notification
marker and a configure action, not a general native API.

## Focused native smoke

`ui/tests/native_smoke.py` is an opt-in Linux probe. It needs Python GObject
(including Atspi), dbus-python, python-xlib, Xvfb, xdotool and the built native app.
It uses an isolated X server, configuration/cache/temp roots, a standalone HTTP fixture
and a private D-Bus notification service. Readiness is observed from actual
accessible controls, HTTP events and native notification signals.
It also closes and reopens Connection repeatedly through both the menu and board
button, checking that the active connection and saved address remain available.

```sh
dbus-run-session -- python3 ui/tests/native_smoke.py --auth token
dbus-run-session -- python3 ui/tests/native_smoke.py --auth basic
dbus-run-session -- python3 ui/tests/native_smoke.py --auth none
```

The probe also supports the real embedded board and a verified code-server
publisher, through the existing standalone server fixture (no engine/agents):

```sh
go test -c -o bin/ui-server-fixture ./internal/server
dbus-run-session -- python3 ui/tests/native_smoke.py \
  --board-fixture "$PWD/bin/ui-server-fixture" \
  --publisher /path/to/verified/code-server/bin/code-server
```

The app's token/Basic/none loading, remote IPC denial, native notification
submission and activation, reserved child-window shortcuts, and the actual board → authenticated proxy Open →
code-server 4.145.2 command palette/typing/clipboard flow have been
exercised on Linux x86_64/WebKitGTK 2.54.3 under Xvfb. Notification evidence uses
a D-Bus fixture, not a visible desktop toast. The Windows native shell and core
compile with the GNU cross target; Android
ARM64 core compilation was checked with the cached NDK. Windows runtime/Action
Center, macOS and Android behavior have not been run on their platforms.
WebView2 notification
activation is limited by the plugin to on-screen toasts, not entries already
in Action Center. This host has no macOS SDK; macOS and native Android builds,
and device
permission/background behavior, need native-platform verification.

Implementation references: [Tauri capabilities](https://v2.tauri.app/security/capabilities/),
[WebviewWindowBuilder](https://docs.rs/tauri/2.12.7/tauri/webview/struct.WebviewWindowBuilder.html),
and [notification plugin](https://v2.tauri.app/plugin/notification/).

The local lifecycle probe uses a standalone fixture with an isolated normal
installation/home, private X server and D-Bus tray watcher; it never starts the
user's omo or contacts release servers:

```sh
dbus-run-session -- python3 ui/tests/local_smoke.py --app ui/target/debug/omo-ui
```

It covers attachment with and without an executable, fresh-home startup,
actual window close, tray reopen/Exit, stop-with-window, direct external close,
owned Disconnect, replacement-safe stop, and owned cleanup after authentication
preparation fails. It also terminates an owned control endpoint, joins its
worker while retaining stale metadata, then checks both close settings and
explicit native Exit. `--host <prefix>` can use an extracted Arch dependency tree through a read-only Bubblewrap overlay; this
keeps WebKit helper paths intact without installing system packages.
