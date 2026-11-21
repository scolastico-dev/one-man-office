# omo ui

`ui/` contains a Tauri 2 application that opens an omo server's board in a
native window. Its package contains only connection/setup assets and branding;
the selected server supplies the board, plugins and forwarded applications.

## Connect

Start the app, choose **An external omo host directly**, and enter its origin, for
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

Android offers direct and relay connections. Native validation and the local
runtime refuse local start/install there.

## Connect through a relay

Desktop offers **Local omo**, **An external omo host directly**, and **An external
omo host through relay**. Android offers the two external choices. Select the
relay choice and enter its HTTPS origin (no slash/path, query, fragment or
credentials), the omo host's client id, and an optional separate relay admission
password. An explicit checkbox permits HTTP only for a loopback relay.

For a new device, run `omo relay pair begin` on the host's local command line,
then click **Start new pairing** in the app. The app creates a post-quantum device
identity in the native credential store and proves possession with the host.
Only after mutual proof does Connection show the six-digit PIN (including leading
zeroes) and the exact command:

```sh
omo relay pair accept <aid> <PIN>
```

Run that command locally on the real host. Acceptance is the entire ceremony:
the app loads the host's board automatically, with no second confirmation or
invitation. **Advanced** contains the entire raw public key and the alternative
`omo relay pair accept <aid> --public-key <full-public-key>` command. A fingerprint
is not a replacement for the full key.

**Connect** uses saved trust only. The app restores its relay connection after a
restart and reconnects with fresh session keys after host/relay loss. It never
starts another pairing automatically. Disconnect cancels and waits for this
connection's socket/bridge to close and closes its board windows. After a failed,
expired or cancelled ceremony, start a fresh host ceremony and explicitly choose
**Start new pairing**. If the earlier device may have been accepted, unregister
it on the host before replacing it. Saved candidate trust can instead recover an
acceptance whose reply was lost. Missing private state is an error; restoration
never silently creates a replacement identity.

Relay identities, host pins, roots and admission passwords stay in the native
credential store. Public settings contain the route and connection preferences.
The board is served through an authenticated private loopback bridge over the
encrypted relay; bridge credentials never enter page JavaScript. Relay admission
and TLS routing do not grant host authority. This UI does not bundle the board.

The notification checkbox and Android scheduling preference are saved for relay
connections. Relay receipt/delivery scheduling is a separate native integration;
until it is active, relay connections negotiate capabilities without claiming
system delivery and leave browser notifications available.

## Recognize the Connection screen

On desktop, the app-owned window is titled **omo ui · Connection**. Every board
and child document's native title starts with **omo ui · Host content ·**, followed
by bounded plain document text. A page cannot replace that prefix with its own
Connection title. Host HTML fullscreen is disabled on WebKit so it cannot hide
the native frame. On Windows, a native window subclass preserves the caption
and nonclient frame even if WebView2 requests fullscreen. Remote windows have
no configuration or pairing command ACL.

Android keeps the bundled Connection WebView separate from the native host
WebView. An app-owned blue surrounding frame says **omo ui · Trusted Connection**;
remote content gets a brown frame saying **omo ui · Host content**, with a native
**Connection** button. These widgets live outside page HTML, ignore page titles,
and remain visible when a document requests fullscreen. Native Connection actions
switch to the trusted view. The host WebView has no Tauri JavaScript command
interface, and visiting a local-looking URL there never grants setup authority.

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
Release signing and CI use the installation key described below.

Focused checks, after an Android build has generated Tauri's local Gradle wiring:

```sh
npm run check
cargo fmt --all --check
cd src-tauri/gen/android
./gradlew --no-daemon :tauri-plugin-omo-notifications:testDebugUnitTest
```

The Android tests use Robolectric SDK 35 (plus SDK 27/35 crash-backup recovery),
controlled WorkManager/executors,
private stores and Rust local HTTP fixtures. They cover scheduler exclusivity,
interval/constraints, permission and channel blocking, cold worker execution,
foreground notification/timeout, encrypted work storage and AtomicFile backup
recovery after interrupted writes, delivery persistence,
GET authentication and tap destinations. A development arm64 APK was built on
Linux. No emulator or device was available for this work: actual device Keystore,
JNI delivery, permission prompts, notification taps and background/foreground
execution remain unverified on hardware. JVM tests do not replace that evidence.

## Release installers and CI

