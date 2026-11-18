# Stress pressure calibration

On 2026-11-18, the Linux harness reproduced the historical
`TestChangeNoticesAreCoalescedAndOnlyARealChangeIsAnnounced` failure on its exact
pre-fix source, without a code transplant:

- Commit: `2a81d945d143a86aac818e1a0a47621db64a202f` (parent of the MCP fix
  `10e76f7478c17e410dea8813118c85353796a1ec`).
- Tree: `c3369a7728a74411cdbedad9f70851f1f546f10a`; clean source manifest SHA256
  `ba0e2c507a3350ec8763a990670aa9636cb0a05013e759d8e9761c031b2741bb`.
- Go `go1.27.2 linux/amd64`; allowed affinity 0–15; chosen affinity 0,1.
- Four package/test workers, GOMAXPROCS=4, race detector enabled, count=1,
  one independently recorded invocation per attempt, no retry-on-failure.
- Base shuffle seed `1795006222869594230`; attempt n uses base+n-1.
- **19 failures / 100 completed attempts; 81 passes; no skips. Campaign exit 1.**

The exact original invocation, all 100 outcomes/seeds, raw-log hashes and every
failure's diagnostic output are retained in the committed
[calibration ledger](../scripts/stress/calibration/mcprelay.json). The original
raw JSON/stdout/stderr, command files and summaries remain under
`bin/stress-calibration/logs/20261118T134720Z-1v8xminh/` in the calibration task's
working area. These historical failures establish pressure; they are not final
acceptance runs.

To reproduce, create an isolated checkout at that revision beneath a private
working area, review its environment safety, and run the current harness against
it. Keep generated source snapshots in a private task-owned temporary directory
outside repository-wide source scans and dependency folders. Install each
snapshot's own dependencies; never link mutable node_modules between checkouts.
Never change a live project or someone else's worktree.

```bash
python3 scripts/stress/run.py \
  --source /tmp/<owned-private-directory>/calibration \
  --suite go --go-runs 100 --packages ./internal/mcprelay \
  --go-test '^TestChangeNoticesAreCoalescedAndOnlyARealChangeIsAnnounced$' \
  --seed 1795006222869594230 --output bin/stress-calibration/logs
```

The child command for attempt n was:

```text
go test -json -race -count=1 -shuffle=<base+n-1> -p=4 -parallel=4 -timeout=60m -run ^TestChangeNoticesAreCoalescedAndOnlyARealChangeIsAnnounced$ ./internal/mcprelay
```

Every failed attempt reported `stdio_test.go:475: an unchanged list was
announced`, with `mcp.tools_changed` and `mcp.changed` events. Failed attempts:
2, 11, 13, 16, 20, 27, 32, 33, 42, 43, 46, 47, 49, 50, 56, 59, 60, 61, 71.
There were no other failures in this campaign.

The class was **publication/completion boundaries that did not cover the work**.
Historical `instance.start` closed `ready` before publishing the final startup
status, so the test could capture its event baseline before the startup event.
Historical `relist` also cleared `relistQueued` and incremented `relists` before
the list RPC and publication completed; that condition did not prove an idle
pipeline. The MCP change fixes the shared lifecycle/publication protocol and adds
barrier-based publication tests. This calibration changes no product code or
historical assertions and does not claim a genuine tool-list mutation occurred.

Safety review covered historical `TestMain`, `newFixture`, `stdioServer`,
`instance.startProcess` and `childEnvironment`: this selected test starts only
its own scripted fake MCP test binary and an in-memory pool, with cleanup; it
never starts omo's server or CLI. The harness removed every inherited `OMO_`
key case-insensitively before Go started, isolated HOME/XDG/git, and the relay
removed the fixture's synthetic `OMO_` values before its fake child. No
historical full suite was run. Browser pressure uses the agreed Chromium rate
hook; its initial-inline-popup limitation is described in the contributor guide.
