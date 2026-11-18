#!/bin/sh
# The CI entry point also accepts explicit local calibration selectors.
# Workflows pass no selectors, so scheduled/dispatched runs cover full suites.
set -eu
exec python3 scripts/stress/run.py \
  --suite "${STRESS_SUITE:-both}" \
  --go-runs "${GO_RUNS:-3}" \
  --browser-runs "${BROWSER_RUNS:-2}" \
  --output bin/stress-ci "$@"