Publishing a GitHub release starts the existing CLI release flow and the native
UI jobs in parallel. All check out that release's tag. UI jobs pass its version
through `scripts/release.mjs config`; no manifest needs a version bump. Supported
tags are `MAJOR.MINOR.PATCH` with an optional `v` prefix, matching the existing
stable tags. Minor/patch components must be at most 999 and the derived Android
version code (`major * 1000000 + minor * 1000 + patch`) must be 1–2100000000.
The checked-in `0.1.0` versions are development defaults; PR builds use a fixed
test version and never upload release assets.

For version `0.9.2`, the additional release assets are:

| Platform | Architecture | Installer |
| --- | --- | --- |
| Linux (Debian/Ubuntu, GTK 3/WebKitGTK 4.1) | x64 / arm64 | `omo-ui-0.9.2-linux-amd64.deb` / `omo-ui-0.9.2-linux-arm64.deb` |
| Windows (WebView2) | x64 / arm64 | `omo-ui-0.9.2-windows-amd64.exe` / `omo-ui-0.9.2-windows-arm64.exe` (NSIS) |
| macOS | Intel / Apple Silicon | `omo-ui-0.9.2-darwin-amd64.dmg` / `omo-ui-0.9.2-darwin-arm64.dmg` |
| Android 7+ (API 24+) | arm64-v8a | `omo-ui-0.9.2-android-arm64.apk` |

Desktop installers have no developer certificate, Authenticode signature or
Apple notarization. The APK alone is signed with the persistent installation
key. These packages bundle only the native shell, setup/connection assets and
branding, with no board web files or dedicated omo server executable.

Each target uploads its own `omo-ui-<platform>-<arch>-SHA256SUMS` beside the
installer. Download both and run `sha256sum -c omo-ui-linux-amd64-SHA256SUMS`
(or the matching target file). No UI job writes the CLI's `SHA256SUMS`; its six
archive names, contents and checksums remain the contract used by installers
and `omo update`. Plugin-branch syncing and container publishing still depend
on the existing CLI release job.

`.github/workflows/ui-desktop.yml` builds real packages on native Linux,
Windows and macOS runners for both architectures. `ui-android.yml` installs
JDK 21, SDK 36/build-tools 36.0.0/NDK 29.0.14206865 and Rust's Android arm64
target. PRs call the same workflows: all desktop packages, focused core/Node
checks, an Android debug APK, and the native notification JVM tests. Release
APK output is zipaligned (including 16 KiB native alignment), signed with
`apksigner`, verified, and checked against the configured certificate before
staging. Missing signing inputs fail with the exact secret name; they never
produce an unsigned release upload. The unsigned intermediate stays in Gradle's
build folder and is never uploaded.

Normal local packaging commands from `ui/`, after installing the prerequisites:

```sh
npm ci
# Choose the tag being packaged; this does not publish anything.
ui_release_tag=0.9.2
# Linux; use aarch64-unknown-linux-gnu on a native arm64 host.
npm run build -- --ci --target x86_64-unknown-linux-gnu --bundles deb --config "$(node scripts/release.mjs config "$ui_release_tag")"
node scripts/release.mjs desktop target/x86_64-unknown-linux-gnu/release/bundle dist linux amd64 "$ui_release_tag"
# Windows: x86_64-pc-windows-msvc or aarch64-pc-windows-msvc, --bundles nsis.
# macOS: x86_64-apple-darwin or aarch64-apple-darwin, --bundles dmg.
# Stage with platform windows/darwin and architecture amd64/arm64 respectively.

# Android: export the five OMO_UI_ANDROID_* variables listed below first,
# plus ANDROID_HOME, NDK_HOME and JAVA_HOME from the Android build instructions.
node scripts/release.mjs android "$ui_release_tag"
# Only verified signed output and its checksum are placed in ui/dist/.
"$ANDROID_HOME/build-tools/36.0.0/apksigner" verify --verbose --print-certs dist/omo-ui-0.9.2-android-arm64.apk
```

A bare `npm run android:build -- --apk` remains a low-level unsigned release
build. Use the wrapper above for installation/distribution; use `--debug` for
the development-key APK described earlier. No CI job publishes an AAB or uses
Google Play publishing credentials.

### One-time Android installation key setup

The repository owner performs these steps once on a trusted machine. Generate
a new installation-only key, distinct from any Google Play app-signing or
upload key. Keep the keystore and both passwords in secure backups: updates
must retain this identity. CI never generates a replacement key.

