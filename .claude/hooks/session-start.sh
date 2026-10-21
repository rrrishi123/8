#!/bin/bash
# session-start.sh — bring the four-arm system up in a Claude Code on the web
# container: chrome seat (adapters) -> channel broker (http-mcp) -> collector (8,
# wired to the seat) -> wire (http-mcp) -> pilot broker -> `collector watch`.
# Idempotent: every body is probed first and only started if it is down, so a
# resume or a container restart brings back exactly what is missing.
# The four repos are siblings: <root>/{8,adapters,http-mcp,pilot}; root is
# derived from this file's location, never inscribed.
set -uo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

root="$(cd "$(dirname "$0")/../../.." && pwd)"
logs="$root/.logs"
mkdir -p "$logs"
export EIGHT_ADAPTERS="$root/adapters"

open() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
healthy() { curl -fsS -m 2 "http://127.0.0.1:$1/health" >/dev/null 2>&1; }
spawn() { local log="$1"; shift; setsid nohup "$@" >>"$logs/$log" 2>&1 < /dev/null & }
wait_open() { for _ in $(seq 1 40); do open "$1" && return 0; sleep 0.25; done; return 1; }

for r in 8 adapters http-mcp pilot; do
  [ -d "$root/$r" ] || { echo "four-arm: $root/$r missing — skipping bring-up"; exit 0; }
done

# substrate: the container's chromium (Playwright's) as a chrome-family seat
if ! command -v chromium >/dev/null 2>&1 && [ -x /opt/pw-browsers/chromium ]; then
  ln -sf /opt/pw-browsers/chromium /usr/local/bin/chromium
fi

# build each arm only if its binaries are absent (a fresh container)
[ -x "$root/8/collector/collector" ] || (cd "$root/8" && ./build.sh) >>"$logs/build.log" 2>&1
[ -x "$root/http-mcp/.bin/wire" ]    || (cd "$root/http-mcp" && ./build.sh) >>"$logs/build.log" 2>&1
[ -x "$root/adapters/.bin/browser" ] || (cd "$root/adapters" && ./build.sh) >>"$logs/build.log" 2>&1
[ -x "$root/pilot/.bin/broker" ]     || (cd "$root/pilot" && ./build.sh) >>"$logs/build.log" 2>&1

# 1. chrome seat on :9333 (the pack detaches it and goes headless when DISPLAY is unset)
if ! open 9333; then
  "$root/adapters/.bin/browser" up --engine chrome --port 9333 --bin "$(command -v chromium)" >>"$logs/seat.log" 2>&1
  wait_open 9333
fi
ws="$(curl -fsS -m 3 http://127.0.0.1:9333/json/list 2>/dev/null \
  | python3 -c 'import sys,json; print(next((p["webSocketDebuggerUrl"] for p in json.load(sys.stdin) if p.get("type")=="page"),""))' 2>/dev/null)"

# 2. channel broker holding the seat's CDP socket on :4446
if [ -n "$ws" ] && ! open 4446; then
  spawn channel.log "$root/http-mcp/.bin/channel" -ws "$ws" -listen :4446
  wait_open 4446
fi

# 3. collector on :7070, wired to the seat (serve mode persists these flags to
#    ~/.8/boot.json, so `watch` revives it wired)
if ! healthy 7070; then
  if open 4446; then
    spawn collector.log "$root/8/collector/collector" -listen :7070 -brokers chrome=http://127.0.0.1:4446
  else
    spawn collector.log "$root/8/collector/collector" -listen :7070
  fi
  wait_open 7070
fi

# 4. wire (MITM witness) on :4724, witnessing into the collector
open 4724 || spawn wire.log "$root/http-mcp/.bin/wire" -listen :4724 -witness http://127.0.0.1:7070

# 5. pilot broker on :7700
open 7700 || spawn pilot-broker.log "$root/pilot/.bin/broker" -addr :7700

# 6. supervisor
pgrep -f "collector/collector watch" >/dev/null || spawn watch.log "$root/8/collector/collector" watch

sleep 1
status=""
for p in 9333:seat 4446:channel 7070:collector 4724:wire 7700:pilot-broker; do
  open "${p%%:*}" && status+="${p#*:}=up " || status+="${p#*:}=DOWN "
done
echo "four-arm system: ${status}(logs: $logs)"
exit 0
