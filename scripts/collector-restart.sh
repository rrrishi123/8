#!/usr/bin/env bash
# collector-restart — make a freshly-built collector binary actually RUN.
#
# The collector is the WITNESS. fleet-sync rebuilds its binary but the running
# process keeps executing the OLD one until it is bounced.
#
# THE SUPERVISOR WINS (verified 2026-09-25, and per the 'collector restart race'
# memory): when scripts/watchdog.sh is up it polls every ~15s and, on :7070
# empty, revives the collector itself — recomputing the gecko SID live, probing
# brokers, applying the witness-tmux pin, and exec'ing collector/collector (it
# only rebuilds if the binary is MISSING, so a fresh build.sh binary is picked
# up). That revive is MORE correct than any argv we could capture (a captured
# -gecko session id goes stale on the next Firefox recycle). Fighting it =
# two collectors racing for :7070. So the right restart is: KILL and let the
# watchdog revive. We keep a self-relaunch only as a SAFETY NET for hosts with
# no collector-reviving watchdog up — otherwise the witness would stay dead.
#
#   ./collector-restart.sh --check   # print the plan; change nothing
#   ./collector-restart.sh           # DISRUPTIVE: bounces :7070 in place
#
# Opt-in from fleet-sync (RESTART=1); never runs on its own.
set -uo pipefail
PATTERN="${PATTERN:-collector/collector -listen}"   # the built binary — NOT gitbroker/watch
HEALTH="${HEALTH:-http://127.0.0.1:7070/health}"
WAIT="${WAIT:-40}"                                   # total seconds to wait for /health
GRACE="${GRACE:-20}"                                 # let a watchdog revive first (>1 loop); 0 if none
CHECK=0; [ "${1:-}" = "--check" ] && CHECK=1
say(){ printf '  %s\n' "$*"; }
grn(){ printf '\033[32m%s\033[0m\n' "$*"; }
red(){ printf '\033[31m%s\033[0m\n' "$*"; }
alive(){ curl -s -m 3 "$HEALTH" | grep -q '"alive":true'; }

# exactly one live collector, or refuse (ambiguity near the witness = don't touch)
n=$(pgrep -f "$PATTERN" | wc -l | tr -d ' ')
[ "$n" = 1 ] || { red "collector-restart: expected 1 process matching '$PATTERN', found $n — refusing"; exit 1; }
pid=$(pgrep -f "$PATTERN" | head -1)
argv=$(ps -o command= -p "$pid")
if [ -r "/proc/$pid/cwd" ]; then cwd=$(readlink "/proc/$pid/cwd")          # Linux
else cwd=$(lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p'); fi # macOS/BSD
[ -n "$cwd" ] || { red "collector-restart: cannot resolve cwd of $pid — refusing"; exit 1; }

# a collector-reviving watchdog? (mac watchdog.sh is verified to revive :7070;
# match the peer variant too — if it turns out not to revive, the safety net fires)
wd=$(pgrep -f 'scripts/watchdog(-omarchy)?\.sh' | head -1)
if [ -n "$wd" ]; then mode="watchdog(pid $wd) revives; self-relaunch only if still down after ${GRACE}s"
else mode="no watchdog — self-relaunch immediately"; GRACE=0; fi

echo "== collector-restart  pid=$pid  cwd=$cwd"
say "argv: $argv"
say "plan: kill -> $mode -> verify /health within ${WAIT}s"
if [ "$CHECK" = 1 ]; then grn "(--check) nothing changed"; exit 0; fi

relaunch(){ local log="/tmp/collector-restart.$(date +%s).log"
  ( cd "$cwd" && setsid nohup $argv >"$log" 2>&1 & ) 2>/dev/null || ( cd "$cwd" && nohup $argv >"$log" 2>&1 & )
  say "self-relaunched from $cwd (log: $log)"; }

kill "$pid" 2>/dev/null; for _ in 1 2 3 4 5; do kill -0 "$pid" 2>/dev/null || break; sleep 0.4; done
kill -0 "$pid" 2>/dev/null && { kill -9 "$pid" 2>/dev/null; sleep 0.5; }
say "collector $pid killed — waiting for revive"

relaunched=0
for s in $(seq 1 "$WAIT"); do
  if alive; then grn "collector-restart: /health alive again after ${s}s ($(curl -s -m 3 "$HEALTH"))"; exit 0; fi
  # past the grace window and still nothing listening? fire the safety net once.
  if [ "$relaunched" = 0 ] && [ "$s" -ge "$GRACE" ] && ! lsof -ti :7070 >/dev/null 2>&1; then
    [ -n "$wd" ] && say "watchdog didn't revive within ${GRACE}s — firing self-relaunch net"; relaunch; relaunched=1
  fi
  sleep 1
done
red "collector-restart: /health did NOT return within ${WAIT}s — WITNESS DOWN, check /tmp/collector-restart.*.log and watchdog"; exit 1
