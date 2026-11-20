# Browser VS Code

Install **vscode** from omo's official plugin list and enable it. **Open in VS
Code** in the **VS Code** sidebar tab opens a project search; selecting a
project opens its repository. The tab lists running instances below the button,
each with **Stop**. It adds no controls to the CEO tab. On a ticket it opens the
selected project's worktree (one button per worktree), or the recorded working
directory of a task without a project. Start ticket work to create its folder.
Missing, removed or replaced folders are reported as unavailable; the plugin
never substitutes a repository. Finished tickets cannot start an editor, even
when their working directory remains available for inspection with a terminal. Editors write directly into those folders,
including alongside an agent. There is no lock or separate copy.

The first click reserves a new tab, checks/downloads the pinned editor, shows
installation progress on the board and starts its supervised task. The same
tab then opens through omo's authenticated Web UI proxy. Keep the board open
while installation finishes; allow popups. A lost request is reported without
automatic retries: inspect tasks/logs before clicking again. Only a confirmed
pre-execution busy response is retried, within the same cancellable deadline.
A later click reuses the running instance when its canonical folder, editor,
pinned version and ticket/global binding match. Each canonical folder has at
most one running editor; different folders can run independently.

## Editors and platforms

The dropdown uses these pinned publisher builds, without a separately installed
Node, Python, editor CLI, curl, tar or shell installer:

| Host | code-server 4.145.2 | OpenVSCode Server 1.109.5 | Microsoft VS Code for the web |
|---|---|---|---|
| Linux amd64 / arm64 | Available | Available | Unavailable: terms not confirmed |
| macOS amd64 / arm64 | Available | No publisher build | Unavailable: terms not confirmed |
| Windows amd64 | Available | No publisher build | Unavailable: terms not confirmed |
| Windows arm64 | No native publisher build | No publisher build | Unavailable: terms not confirmed |

“Available” identifies a pinned native publisher artifact, not proof of every
host version. Linux amd64 is exercised with real Chromium and omo's proxy;
other native targets have fixture coverage and publisher metadata only. Source
builds, WSL, containers, Windows x64 emulation and Alpine/musl are unverified
alternatives, not native supported installs.

