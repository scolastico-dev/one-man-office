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

The desktop setup also offers Local omo as an extension point. This build
reports that local installation/lifecycle is unavailable. Android exposes only
direct connection, and native validation refuses local mode too.

## Notifications and shortcuts

Desktop system notifications are enabled in the connection screen. Capability
exchange uses the [Native UI API](ui-api.md); an older server without that
endpoint still opens, with native polling disabled. Server and app versions do
not have to match. Android uses the same feed and system-notification feature flags.

A new connection starts after current server history. On desktop, polling runs in native code every five seconds after catching up,
independently of WebView visibility. Android scheduling is described below.
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

## Android

Android connects only to an external omo instance and loads its existing mobile
board. The bundled Connection screen offers **System notifications** and two
modes; authenticated unattended polling requires **Remember credentials**.
Turning notifications off still opens the board. Older servers without the
notification feed still open with native polling disabled.

- **Background** installs one persistent WorkManager periodic job with a
  network-connected constraint and the minimum 15-minute repeat interval.
  Android can defer execution because of Doze, battery restrictions or load;
  this is not a promise of delivery every 15 minutes. Work survives process
  death and reboot. Each run drains up to 20 feed pages.
- **Foreground** starts a native `dataSync` foreground service while the app is
  visible. It immediately posts an ongoing, low-importance polling notification,
  checks every five seconds after catching up, and drains further pages without
  that delay. It continues when the WebView is backgrounded or closed. This
  consumes more battery and does not guarantee that Android keeps the app alive.
  Android 15+ limits dataSync services to six background hours per 24 hours,
  shared across such services. On timeout the service stops and Connection
  reports the limit; reopen Connection to restart it while the app is visible,
  or choose Background. There is no foreground-service launch on boot.

The app requests Android 13+ notification permission when connecting with native
notifications enabled. **Board notifications** and **Foreground polling** have
separate system channels. If permission or a required channel is blocked,
checks retain the cursor and report the problem in Connection. Re-enable them
in Android Settings and reconnect. Android's stop/force-stop and manufacturer
battery controls can suspend work; opening and reconnecting restores it.

Both modes use authenticated plain GET requests through the same Rust feed and
delivery journal as desktop. There is no Firebase, Google push or Google services
account. Native jobs contain only a random scheduling generation, never a token
or password. The work configuration is encrypted with an Android Keystore key
in the app's private no-backup directory. Disconnect invalidates it and cancels
periodic and foreground work. Turning notifications off retains the current
connection for existing notification taps while cancelling all polling.
Changing modes replaces the prior scheduler;
changing host/identity also prevents old notification taps from opening the
new board. A tap can restore the saved connection after process death and open
its ticket/comment. Explicit cursor recovery is available in Connection.

An origin-scoped document-start WebView script supplies the board's existing
auth bootstrap and disables page notifications while native polling is enabled,
including before board scripts execute. Android System WebView must support
`DOCUMENT_START_SCRIPT`; update WebView if Connection reports it unavailable.
Remote board/proxy pages have no native command permissions. HTTP board origins
are supported in debug and release builds; use HTTPS for remote credentials.

### Build an Android development APK

The native project is checked in; **do not rerun `android:init`** for normal
builds, because it would overwrite the project's Android configuration.
Install JDK 21, SDK command-line tools, platform-tools, Android platform 36,
build-tools 36.0.0 and NDK 29.0.14206865. Set `ANDROID_HOME`, `NDK_HOME` and
`JAVA_HOME` to those installations. Tauri may select Android Studio's bundled
JDK when `JAVA_HOME` is unset. Run from `ui/`:

```sh
sdkmanager 'platform-tools' 'platforms;android-36' 'build-tools;36.0.0' 'ndk;29.0.14206865'
sdkmanager --licenses
rustup target add aarch64-linux-android
npm ci
npm run android:build -- --debug --target aarch64 --apk
# Debug-key-signed, installable APK:
# src-tauri/gen/android/app/build/outputs/apk/universal/debug/app-universal-debug.apk
adb install -r src-tauri/gen/android/app/build/outputs/apk/universal/debug/app-universal-debug.apk
adb shell am start -n dev.scolastico.omo.ui/.MainActivity
npm run android:dev
```