```sh
ui_key_dir="$HOME/.local/share/omo-ui-signing"
install -d -m 700 "$ui_key_dir"
keytool -genkeypair -storetype JKS -keystore "$ui_key_dir/installation.jks" \
  -alias omo-ui-install -keyalg RSA -keysize 3072 -validity 10000
chmod 600 "$ui_key_dir/installation.jks"
# keytool prompts for the store password, certificate details and key password.
# This prints public certificate details, including SHA256:
keytool -list -v -keystore "$ui_key_dir/installation.jks" -alias omo-ui-install
```

In GitHub repository **Settings → Secrets and variables → Actions → Repository
secrets**, set these exact names. Equivalently, from a checkout authenticated
with `gh`, use the following commands; passwords are read without echo and
passed on standard input:

```sh
base64 < "$ui_key_dir/installation.jks" | gh secret set OMO_UI_ANDROID_KEYSTORE_BASE64
gh secret set OMO_UI_ANDROID_KEY_ALIAS --body omo-ui-install
read -r -s -p 'Keystore password: ' ui_store_password; printf '\n'
printf '%s' "$ui_store_password" | gh secret set OMO_UI_ANDROID_KEYSTORE_PASSWORD
read -r -s -p 'Key password: ' ui_key_password; printf '\n'
printf '%s' "$ui_key_password" | gh secret set OMO_UI_ANDROID_KEY_PASSWORD
read -r -p 'Public SHA256 fingerprint from keytool: ' ui_certificate
printf '%s' "$ui_certificate" | gh secret set OMO_UI_ANDROID_CERT_SHA256
unset ui_store_password ui_key_password ui_certificate
```

`OMO_UI_ANDROID_KEYSTORE_BASE64` is the base64-encoded complete JKS file;
`OMO_UI_ANDROID_KEYSTORE_PASSWORD` unlocks the store;
`OMO_UI_ANDROID_KEY_ALIAS` selects `omo-ui-install`;
`OMO_UI_ANDROID_KEY_PASSWORD` unlocks that private key (use the store password
if identical); `OMO_UI_ANDROID_CERT_SHA256` pins the public certificate, either
64 hexadecimal digits or keytool's colon-separated form. This pin rejects a
wrong key even if all passwords are valid. Store none of these values in the
repository, workflow text or artifacts. The signing helper decodes the private
key into a mode-0600 file in a private temporary directory and removes it on
success or failure. Compilation does not receive the signing secrets.

### Register the public certificate

Use the **SHA256 certificate fingerprint**, not SHA1 and not the APK file's
checksum. Confirm it matches both `keytool -list -v` above and `apksigner verify
--print-certs` on the signed release APK.

