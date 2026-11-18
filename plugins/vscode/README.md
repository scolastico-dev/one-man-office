# Browser VS Code

Install **vscode** from omo's official plugin list and enable it. **Open in VS
Code** on each sidebar project opens its repository; on a ticket it opens the
selected project's worktree (one button per worktree). Start ticket work first
if its folder is unavailable. Editors write directly into those folders,
including alongside an agent. There is no lock or separate copy.

The first click reserves a new tab, checks/downloads the pinned editor, shows
installation progress on the board and starts its supervised task. The same
tab then opens through omo's authenticated Web UI proxy. Keep the board open
while installation finishes; allow popups. A lost request is reported without
automatic retries: inspect tasks/logs before clicking again. Only a confirmed
pre-execution busy response is retried, within the same cancellable deadline.
A later click
reuses the same running instance for the canonical folder/editor/version.

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
without downloading again. Artifact generations and editor-managed persistent
state stay below the plugin's own data folder, `plugins/.data/vscode/`.
`profiles/<editor>/` holds settings, extensions, portable defaults and extension
downloads, shared across folders and pinned versions of that editor. The two
editors have separate profiles. Shared Remote settings live in
`profiles/<editor>/portable/user-data/Machine/settings.json`; edit the Remote
settings scope in the editor. `User/settings.json` is retained and seeds a missing
Machine file without replacing existing JSONC. Browser-local preferences follow
the editor's normal origin/profile behavior. `sessions/<folder/editor/version hash>/` holds
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
portable paths and a pinned code-server path-provider preload direct editor
state into plugin data, including wrapper logs/heartbeat that bypass
`--user-data-dir`. IPC/runtime/tmp uses a short unique private OS temporary
directory allocated by omo's generic task helper, with mode 0700 on Unix. This
avoids Unix socket byte-length limits even with long or multibyte plugin paths
or inherited TMPDIR. The editor receives this private XDG_RUNTIME_DIR. Initial
Linux/macOS terminal settings restore the host value through
`EDITOR_HOST_XDG_RUNTIME_DIR` (empty if originally unset); existing user JSONC
and terminal overrides remain untouched. Such profiles may need that terminal
setting explicitly if they predate it or override it. This runtime distinction
does not change host HOME or configuration/data roots. Core releases the private
directory after reaping its stable task, including failed startup, Stop, idle,
disable/remove/replacement and shutdown; normal host temporary files are untouched.
Editor flags disable the editor's own
password/token and bind exclusively to IPv4 `127.0.0.1`. Inherited `PORT`,
`CODE_SERVER_*`, `VSCODE_*`, password and Node selectors cannot override the
editor launch. code-server uses prefix stripping plus the exact loopback trusted
origin; OpenVSCode uses its native server base path without stripping. Dedicated
proxy subdomains use `/`. Assets and WebSockets use omo's existing proxy and
Open grant; board credentials never enter editor URLs. Loopback listeners are
reachable to other local processes, as with other omo Web UI forwards.

## Stop and idle

`idle_minutes` defaults to **30**, with a range of 1–10080. The plugin arms the
generic forward idle watch; an authenticated editor WebSocket keeps it alive,
including beyond 30 minutes. Only the last observed browser disconnect starts
the idle interval. Reconnecting clears that interval. HTTP health/poll traffic
does not extend it. A never-connected/abandoned startup also expires. Physical
network loss is observed when the underlying connections close.

Tasks are named `cs-t<TICKET>-<folder hash>-<run>`, `ovs-t<TICKET>-<folder hash>-<run>` or
`cs-project-<folder hash>-<run>` / `ovs-project-<folder hash>-<run>`. Names are unique
per run so delayed cleanup cannot stop a replacement. Their first output lines
identify the folder and private state directory. **Stop** in plugin settings,
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

Changing the editor affects the next click; existing instances retain their
editor/version until Stop or idle. A selection change during first-use polling
ends that request with a clear retry message. Changing idle duration is applied
when the instance is next opened/polled; it preserves the existing no-client
interval. A canonical folder already open under a different ticket/global
binding must be stopped before changing that binding.

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
```

Each runs the actual plugin first-click installation through the real board,
opens the workbench in the reserved tab, saves a text edit directly into each
folder, checks a second click reuses the task,
opens two simultaneous folders with retained shared settings/extensions and
separate temporary directories, runs Git against disposable host configuration
inside both integrated terminals, checks independent folder cleanup,
keeps browsers connected beyond the configured one-minute idle timeout, tests
reconnect and checks last-disconnect task/forward removal. Temp files and a
secret-free `report.json` are retained at the printed path; the server and
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
