# Contributing

omo is a Go server with the board, a Vue app, built into the binary.

- [CLAUDE.md](../CLAUDE.md) is the map of the code: the package layout, the
  invariants, the test rules and where each feature lives. Read it first; it
  is kept up to date with the code.
- The [Makefile](../Makefile) has every build and check (`make help` lists
  them):

```bash
make build           # bin/omo (builds the board first: needs Node.js 24 and npm)
make build NO_WEB=1  # bin/omo without the board, no Node.js needed
make check           # gofmt check, go vet, unused code (with staticcheck installed) and the full Go test suite: what CI runs
make web-check       # type-check and unit-test the board
make e2e             # build omo and run the browser tests (Playwright, Chromium)
make cross           # binaries for Linux, macOS and Windows on amd64 and arm64
make notices         # bin/THIRD_PARTY_NOTICES, the licences of the Go modules and the bundled npm packages
```

To run your build next to an omo you use, give it its own home and port (see
[Several homes](home-folder.md#several-homes)) and put its `bin` folder first
on `PATH`, because agents call `omo ai` by name:

```bash
export OMO_HOME=$(mktemp -d)
./bin/omo config set port 18123
PATH="$PWD/bin:$PATH" ./bin/omo
```

No test calls a real model, forge or usage service: agents in tests are a
scripted fake agent, and every test uses a temporary home.

Pull requests follow [the template](../.github/PULL_REQUEST_TEMPLATE.md) and
conventional commit messages. A change in what users see updates this wiki in
the same pull request. Plugins are listed through a pull request to
`plugins/index.yaml`, described in [plugins/README.md](../plugins/README.md).

## Tests that stay deterministic under load

Wait for the condition that makes an action valid: a completed RPC, a stored
board event, a closed completion channel, the final renamed file, or a usable
control. Visibility alone does not prove that terminal history has replayed or
that a clipboard upload response has delivered the path to paste. Attach event
listeners before triggering the action; hold responses or use acknowledgements
to exercise the unfinished state deliberately.

A negative assertion needs a completion barrier: drain the operation/event
pipeline, then assert that no extra result exists. A fixed sleep proves nothing
about completion. Use bounded condition polling for external state and explicit
channels/barriers for owned goroutines. Publish whole files by writing a private
temporary file and renaming it; readers wait for the final name and contents.

Separate a behavioral deadline (the timeout being tested) from a generous
watchdog that only prevents a hung test. Explain watchdog choices in a comment.
Use `testing/synctest` for in-process clock behavior where possible. Real elapsed
upper performance ceilings below one minute are rejected by the Go test guard;
Playwright `waitForTimeout` calls are rejected by the browser unit-test guard.
Lower bounds, virtual clocks and diagnostic elapsed values remain valid. These
narrow guards complement review; they do not infer the purpose of every timer.

Tests own their temporary homes, repositories, processes and state. Strip all
inherited `OMO_` variables case-insensitively before setting isolated values;
isolate HOME/XDG/git configuration and use fake agent programs. A port observed
free can be taken before bind: use an acquired listener or prove startup and
handle only genuine address collisions. Stop children even on failure. Avoid
shared mutable test fixtures and assumptions about concurrent event order.

## Constrained Linux stress runs

The [stress runner](../scripts/stress/run.py) requires Linux, Python 3, Go, a C
compiler for Go's race detector, and (for browsers) Node/npm dependencies and
Playwright Chromium. Install the browser prerequisites with
`npm --prefix web ci` and `npx --prefix web playwright install --with-deps chromium`.
The harness builds the current browser app/binary before browser attempts.
Each checkout needs its own dependencies; linked node_modules are rejected to
keep preparation from mutating another checkout.

```bash
make stress-check                 # accounting, isolation and cleanup contract checks
make stress                       # full suites: 20 Go + 10 browser attempts
make stress STRESS_ARGS='--suite go --go-runs 3'
make stress STRESS_ARGS='--suite browser --browser-runs 2 --throttle 6'
# Focused diagnosis/calibration only; never full-suite acceptance evidence:
make stress STRESS_ARGS='--suite go --go-runs 100 --packages ./internal/mcprelay --go-test ^TestChangeNoticesAreCoalescedAndOnlyARealChangeIsAnnounced$ --seed 1795006222869594230'
STRESS_SUITE=go GO_RUNS=3 scripts/stress/ci.sh --packages ./internal/testguard
```

The runner picks two CPUs from its actual allowed affinity, rather than assuming
CPU 0/1. `--cpus` can select two allowed IDs. Go uses `-race -count=1`, four
package/test workers, GOMAXPROCS=4 and a recorded shuffle seed per attempt;
`--seed` fixes the base (attempt n uses base+n-1). Playwright uses four workers,
`--repeat-each=1 --retries=0`, Chromium CDP CPU rate 4 by default, and retains
failure traces/screenshots. Inherited GOFLAGS are cleared and full-test selectors
are explicit, so a developer's shell flags cannot silently narrow a campaign. Playwright runtime/report
overrides are removed, the source config is explicit, and a separate `--list`
collection must match every browser attempt. Go top-level inventories must stay
consistent across attempts. Browser order remains Playwright's normal declaration
and scheduler order; only Go order is shuffled. Counts are separate
whole-suite attempts, never rerun-failed retries. `--go-workers` and
`--browser-workers` must exceed two; see `--help` for selectors and watchdogs. The Go per-package watchdog is 60 minutes
because it covers a whole race-instrumented package; the whole-command watchdog
is three hours. Neither is a performance assertion.

Throttling uses `E2E_CPU_THROTTLE_RATE`: new pages are throttled before navigation,
and popup external resources wait for the CDP acknowledgement. Playwright cannot
guarantee throttling an initial inline script in an unrelated popup document.
The default ordinary browser run remains unthrottled. A source without the
throttle hook fails preflight instead of producing unthrottled stress evidence.

Every invocation creates a fresh folder under `bin/stress/` (`--output` changes
the parent). It records the source commit/tree, dirty-source content hashes,
runner hash, affinity, settings, exact commands, seeds, raw stdout/stderr, Go JSON,
Playwright JSON and per-attempt artifacts. `summary.json` contains all individual
test pass/failure/skip counts; each attempt records reported/executed/skipped
totals and browser workers actually used. `summary.md` lists attempt results and
failure frequencies. Explicit platform/screenshot skips remain visible in JSON and skip
counts; no filters, retries or skips are added by the harness. Empty, incomplete,
retried or malformed reports fail the campaign, as does any failed command/test.
A later success cannot erase a failure. Source changes during a campaign also
invalidate it. SIGINT/SIGTERM and command watchdogs terminate/reap owned process
trees, including adopted detached children; diagnostic files remain.

The **Constrained stress** workflow runs both full suites nightly, with three Go
and two browser attempts, and accepts custom counts through `workflow_dispatch`.
It uses `scripts/stress/ci.sh`, publishes the failure-frequency summary and keeps
all artifacts for 14 days, including on failure. The 3/2 guard is smaller than the
20/10 acceptance campaign: use `make stress` or dispatch counts 20/10 for that
campaign, on a stable final commit. Focused selectors and dirty trees are clearly
recorded and do not establish final full-suite acceptance. Historical calibration
and its exact provenance are documented in [stress calibration](stress-calibration.md).
