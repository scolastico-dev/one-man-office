# one-man-office agent guide

Fast technical orientation for agents modifying `one-man-office` (`omo`). Read [README.md](./README.md) for what the product is and how to start it.

## What exists today

`omo` is one global server per user (per `OMO_HOME`). It keeps tickets in SQLite, enforces the board's data rules, and serves one HTTP API with an event stream, on a TCP port for the browser and on a local Unix socket (Windows named pipe) for `omo ai`. The server runs the agent engine (`internal/engine`) and serves the board, a Vue app built from `web/` and embedded in the binary.

Not there yet: project discovery (projects are added in the board or through the API), comments, diffs and agent terminals in the browser, settings pages, usage scripts and skill sets. The plugin engine (`internal/plugins`, `internal/pluginmanager`, the top-level `plugins/` folder) compiles and keeps its tests, but the server does not load it. The `wiki/` folder describes the earlier office-based design and is outdated.

The supported targets are Linux, macOS and Windows on amd64 and arm64. `go.mod` is authoritative for the Go version. Builds are pure Go with `CGO_ENABLED=0`.

## Start here

```bash
make build         # bin/omo
make check         # gofmt check, go vet, full test suite
make test          # tests only
make cross         # all supported OS/architecture binaries
make web-check     # type-check and unit-test the board app
make e2e           # build omo and run the browser tests
```

`make build` and `make cross` build the board first and need Node.js 24 and npm; `make build NO_WEB=1` builds without it (the binary then serves a notice page). `go build ./...` and `go test ./...` never need Node.

Focused runs: `go test ./internal/board -count=1`. The smoke test in `cmd/omo` builds the binary and runs a real server (`go test ./cmd/omo -count=1`); it is skipped with `-short`.

## Architecture

```text
cmd/omo
  -> internal/cli         Cobra commands: omo, -d, stop, config, autostart, update, ai
       -> internal/service     run lock, detached start, authenticated stop, login autostart
       -> internal/server      HTTP API, auth, socket, event stream, the embedded board (web/)
            -> internal/engine      starts and supervises agents, merges, exports, cleans up
            -> internal/board       all ticket rules and persistence
            -> internal/settings    settings folders (YAML) and their change notifications
            -> internal/db          SQLite, migrations, event log
       -> internal/apiclient   socket client used by `omo ai`
```

Package map (`internal/`):

| Package | Role |
|---|---|
| `api` | wire types, error codes and event kinds shared by server and clients |
| `board` | tickets, transitions, ranks, review stages, dependencies, comments, mentions, read scope, agent sessions; the engine-facing methods (`BeginWork`, `CompleteMerge`, `MergeConflict`, `CompleteExport`, `ExportFailed`, `SetBranch`, `SetAttention`) are pure data transitions |
| `db` | `Open` applies migrations by `PRAGMA user_version`; one connection only; `AppendEvent` works inside a transaction |
| `globalhome` | the `OMO_HOME` layout, layout marker, refusal of an older omo's folder, atomic private file writes |
| `serverconfig` | start options (`server.yaml`) and the persistent login token |
| `settings` | `settings.yaml`, `projects/`, `profiles/`, `roles/`, `prompts/`; strict YAML, last valid content stays on a broken hand edit |
| `server` | routes, token/basic auth, Host and Origin checks, socket identity, event broker |
| `transport` | socket / named pipe endpoints and dialers |
| `service` | lifecycle of the one server process |
| `apiclient`, `cli` | `omo ai` and the human commands |
| `webterm` | web terminal core; the engine serves agent terminals through it |
| `agentcli`, `claudetrust`, `names`, `session`, `gitops`, `filelock` | kept for the agent engine |
| `plugins`, `pluginmanager`, `pluginfiles`, `modelusage`, `codexauth`, `selfupdate`, `yamlformat` | plugin engine, usage helpers, self-update |
| `config` | frozen leftover of the earlier design; only plugins, pluginmanager, modelusage and codexauth import it. `internal/archtest` fails if a current package imports it |
| `archtest` | import guard |

## Frontend (`web/`)

The board is a Vue 3 + TypeScript single-page app (Vite, Tailwind CSS v4, vue-router, markdown-it). `web/` is also the Go package `web`, which embeds `web/dist`; `internal/server/spa.go` serves `/assets/*` with immutable caching and `index.html` for every other non-API path. Packages are pinned exactly; install with `npm --prefix web ci`.

```text
web/src/api/        wire types (types.test.ts compares them with internal/api), client, event stream reader
web/src/auth/       login token: taken from the address fragment, kept in localStorage
web/src/store/      the board store: snapshot, then the event stream only; optimistic moves
web/src/board/      move rules, ordering, badges, filters (pure logic)
web/src/features/   board, ticket panel, top bar, create, projects, onboarding
web/src/components/ui/  shared components; web/src/styles/tokens.css holds every colour and size
web/e2e/            Playwright tests against the real bin/omo; web/e2e/seed is the seeding tool
```

