# Dashboard Reload Reconnect Regression Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task with review checkpoints.

**Goal:** Exercise dashboard reload/reconnect against the actual company Handler, a registered running Instance, terminal replay, and both current and pre-bbef593 filebrowser installations.

**Architecture:** The Go test owns an isolated global home, actual company Server, controlled terminal process, and registered Instance. A Node test connects Chromium through CDP, authenticates with persistent BasicAuth headers, captures WebSocket bytes before xterm consumes them, and asserts terminal/plugin geometry, hit-testing, modal state, and real mouse clicks over repeated reloads.

**Tech Stack:** Go httptest, company Server/Handler, coder/websocket, Node.js built-ins, Chromium CDP, xterm.js, bundled filebrowser assets.

**Spec:** PM Jason’s Job 76 rework mail, message 574.

## Global Constraints

- Keep production behavior unchanged unless the faithful regression proves a root cause.
- Run both the current bundled filebrowser and the pre-bbef593 stale tree in isolated global homes.
- Include Chromium headless-shell and chrome-linux64 discovery, with cleanup installed before startup can fail.
- Verify with focused Node/Go tests, terminal exit stress, `make check`, and `git diff --check`.

### Task 1: Actual company integration fixture

**Files:**
- Create: `internal/company/dashboard_reload_integration_test.go`
- Modify: `internal/company/browser_input_test.go`

- [x] Add a controlled terminal that publishes startup mode sequences, accepts a WebSocket trigger, then publishes a replay-overflow payload while remaining running.
- [x] Add sequential current/stale subtests using isolated OMO_HOME values, actual `company.New`, httptest Handler, BasicAuth, and a registered `ownInstance`.
- [x] Materialize the stale plugin from `bbef593^` into the isolated global plugin root without allowing globalhome to refresh it.
- [x] Pass the live server URL, credentials, and variant to the Node CDP test; register process/server cleanup immediately after every creation.

### Task 2: CDP reload/reconnect runner

**Files:**
- Replace: `internal/company/dashboard_reload.test.cjs`

- [x] Discover chromium, chromium_headless_shell, and chrome-linux64 candidates; skip cleanly when Node WebSocket or a browser is unavailable.
- [x] Install browser/profile cleanup before startup, log browser version and launch command, and configure persistent BasicAuth headers.
- [x] Load the authenticated dashboard, click the running instance, wait for xterm and filebrowser, trigger replay overflow, then repeat reload/select cycles.
- [x] Capture raw initial replay frames before xterm handling and report mode sequences for 1000/1002/1003/1006/1004/1049/2004 plus xterm final modes.
- [x] Assert toolbar/sidebar/terminal/footer elementFromPoint data, computed style/rects, plugin and terminal geometry, dialogs, `:modal`, inert ancestors, active element, and CDP mouse clicks.

### Task 3: Verification and handoff

- [x] Run the focused browser regression for both variants and the direct Node suite.
- [x] Run the terminal exit test with `-count=100`, `make check`, diff check, and inspect the final clean worktree.
- [x] Report exact reproduction/non-reproduction evidence and send the PM the commit and verification results.