CI needs those same SDK/NDK/JDK and Rust components, Node/npm, accepted SDK
licenses, and dependency access to crates.io, Google Maven and Maven Central.
The checked-in Gradle wrapper downloads Gradle 9.6.1 and uses AGP 9.3.1 / Kotlin
2.2.10. Platform/target SDK is pinned to the available stable SDK 36 rather than
the generated template's unavailable SDK 37. The scoped Wry class extension in
`ui/.cargo/config.toml` adds Basic authentication without replacing navigation or
resource handlers. Changing the identifier also requires updating that JNI export.
Release signing and release CI are separate work.

Focused checks, after an Android build has generated Tauri's local Gradle wiring:

```sh
npm run check
cargo fmt --all --check
cd src-tauri/gen/android
./gradlew --no-daemon :tauri-plugin-omo-notifications:testDebugUnitTest
```

The Android tests use Robolectric SDK 35, controlled WorkManager/executors,
private stores and Rust local HTTP fixtures. They cover scheduler exclusivity,
interval/constraints, permission and channel blocking, cold worker execution,
foreground notification/timeout, encrypted work storage, delivery persistence,
GET authentication and tap destinations. A development arm64 APK was built on
Linux. No emulator or device was available for this work: actual device Keystore,
JNI delivery, permission prompts, notification taps and background/foreground
execution remain unverified on hardware. JVM tests do not replace that evidence.

## Native interfaces

- `ui/core`: `Config` validates modes/origins/auth inputs; `BoardClient` performs
  authenticated native HTTP, feature exchange and incremental polling.
  `Delivery::accept` validates a complete page, persists accepted IDs after
  submissions, then advances its cursor. `Recovery::apply` persists an explicit
  recovery choice. Android workers call the disk-backed adapter through JNI without an activity
  or a running Tauri event loop.
- `ui/src-tauri/src/lib.rs`: the bundled setup's commands are `get_setup`,
  `connect({settings})`, `disconnect`, `recover({replay})` and `local_connect`.
  Settings have `mode`, `origin`, `auth`, `username`, `secret`, `notifications`,
  `notification_mode` (`background` or `foreground`),
  `remember` and `reset_identity`. Results contain public configuration, platform,
  connection/polling status, errors and pending recovery, never credentials.
  `local_connect` is the explicit desktop lifecycle extension point and currently
  returns an unavailable error. Android rejects it.
- `windows.rs`: remote views, related child windows, the connection menu/button
  and the main-window close policy. A lifecycle implementation should replace
  that close policy only after tracking whether the UI owns the server it
  started. No server process or tray is owned by this shell yet.
- `platform.rs`, `macos_auth.rs`, `android_auth.rs`: scoped native WebView settings
  and authentication callbacks. `credentials.rs` is the OS secret-store adapter.
- `ui/notifications`: Android Tauri plugin, WorkManager worker, native foreground
  service, encrypted work storage, channels and activation routing. Desktop uses
  the Rust notification plugin. Neither depends on a visible board or JS timer.

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
a D-Bus fixture, not a visible desktop toast. Windows adapter compilation was
checked with the GNU cross target; Windows runtime/Action Center, macOS and
Android device behavior have not been run on their platforms. WebView2 notification
activation is limited by the plugin to on-screen toasts, not entries already
in Action Center. This host has no macOS SDK. Android build and JVM evidence is recorded above;
device permission/background behavior still needs native-platform verification.

Implementation references: [Tauri capabilities](https://v2.tauri.app/security/capabilities/),
[WebviewWindowBuilder](https://docs.rs/tauri/2.12.7/tauri/webview/struct.WebviewWindowBuilder.html),
and [notification plugin](https://v2.tauri.app/plugin/notification/).

Android references: [periodic WorkManager requests](https://developer.android.com/develop/background-work/background-tasks/persistent/getting-started/define-work),
[foreground service launch restrictions](https://developer.android.com/develop/background-work/services/fgs/launch),
[dataSync time limits](https://developer.android.com/develop/background-work/services/fgs/timeout),
[notification permission](https://developer.android.com/develop/ui/compose/notifications/notification-permission),
and [Tauri mobile plugins](https://v2.tauri.app/develop/plugins/develop-mobile/).