- `make web` builds `web/dist`; `make web-check` runs `npm --prefix web run typecheck` and `npm --prefix web run test` (Vitest); one file: `npm --prefix web run test -- <path>`.
- `make e2e` builds omo and runs the browser tests (Chromium; `npx --prefix web playwright install chromium` once). One spec: `npx --prefix web playwright test e2e/<name>.spec.ts`. Each test starts its own omo on a temporary home and a free port, paused.
- Development: run omo, then `npm --prefix web run dev` (port 5173) and open `http://localhost:5173/#<token>`; `/api` is proxied to `OMO_DEV_TARGET` (default `http://127.0.0.1:8090`).
- Which columns accept a drag is computed in the browser (`web/src/board/moveRules.ts`) and checked against `internal/board/testdata/user_drag_targets.json`, which the Go test `TestUserDragTargetsGolden` generates from the real board. A change to the transition rules regenerates it in the same commit: `go test ./internal/board -run TestUserDragTargetsGolden -update-drag-golden -count=1`.
- The seeding tool (`web/e2e/seed`, built by the e2e global setup) writes states the API cannot reach (tickets in progress or review) into a fresh temporary home before omo starts. They are board records only: seeded branches do not exist in the repository, and omo ends seeded agent sessions when it starts.
- Only `web/src/api/log.ts` may call `console`; tests fail on literal colours, text below 12 px and border radii outside the tokens.

## Global home (`OMO_HOME`, default `~/.local/omo`, `%APPDATA%/omo` on Windows)

```text
layout.json  server.yaml  token  settings.yaml  omo.db
projects/<id>.yaml  profiles/<name>.yaml  roles/<role>.yaml  prompts/<name>.md
run/            run.lock, runtime.json, omo.log, omo.sock, autostart.json (0700)
plugins/ usage/ skillsets/ worktrees/ ceo/ logs/     folders reserved for later work
```

`server.yaml` keys: `host` (default `127.0.0.1`), `port` (8090), `auth` (`token`, `basic` or `none`), `basic_auth` (`user:password`), `origin_check` (default on), `allowed_hosts` (extra Host/Origin names). Edit them with `omo config list|get|set|unset`. The login token is generated once, stored in `token` and reused on every start; `omo config rotate-token` replaces it and a running server keeps the old one until its next start.

Agents act through the socket with the header `X-Omo-Session: <session id>` (`omo ai` sends `OMO_AGENT_ID`). No header means the user. A header naming an unknown or ended session is rejected with 401, never downgraded to the user.

## Invariants

- A change is committed to SQLite before it is acknowledged, and every committed change appends an `events` row. The event stream tails that table, so clients get every event once, or an explicit `resync`.
- The board owns every rule. Routes only map requests onto board calls and add no rules of their own. User and agent actions record intent (for example approving sets `review_stage = merging`); only the engine performs git work and then calls the engine-facing methods.
- Inside `board` a transaction callback queries only through its `tx`, never `b.db` (single connection, would deadlock). Never hold a board transaction across slow work.
- Nothing omo-related is written into a project folder, `.git/info/exclude` is never written, and omo never edits the user's git config. Worktrees will live in the global home.
- Producers return empty slices and objects, never nil, so clients see `[]` and `{}`.
- Every non-GET `/api` request needs `Content-Type: application/json`; JSON bodies are decoded strictly (unknown keys are errors).
- Unix-only process or socket code needs its Windows counterpart in a `_windows.go` file.

## Tests

`make check` is the required local gate and matches CI: `gofmt -l` must report nothing, `go vet ./...` must pass, `go test ./... -timeout 900s` must pass.

Tests isolate state: `t.Setenv("OMO_HOME", t.TempDir())`, and `XDG_CONFIG_HOME` (login autostart) into a temp dir. They never touch `~/.local/omo`, a real autostart location, real projects or a running omo, and never depend on the developer's git config. Wait for a stored event or state instead of sleeping. A test that starts a server stops it on every path, including failure.

## CI and releases

- `.github/workflows/pull-request.yml`: `make check`, `make cross`, container build and entrypoint test on pull requests.
- `.github/workflows/nightly.yml`: scheduled test and cross build when `main` changed in the last 24 hours.
- `.github/workflows/release.yml`: test, cross build, package, checksum and upload on a published release, then syncs the tag's `plugins/` tree to the `release` branch.
- `.github/workflows/publish-container.yml`: container image publishing.

Release assets must keep these names, because the installers and `omo update` depend on them: `omo-{linux,darwin}-{amd64,arm64}.tar.gz`, `omo-windows-{amd64,arm64}.zip`, `SHA256SUMS`. The Makefile injects the version with `-ldflags`; do not add a second version source.

The container entrypoint (`docker/entrypoint.sh`) runs `omo config set host 0.0.0.0` and then `omo`; the image exposes port 8090.

## Change checklist

1. Read the owning package and its tests; check `git status` and preserve unrelated work.
2. Add or update the narrowest useful test, run it, then run `make check`.
3. Run `git diff --check` and inspect the final diff.
4. Update `README.md` and this guide when user-visible behavior, commands, the home layout or release assets change.
5. Document only current behavior; git history is the record of old behavior.