Linux needs glibc >=2.28, libstdc++ and the bundled Node runtime's dependencies;
OpenVSCode's prerequisite script calls for GLIBCXX >=3.4.25 (code-server's
standalone docs say >=3.4.21). code-server bundles Node 24; its upstream support
floors include Linux kernel 4.18, macOS 13.5 and Windows 10/Server 2016. Optional
native authentication extensions may require WebKit and other system libraries.
Shells, Git, compilers and language servers still come from the host. ABI,
missing-library and permission errors appear in **Settings → Plugins → vscode
→ Tasks**. See the [code-server installation guide](https://coder.com/docs/code-server/install)
and [OpenVSCode release](https://github.com/gitpod-io/openvscode-server/releases/tag/openvscode-server-v1.109.5).

Microsoft's choice remains visible but cannot install or run, even if old JSON
contains `accept_microsoft_license: true`. Its terms have not been confirmed
for this integration. This is an unresolved permission condition, not a claim
that every personal use is prohibited. The [publisher terms](https://code.visualstudio.com/license/server)
allow specified licensed/internal uses and restrict hosting and combining the
server into offerings for others; the [publisher FAQ](https://code.visualstudio.com/docs/remote/faq#_can-i-use-vs-code-server-to-build-a-service)
explains that restriction. Any future implementation needs affirmative permission
and explicit persisted user licence acceptance before download/start. No checkbox
unlocks the current unavailable choice; no terms are accepted on install, enable
or selection. The proposed direct, checksum-verified **server-web** payload mode
would use its bundled runtime and **would not literally invoke `code serve-web`**.
The latter can fetch additional unverified payloads, so it is not used here.

## Integrity and state

Version, platform, URL and SHA-256 are pinned in `editor.lua`. Sources are the
publisher's [code-server release](https://github.com/coder/code-server/releases/tag/v4.145.2)
and [OpenVSCode release](https://github.com/gitpod-io/openvscode-server/releases/tag/openvscode-server-v1.109.5);
the digests below are the GitHub release API's per-asset SHA-256 values, rather
than detached signatures. A mismatch stops before extraction or execution.
Unsupported platforms fail before any network request. Updating versions requires
reviewing new publisher metadata, layout, flags and probes, then updating the
plugin; there is no `latest` lookup or arbitrary download/version setting.

| Artifact suffix | SHA-256 |
|---|---|
| code-server linux-amd64 | `2f6db6c3e8b733dd6b3aeee83532e148b2bae2e392353ba85452d03572348962` |
| code-server linux-arm64 | `e867a34416e2da4a83496b09580001893e7a4e5bb58a0624c4fcd22819e7d53d` |
| code-server macos-amd64 | `5fb7ac2bd60538400244d942f649559e0ff4fb3d8792979287f504780f357af6` |
| code-server macos-arm64 | `d6699692f1e59d918517c81f9d2a4ef335132128cd45725b22236d5fe09636d4` |
| code-server windows-amd64 | `d88f8a00831334da291ad29b35714c7bf6d3dc398e5b6f5ed1b20034c1da7675` |
| OpenVSCode linux-x64 | `b433bf4f0227321a7014d8460d10a8f958adc0f45aa79bd889e84e65e8f88363` |
| OpenVSCode linux-arm64 | `36d9c14036489b63de84ebace837fcacf7e60e669a0dc715802c5443684ea4dc` |

The generic artifact helper downloads asynchronously over HTTPS, checks SHA-256,
bounds compressed/expanded size and archive members, rejects unsafe paths/links,
and publishes an atomic cache generation. Reuse checks the complete cached tree
without downloading again. Artifact generations and private editor state stay below the plugin's data
folder, `plugins/.data/vscode/`.

### Desktop preferences and extensions

At launch the plugin detects standard default profiles for Microsoft VS Code,
VS Code Insiders, VSCodium, VSCodium Insiders and Code OSS, in that priority
order. Linux uses `XDG_CONFIG_HOME` (or `~/.config`), macOS uses
`~/Library/Application Support`, and Windows uses `APPDATA`. The selected
product and paths appear in the task output. With several installations, the
first matching profile is the shared source. Custom user-data directories,
portable installations and named profiles need their files in the standard
default profile to participate.

The browser uses that product's existing extensions directory (`.vscode`,
`.vscode-insiders`, `.vscode-oss` or `.vscode-oss-insiders` under the OS home).
Installing or removing a compatible extension updates the same directory and
inventory used by the desktop editor; its next start sees those changes.
Desktop-only UI extensions without a browser entry point cannot run in this
browser host; the task output names them and keeps the complete list in
`profiles/<editor>/incompatible-extensions.json`. Extension licences, host/API version
requirements and native dependencies still apply.

A builtin **omo shared preferences** extension connects the browser's actual
user-data provider to the selected desktop `User` directory. It shares
`settings.json`, `keybindings.json`, `tasks.json` and `snippets/`, including new
files and deletions. Dotfile-managed snippet links are followed, with directory
cycles bounded; edits retain existing links, and deletion removes the link
without deleting its external target. User-scoped themes/settings therefore apply in both editor
builds. Changes are synchronized on settings/save events and a one-second
poll. Raw JSONC, trailing commas and UTF-8 BOMs are retained. Disjoint
concurrent setting changes merge using the desktop document’s comments/formatting; a same-setting conflict keeps the desktop
value and saves both complete versions in
`profiles/<editor>/preference-conflicts/`. The extension's output channel reports
that location. Builtin theme ID aliases are translated without rewriting the
desktop's existing theme ID.

Browser preference saves are recorded at the storage boundary, so a save that
finishes during a pending bridge write or deletion remains recoverable in those
private conflict files. Records are acknowledged only after preservation.
code-server's inherited Node preload records complete preference write payloads,
including its disk provider's `open`/`write`/`close` saves and `.vsctmp` atomic
files; it retains Node's promisify metadata. OpenVSCode's private overlay prepends
`browser-history.cjs` to `out/vs/code/browser/workbench/workbench.js`, recording
`put`/`delete` versions in the same transaction in IndexedDB `vscode-web-db` /
`vscode-userdata-store`. A fresh challenge verifies that recording is active
before the bridge mutates browser preferences. Publisher updates must recheck
these internal paths and storage APIs. Verified publisher files remain unchanged.
Native pending records live in `profiles/<editor>/preference-history/`;
OpenVSCode keeps its pending journal in its browser user-data store. A journal
record is bounded to 32 MiB; the browser journal also bounds the total to 32 MiB
and 4096 unacknowledged versions, aborting the original transaction on overflow.
The bridge archives intervening versions even when they exceed its normal
1 MiB synchronization limit or repeat the previous file's content.
Each code-server window also emits its own user-data provider update when a
peer changed the shared native file, refreshing that window's cached settings.
Before replacing or deleting a desktop preference, the native adapter atomically
moves the actual file aside and archives its bytes privately. Publication and
restoration use exclusive creation, leaving any newer desktop file untouched;
dotfile links retain their identity. If both archiving and restoration fail, the
snapshot remains beside the original file with an `.omo-displaced-<uuid>` suffix,
and the extension output reports its recovery path. Native staging/recovery
siblings are excluded from the shared snippet inventory.
Existing conflict copies are verified before reuse; an incomplete or edited
copy is retained alongside a new complete archive.
Native snapshots also keep a link to the displaced inode, so an already-open
desktop descriptor can finish an in-place save during or after synchronization
without losing its bytes. Such `-native-` archives can change until that writer
closes its descriptor. If a private inode link is unavailable, for example
across filesystems, the managed recovery sibling stays on the native filesystem
and its path is reported even after a successful commit.

Without a desktop profile, extensions remain in `profiles/<editor>/extensions/`
and shared preference files in `profiles/<editor>/preferences/User/`. Fresh
profiles initialize Dark Modern and `chat.disableAIFeatures=true`. Existing
browser choices are imported once, and later browser origins recover the
persistent preferences. Each editor has its own private profile. Remote
terminal settings live in `profiles/<editor>/portable/user-data/Machine/settings.json`;
existing JSONC is preserved. code-server also uses `--enable-remote-storage`
for workbench state on disk.

Publisher files remain in the verified artifact. A private builtin-extension
overlay adds the bridge. OpenVSCode's pinned bootstrap overwrites the builtin
directory flag; its preload maps FileAccess to a private publisher root linked
to the verified files and builtin overlay. Databases/runtime stay private.
The bridge includes the pinned MIT-licensed
[jsonc-parser 3.3.1](vendor/jsonc-parser/ORIGIN.md); no package installer runs
when opening an editor.

`sessions/<folder/editor/version hash>/` holds
server data, logs and cache for each instance. Completed
artifact generations, profiles and session directories survive restart and idle
cleanup; session directories are not automatically pruned. Stop the editor before
manually removing an obsolete session directory. Unix preserves contained file symlinks;
Windows materializes file links, which can change Node realpath semantics.
The plugin launches the real JS entry with bundled `node` / `node.exe`, not
copied `.bin` wrappers or shell commands. Windows native execution remains
unproven. Publisher licence/notice files remain in the full artifact tree;
code-server and OpenVSCode use MIT, with separate third-party/extension terms.

The launcher disables code-server update checks, initializes `update.mode=none`,
and OpenVSCode has no editor updater endpoint in its OSS product. Neither uses
a CLI that can silently fetch another editor. Normal user-installed extensions
have their own network behavior/licences. Host HOME/USERPROFILE, XDG configuration,
data/cache/state roots and Windows APPDATA/LOCALAPPDATA remain available to Git,
SSH and terminal tools. Those tools use and may write their normal host
configuration; the editor is not a filesystem sandbox. Editor-specific flags,
portable paths and pinned path-provider preloads keep runtime, databases and
wrapper state in plugin data, including logs/heartbeat that bypass
`--user-data-dir`. IPC/runtime/tmp uses a short unique private OS temporary
directory allocated by omo's generic task helper, with mode 0700 on Unix. This
avoids Unix socket byte-length limits even with long or multibyte plugin paths
or inherited TMPDIR. The editor receives this private XDG_RUNTIME_DIR. Initial
Linux/macOS remote terminal settings restore the host value through
`EDITOR_HOST_XDG_RUNTIME_DIR` (empty if originally unset); existing user JSONC
and terminal overrides remain untouched. Such profiles may need that terminal
setting explicitly if they predate it or override it. This runtime distinction
does not change host HOME or configuration/data roots. Core releases the private
directory after reaping its stable task, including failed startup, Stop, idle,
disable/remove/replacement and shutdown; normal host temporary files are untouched.
Both editors launch with `--disable-workspace-trust`, so omo folders open as
trusted without a prompt. This applies to the launched editor process, without
writing a workspace-trust setting into the project. Editor flags disable the editor's own
password/token and bind exclusively to IPv4 `127.0.0.1`. Inherited `PORT`,
`CODE_SERVER_*`, `VSCODE_*`, password and Node selectors cannot override the
editor launch. code-server uses prefix stripping plus the exact loopback trusted
origin; OpenVSCode uses its native server base path without stripping. Dedicated
proxy subdomains use `/`: OpenVSCode must omit `--server-base-path` to use
the publisher's root default. Explicit `--server-base-path /` becomes an empty
client base in the pinned build and breaks its WebSocket URLs. Non-root path
forwards pass the exact `/proxy/<id>/` flag value, including its trailing slash.
Assets and WebSockets use omo's existing proxy and
Open grant; board credentials never enter editor URLs. Loopback listeners are
reachable to other local processes, as with other omo Web UI forwards.

### Desktop sign-ins and secrets

Every new editor process imports the selected desktop default profile's
SecretStorage records from `User/globalStorage/state.vscdb`, opened read-only.
The import decrypts Electron safeStorage records through the host keyring. For
example, an existing GitHub session is available to the browser's GitHub
authentication extension without signing in again. Expired or revoked tokens
still need the provider's normal sign-in flow. Reusing an already running editor
does not import again; Stop and reopen it after signing in on the desktop.

Linux uses `secret-tool` for Secret Service, or `dbus-send` for KWallet
(including its configured network wallet). These host utilities must be
installed for keyring-encrypted records. Desktop `argv.json` password-store
selection takes precedence over desktop-environment detection, including explicit
KWallet service versions. KWallet discovery, synchronous open, password read and
non-forced handle release all address that same daemon; an unversioned KF wallet
client cannot redirect the read to another version. Existing Linux basic-text (`v10`) records need no
keyring helper. macOS uses `/usr/bin/security`; Windows uses built-in Windows
PowerShell and DPAPI, including the profile key in `Local State` when present.
The OS may request permission to unlock/access its keyring. Missing helpers,
locked keyrings, unsupported ciphertext or an unreadable desktop database stop
startup with a credential-free error in task output. A desktop database lock
waits up to five seconds before reporting failure. No keyring item is created
or changed by the import.

The browser's provider is installed before authentication extensions activate.
It persists secrets across folders, process restarts and changed proxy origins in
`profiles/<editor>/secrets/`: AES-256-GCM ciphertext in SQLite, with a separate
private random key. The directory is 0700 and its files 0600 on Unix; access to
both key and database permits decryption. It follows omo's existing user account
boundary. Secret values, helper output and capability tokens are never printed
by the plugin or placed in URLs. Requests use a private per-launch capability
through the editor's loopback listener and omo's authenticated proxy.

Unchanged imported values follow refreshed desktop values on the next launch.
Browser edits and sign-outs take precedence and survive subsequent imports;
secrets are not written back to the desktop. The existing first-matching-profile
selection also applies to secrets. Legacy keytar-only stores, custom/portable
layouts and named profiles are outside automatic discovery.

The pinned publisher renderer is copied into a private session overlay to supply
the provider; verified artifact bytes remain unchanged. An unknown renderer
layout is refused. The native format references are VS Code's
[secret store](https://github.com/microsoft/vscode/blob/1.109.5/src/vs/platform/secrets/common/secrets.ts),
[encryption service](https://github.com/microsoft/vscode/blob/1.109.5/src/vs/platform/encryption/electron-main/encryptionMainService.ts)
and [Electron keyring setup](https://github.com/electron/electron/blob/v37.3.1/shell/browser/electron_browser_main_parts.cc).

Linux amd64 publisher probes use synthetic encrypted GitHub sessions and assert
account discovery through the real GitHub authentication extension plus extension
SecretStorage access before and after restart. They do not use a real GitHub
token or certify a live GitHub API request. Linux Secret Service decryption has
also been exercised against an isolated native keyring. Real `dbus-send` calls
against simultaneous synthetic KWallet 5/6 services verify the selected daemon
throughout the sequence, even when wallet names match, including handle release.
Native macOS, Windows and KWallet daemon execution remains fixture-covered.

```sh
node plugins/vscode/tests/probe.cjs openvscode --smoke --profile-sharing --local-product VSCodium --secrets
node plugins/vscode/tests/probe.cjs code-server --smoke --subdomain --cross-site --profile-sharing --local-product Code --secrets
```

`--cache-dir` reuses a probe artifact cache; keep it on the same filesystem as
the probe temporary root (`TMPDIR`) because the probe moves the cache by rename.

## Stop and idle

`idle_minutes` defaults to **30**, with a range of 1–10080. The plugin arms the
generic forward idle watch; an authenticated editor WebSocket keeps it alive,
including beyond 30 minutes. Only the last observed browser disconnect starts
the idle interval. Reconnecting clears that interval. HTTP health/poll traffic
does not extend it. A never-connected/abandoned startup also expires. Physical
network loss is observed when the underlying connections close.
An observed TCP EOF ends a WebSocket tunnel immediately, even if the editor
keeps its side open; the no-browser interval starts when the final tunnel ends.

Tasks are named `cs-t<TICKET>-<folder hash>-<run>`, `ovs-t<TICKET>-<folder hash>-<run>` or
`cs-project-<folder hash>-<run>` / `ovs-project-<folder hash>-<run>`. Names are unique
per run so delayed cleanup cannot stop a replacement. Their first output lines
identify the folder and private state directory. **Stop** in the VS Code sidebar tab or plugin settings,
ticket completion/cancellation, idle expiry, removing a watched forward,
disable/remove and omo shutdown stop the process tree and remove its forward.
No editor restarts automatically. omo startup leaves old references inert and
starts no editor. Manual opens prune completed folders, keeping the bounded
store usable. Periodic maintenance (once a minute) reaps orphaned tasks/forwards
left by an interrupted hook after a 120-second grace from the last manual hook.
It checks only its original resource snapshot and leaves records to manual
hooks, so it cannot delete a concurrent launch. Downloads remain cached. If a forward/task is
stale, a subsequent explicit click cleans up and recreates it. Bind collisions
get at most three attempts; other early exits/timeouts require an explicit retry.

Changing the selection leaves existing editors running. Opening the same
canonical folder with another editor, pinned version or ticket/global binding
is refused with an instruction to **Stop** the existing editor in plugin
settings first. After Stop, the next click can use the new selection; a different
folder can use it immediately. A selection change during first-use polling
ends that request with a clear retry message. Changing idle duration is applied
when the instance is next opened/polled; it preserves the existing no-client
interval.

## Verification

Routine fixtures never download publishers' binaries:

```sh
lua5.1 plugins/vscode/tests/hooks.lua
node --test plugins/vscode/tests/*.test.cjs
go test ./internal/plugins ./internal/pluginmanager -count=1
```

Lua 5.1 is a development-test tool only; installed plugins use omo's built-in
sandbox. Fixtures simulate the documented artifact/task/forward boundary and
exercise the real hook, launcher and frontend code. Existing generic artifact
and forward tests cover checksum/cache integrity, active/last-disconnect/reconnect
races and lifecycle cleanup; fixture launcher tests start real local processes.
Microsoft tests assert refusal only; no payload or saved acceptance is seeded.

Opt-in probes download large real artifacts and start an isolated **temporary**
omo with no real agents or user projects. Build omo first, install web test
dependencies and Playwright Chromium. Run from the repository root:

```sh
make build
node plugins/vscode/tests/probe.cjs code-server
node plugins/vscode/tests/probe.cjs openvscode
# Optional dedicated-subdomain check:
node plugins/vscode/tests/probe.cjs code-server --subdomain
node plugins/vscode/tests/probe.cjs code-server --subdomain --cross-site
node plugins/vscode/tests/probe.cjs openvscode --subdomain --cross-site
```

Use `--cache-dir <folder>` to move one reusable publisher cache between
isolated probe homes instead of downloading/copying each publisher again. Keep
that folder outside `/tmp`; only one probe may lease it at a time. Add
`--smoke --profile-sharing` for real settings/extension changes and a full
Stop/reopen check on a new forward. `--local-product Code`, `VSCodium` or
`"Code - OSS"` creates an isolated desktop profile; omit it to verify private
persistence and the dark/AI-disabled defaults. These probes install disposable
fixture extensions only in their isolated profiles, and await an acknowledged
extension-host startup before terminal commands.

Add `--preference-races` to `--smoke --profile-sharing` to gate the disposable
bridge after its final content check, save through the actual publisher's user
data provider, then release the pending write/delete. All four preference
categories must retain that newer save in private conflict copies. The gate
changes only the installed test copy; neither publisher nor product sources
contain a test delay.

Each runs the actual plugin first-click installation through the real board,
opens the workbench in the reserved tab, saves a text edit directly into each
folder, checks a second click reuses the task,
opens two simultaneous folders with retained shared settings/extensions and
separate temporary directories, runs Git against disposable host configuration
inside both integrated terminals, checks independent folder cleanup,
keeps browsers connected beyond the configured one-minute idle timeout, tests
reconnect and checks last-disconnect task/forward removal. Temp files and a
secret-free `report.json` are retained at the printed path. Reports distinguish
publisher builds from the stand-in and record routing mode, bootstrap/exchange,
unauthenticated refusal, asset loads, the OpenVSCode client base and live
WebSocket sent/received frame counts (without payloads or connection queries).
Instances are pinned by canonical workspace and attached stable task/forward
IDs. Waits and disconnect-to-exit measurements use a monotonic clock. The full
mode requires each pinned task to exit with reason `idle`, including the second
folder's reload/immediate-close case; `--smoke` remains an explicit-Stop check.
`lifecycle.jsonl` records sanitized page/socket events and resource checkpoints.
On Linux, the report also verifies that captured editor descendants are gone
using PID/start-time identities; `shutdown.json` retains browser/server exit
and remaining-child evidence on success or failure. Process inspection on other
platforms remains unverified.
The server and
browser are stopped on success and failure. Delete those disposable files when
you no longer need the evidence. These probes are not part of routine tests.
Append `--smoke` for the first-use, reuse, two-folder settings/terminal/edit and
explicit Stop checks without waiting through the idle/reconnect phase.

On the exercised Linux amd64 builds, both path workbenches load successfully
while the publisher's optional `vsda` JavaScript/WASM resources return 404.
code-server also requests a TypeScript extension browser entry absent from its
pinned publisher archive; its Node extension entry still activates. Failed
requests are recorded in probe reports, including the requested file for remote
resources. These checks do not certify proprietary authentication features or
arbitrary extensions.

The routine browser integration checks are separate from those publisher probes:

```sh
make build
npm --prefix web run e2e -- e2e/plugin-editor.spec.ts e2e/plugin-lifecycle.spec.ts e2e/plugin-frontend.spec.ts --workers=2
```

They use temporary homes/repositories, a local HTTPS archive and a small editor
protocol stand-in. It defaults OpenVSCode to root only when the base flag is
omitted and refuses explicit `/`, which the publisher cannot use for WebSockets.
Only the installed test copy's artifact URL/digest and idle
interval change; the manifest, frontend polling, Lua orchestration, launcher,
artifact extractor, task supervisor and authenticated proxy remain in use.
The fixture runtime bridge uses the test runner's Node through a POSIX wrapper,
so these checks run on Linux and do not prove publisher runtime compatibility.
They need OpenSSL and Playwright Chromium. They exercise both editor launch
contracts in path and subdomain mode, a remote board hostname mapped locally,
real assets and WebSocket round trips beyond idle, reconnect/last-disconnect
cleanup, checksums/cache reuse, download cancellation and unavailable choices.
The sample covers public frontend APIs, configuration, startup and process-tree
cleanup on completion, cancellation, cascade, Stop, disable, removal, replacement,
natural exit and restart. All child omo environments strip inherited `OMO_*`
variables through the browser harness; no live home/socket is used.

Retained Linux amd64 publisher probe evidence for the pinned builds includes
OpenVSCode path mode and code-server cross-site subdomain mode with two active
folders, shared settings, disposable host Git/runtime configuration, reuse and
independent idle/runtime cleanup. Separate smoke reports record browser edits
saved to both folders and explicit Stop cleanup. These are real publisher runs
from development, distinct from the routine stand-in tests; they do not certify
native macOS, Windows or arm64. Reproduce them with the opt-in commands above;
reports retain the optional upstream 404s described here.