In Play Console, complete developer identity verification, open **Android
developer verification → Package names**, register/select
`dev.scolastico.omo.ui`, and add the installation key's SHA256 fingerprint.
If prompted for ownership proof, follow the console's challenge instructions:
it supplies a snippet for `assets/adi-registration.properties` in a separately
prepared verification APK with this same package name and signing key. Upload
that proof only in the verification flow. This CI does not generate or submit
such a challenge and does not publish to a Play track. See Google's
[Play Console key registration instructions](https://support.google.com/googleplay/android-developer/answer/16762301?hl=en).

For distribution exclusively outside Google Play, Google also provides the
[Android Developer Console](https://developer.android.com/developer-verification/guides/android-developer-console):
a verified account registers the package and public SHA256 fingerprint there,
with an ownership challenge when requested. Registration is separate from
Google Play app signing and does not guarantee that Play Protect will suppress
all warnings. Never upload the private keystore to either console.

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
  `notification_mode` (`background` or `foreground`), `remember`, `reset_identity`
  and `stop_with_window` (default false). Results contain public configuration, platform,
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
- `ui/notifications`: Android Tauri plugin, WorkManager worker, native foreground
  service, encrypted work storage, channels and activation routing. Desktop uses
  the Rust notification plugin. Neither depends on a visible board or JS timer.

Only the bundled `setup` document has the five explicit command permissions.
Board/proxy views have no remote capabilities, shell/process/filesystem plugins,
or arbitrary native HTTP endpoint. Their injected bridge exposes a notification
marker and a configure action, not a general native API.

## Native relay library

`ui/core/src/relay` implements the existing [version-1 relay protocol](relay-protocol.md)
in native Rust, without a Go process. It provides committed pairing, mutual
ML-KEM-768 possession proofs, authenticated records/reconnect and notification
request/envelope cryptography. Its native stream multiplexer and private board
bridge carry host HTTP, SSE and WebSocket traffic. The shell uses `RelayConnection`
for setup/window wiring; persisted relay notification receipt policy is a
separate consumer.

The entry points for those consumers are:

- `wire::Route::new(origin, host_id, allow_loopback_http)` validates the exact
  configured origin/name used in every transcript. Production uses verified
  WSS/HTTPS. HTTP/WS requires explicit loopback development opt-in.
- `RelayClient::open(Arc<dyn SecretStore>, key, route, admission)` creates a new
  identity only if the secret entry is absent. Use `RelayClient::restore` for
  an existing saved profile: missing secrets fail instead of generating another
  identity. Clone one client to share its state; never open the same storage
  key concurrently. `RelayStatus` is public metadata only.
- `begin_pair().await` is an explicit human-authorized ceremony. It returns
  `Pairing`, with `info()` exposing `aid`, the six-character `pin`, full
  `public_key`, `host_fp` and `ui_id` only after verifying the possession proof.
  The UI should show `omo relay pair accept <aid> <PIN> --name <label>`;
  full-key acceptance belongs behind advanced options. `accepted().await`
  validates the encrypted acceptance and returns `RelaySession`.
- `reconnect().await` authenticates against the saved host key/epoch and checks
  encrypted active `ready` with the original attempt id before sending
  `ready.ack`. This also recovers a lost acceptance reply from a saved candidate;
  an unaccepted candidate gains no authority. Reconnect never generates a new
  ceremony or notification root. `discard_unproved_attempt` is an explicit
  local operation and refuses a candidate with a saved root, since it may
  already have been accepted on the host.
- `RelaySession::split` gives a cloneable `RecordSender` and one
  `RecordReceiver`. Sender calls serialize sealing **and** writing globally;
  the receive consumer demultiplexes all stream records. `control()` supplies
  status subscriptions, cancellation and async `close()` that waits for socket
  release and key erasure. Old handles can remain closed while a new session
  connects. Dropping a pending send after sealing closes the session; uncertain
  records are never replayed. Socket reader/writer loops continuously service
  Ping/Pong and use bounded queues (32 records); overload terminates a session.
  Consumers must implement the protocol's bounded stream credits and read
  concurrently, rather than treating a socket write as peer application receipt.
- `StreamMux::new(session)` owns one receive demultiplexer and a round-robin
  writer. `open().await` returns a `RelayStream` implementing Tokio
  `AsyncRead`/`AsyncWrite` only after `stream.opened`. It enforces sequential
  odd ids, 64 slots, 65,536-byte receive windows and 16,384-byte data records.
  Credit returns only on consumption; one pending output record per stream
  bounds buffering. `shutdown` sends ordered write FIN while reads continue;
  dropping an unfinished stream resets it. Reset snapshots retain their slots
  until reciprocal reset, validating crossed credit/data/FIN without buffering
  cancelled payloads. `close().await` joins the record/socket driver.
- `crypto::{NotificationContext, new_poll, open_notification}` and the strict
  `wire` notification codecs provide the request MAC, outstanding-request binding
  and response authentication/decryption. They do not persist receipts or
  delivery policy. Request randomness comes from `crypto::random_bytes`.

`SecretStore` loads/saves one private snapshot atomically and durably, with a
missing entry distinct from unavailable storage. Snapshots contain the private
seed, admission password, consumed attempt marker, host candidate/pin, original
attempt id, epoch and notification root; none belongs in public settings.
Snapshots use the same 32 KiB encoded limit for reads and writes, checked before
replacement; this includes worst-case JSON escaping of a 4 KiB admission password.
Saves precede commitment, reveal, final proof and trust transitions. A failed
storage write latches the client closed until storage is repaired and it is reconstructed.
`omo_ui::RelaySecrets` adapts the existing desktop/Android credential store using
separate `relay:<key>` entries. Android headless worker storage can implement the
same trait without Tauri or a WebView; worker wiring is not provided here.
Credential-store size/availability failures are surfaced and never fall back to
public files. A saved candidate with lost secrets requires restoring those
secrets or unregistering the possible host registration before a new ceremony.

Crypto uses [RustCrypto `ml-kem` 0.3.4](https://docs.rs/ml-kem/0.3.4/ml_kem/) with final FIPS 203 seed/key/ciphertext
encodings and OS cryptographic randomness, SHA-384/HMAC/HKDF and AES-256-GCM.
The crate describes its implementation as unaudited; fixture interoperability
is not an independent protocol audit or certification. Tests use the existing
NIST ACVP and protocol fixtures, including PIN `007026`, both proofs, record
keys/ciphertexts and notification MACs/envelopes.

```sh
cd ui
cargo test -p omo-ui-core
node --test tests/*.test.mjs
# Optional real Go relay/host integration, no OS WebViews:
node scripts/relay-probe.mjs
# Actual embedded board assets/API/upload/SSE/terminal and automatic recovery:
# npm --prefix ../web ci first if its dependencies are missing.
node scripts/relay-probe.mjs --board
```

The integration probe builds an isolated Go fixture and exercises PIN/full-key
acceptance, interception/loss of acceptance, UI/host/relay reconstruction,
unaccepted-candidate denial, concurrent ordered record senders and reconnect
with retained closed handles. It removes its private temporary state and joins
its child processes. Relay core compilation has been checked on Linux x86_64,
Android arm64 and Windows x64 GNU; this is not Android/Windows device runtime
or OS credential-store evidence.

### Native board adapter

`BoardTransport::start(client, session: Option<RelaySession>).await` returns the
connection-owned adapter for desktop and Android. Pass the session returned by
`Pairing::accepted` to use a freshly accepted connection, or `None` to authenticate
with saved trust. A live unrelated session makes `start(None)` fail with busy;
clone one client for shared state rather than opening its secret entry twice.

`endpoint()` returns `BoardEndpoint { origin, username, password }`. These are
fresh, ephemeral **bridge** Basic credentials, unrelated to relay admission or
endpoint cryptographic secrets. Use the existing native WebView Basic-auth
callbacks with `Auth::Basic` and realm `omo`; keep the endpoint credentials only
in the owning native connection, out of saved profiles, URLs and JavaScript.
The endpoint is deliberately not serializable or debug-printable. The listener
binds only `127.0.0.1` on an allocated port, requires its exact Host and Basic
credential, and rejects foreign/null Origins, cross-site Fetch Metadata,
ambiguous header framing and relay/private credential headers. Requests without
credentials receive a Basic challenge. It has no CORS grant or native-command
capability. Remote documents retain the shell's existing command boundary.

The bridge sends origin-form HTTP/1.1 over each authorized encrypted stream,
replacing Host/Origin with `omo-relay.invalid` / `http://omo-relay.invalid` and
removing bridge authentication. Ordinary connections use `Connection: close`;
WebSocket upgrades retain their headers, subprotocol and opaque frames. Bodies,
chunking, application JSON, assets and streamed responses are not rewritten or
buffered as whole messages. No arbitrary upstream or host HTTP fallback exists.

`status()` and `subscribe()` expose `Connecting`, `Ready`, `Reconnecting`,
`Unavailable`, `Stopped`. The shell should watch Ready to load/reload its board
document. The same loopback endpoint survives reconnects during this owner.
After loss, the adapter authenticates with fresh session keys and the saved
host key/epoch/attempt, retrying after 1, 2, 4, 8, 16 and then 30 seconds. A host
not registered yet remains retryable. Failure, admission denial or revocation
stays visible as Unavailable; none starts a new pairing or replaces trust.
Offline requests return 503. The adapter never replays HTTP requests, uncertain
writes or terminal input. Board subscriptions reopen through their existing
Last-Event-ID/resync and terminal replay/message-ack behavior.

Own one adapter with the configured connection and call `close().await` on
disconnect/replacement: it closes the listener, cancels and joins every accepted
connection and its own authentication/socket work before reporting Stopped.
Drop requests cancellation; explicit close observes completion. `RelayConnection`
and the shell own setup/native chrome; Android notification receipt policy is
a separate integration.

The `--board` probe builds the real Vue board and a test-only control-pipe
fixture around the production Go server and relay, without a host browser
listener. It checks actual assets/API, an exact multi-window chunked PNG upload,
SSE live/resume/resync, terminal binary output/input and message acknowledgments,
host/relay loss and delayed host registration, saved-trust UI reconstruction,
no duplicate uncertain write or terminal input, revocation and joined shutdown.
Protocol unit tests use exact records and consumption events, not elapsed time.
Transport compilation is checked for Linux desktop, Windows GNU and Android
arm64. The native setup owner is also exercised against the real Go board for
acceptance and saved restoration. For the relay setup integration, Android debug
APK/AAB compilation and 26 Robolectric tests (including native frame switching)
pass; Windows GNU shell compilation passes. This development host lacks
WebKitGTK development files, an attached Android device, and the Apple SDK, so
relay loading in an OS WebView and Windows/macOS runtime chrome remain unverified.

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
compile with the GNU cross target; Windows runtime/Action Center, macOS and
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
