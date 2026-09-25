#!/usr/bin/env bash
# collector-restart — make a freshly-built collector binary actually RUN.
#
# The collector is the WITNESS. fleet-sync rebuilds its binary but the running
# process keeps executing the OLD one until it is bounced. You cannot just
# `kill` it and trust a supervisor to revive it: verified 2026-09-25 there is
# not always a `collector watch` up (none on mac), and ~/.8/boot.json can hold a
# stale/experimental spec (a different port/brokers) that would revive the WRONG
# collector. So this does the only safe thing when unsupervised: capture the
# LIVE process's exact argv + cwd, kill it, relaunch the IDENTICAL invocation
# detached, and VERIFY /health returns — if it does not come back inside the
# window, it shouts RED (the witness is down, a human must look). Same config in,
# same config out: no worse than the state it was already in.
#
#   ./collector-restart.sh --check   # print what it WOULD relaunch; change nothing
#   ./collector-restart.sh           # DISRUPTIVE: bounces :7070 in place
#
# It is opt-in from fleet-sync (RESTART=1) and never runs on its own.
set -uo pipefail
PATTERN="${PATTERN:-collector/collector -listen}"   # the built binary — NOT gitbroker/watch
HEALTH="${HEALTH:-http://127.0.0.1:7070/health}"
WAIT="${WAIT:-15}"                                   # seconds to wait for /health to return
CHECK=0; [ "${1:-}" = "--check" ] && CHECK=1
say(){ printf '  %s\n' "$*"; }
grn(){ printf '\033[32m%s\033[0m\n' "$*"; }
red(){ printf '\033[31m%s\033[0m\n' "$*"; }

# exactly one live collector, or refuse (ambiguity near the witness = don't touch)
n=$(pgrep -f "$PATTERN" | wc -l | tr -d ' ')
[ "$n" = 1 ] || { red "collector-restart: expected 1 process matching '$PATTERN', found $n — refusing"; exit 1; }
pid=$(pgrep -f "$PATTERN" | head -1)
argv=$(ps -o command= -p "$pid")
# cwd: the binary path is relative (collector/collector), so we MUST relaunch from it
if [ -r "/proc/$pid/cwd" ]; then cwd=$(readlink "/proc/$pid/cwd")          # Linux
else cwd=$(lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p'); fi # macOS/BSD
[ -n "$cwd" ] || { red "collector-restart: cannot resolve cwd of $pid — refusing"; exit 1; }

echo "== collector-restart  pid=$pid  cwd=$cwd"
say "argv: $argv"
if [ "$CHECK" = 1 ]; then grn "(--check) would kill $pid and relaunch the above from $cwd — nothing changed"; exit 0; fi

# bounce + relaunch the IDENTICAL invocation, detached, logged
kill "$pid" 2>/dev/null; for _ in 1 2 3 4 5; do kill -0 "$pid" 2>/dev/null || break; sleep 0.4; done
kill -0 "$pid" 2>/dev/null && { kill -9 "$pid" 2>/dev/null; sleep 0.5; }
log="/tmp/collector-restart.$(date +%s).log"
( cd "$cwd" && setsid nohup $argv >"$log" 2>&1 & ) 2>/dev/null || ( cd "$cwd" && nohup $argv >"$log" 2>&1 & )
say "relaunched from $cwd (log: $log)"

# VERIFY the witness answers again, or shout
for _ in $(seq 1 "$WAIT"); do
  if curl -s -m 3 "$HEALTH" | grep -q '"alive":true'; then
    grn "collector-restart: /health alive again ($(curl -s -m 3 "$HEALTH"))"; exit 0
  fi
  sleep 1
done
red "collector-restart: /health did NOT return within ${WAIT}s — WITNESS DOWN, check $log"; exit 1
