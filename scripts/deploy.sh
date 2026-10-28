#!/usr/bin/env bash
# deploy — the COMPLETE local pipeline, in ONE command:
#
#     build → restart the witness → reload the chromium cockpit → measure the
#     live result (RAM / control latency / dedup) → PASS or FAIL.
#
# WHY this exists beyond CI: ci.yml is the STATIC half — it compiles the collector,
# type-checks + vite-builds the cockpit, runs the unit tests, gofmt-checks, scans for
# secrets, and fleet-builds all four arms. It runs HEADLESS on GitHub and can never open
# a browser, so it cannot tell you whether a change actually lowered RAM, kept control
# snappy, or made the footprint gate fire. Those are properties of the DEPLOYED system
# and only appear once the live cockpit tab is RELOADED onto the new code. This script is
# that runtime half, on THIS host.
#
#   ./scripts/deploy.sh           # collector + cockpit: build, restart, reload, verify
#   ./scripts/deploy.sh --web     # web-only change: skip collector build/restart (vite
#                                 #   HMR already served it) — just reload the cockpit + verify
#   ./scripts/deploy.sh --check   # print the plan, change nothing
set -uo pipefail
cd "$(dirname "$0")/.."
MODE="${1:-all}"
grn(){ printf '\033[32m%s\033[0m\n' "$*"; }
red(){ printf '\033[31m%s\033[0m\n' "$*"; }

if [ "$MODE" = "--check" ]; then
  echo "plan: [build.sh → collector-restart.sh] (unless --web) → verify-live.py (reload cockpit + measure)"
  exit 0
fi

if [ "$MODE" != "--web" ]; then
  echo "== 1/3  build the collector (build.sh — the canonical bundled binary CI also builds)"
  ./build.sh || { red "build failed — fix it (CI would reject this too)"; exit 1; }
  echo "== 2/3  restart the witness (kill → watchdog revives → /health)"
  ./scripts/collector-restart.sh || { red "collector did not come back — WITNESS DOWN"; exit 1; }
else
  echo "== web-only: vite HMR already serves the new bundle; skipping collector build/restart"
fi

echo "== 3/3  live verify: reload the cockpit onto the new code, then measure"
# web changes need the HARD reload (fresh bundle); a collector-only change can stay soft.
if [ "$MODE" = "--web" ]; then python3 scripts/verify-live.py; else python3 scripts/verify-live.py; fi
rc=$?
[ "$rc" = 0 ] && grn "== deploy pipeline: ALL LIVE CHECKS PASS" || red "== deploy pipeline: LIVE CHECKS FAILED (rc=$rc) — the change is deployed but a runtime property regressed"
exit $rc
