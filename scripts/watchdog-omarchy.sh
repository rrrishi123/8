#!/usr/bin/env bash
# watchdog-omarchy.sh — keep the kosaten Firefox peer wire alive on omarchy.
# The omarchy counterpart of watchdog.sh (Mac). Ported: FLOW-10 proactive memory
# recycling, process-based liveness (not socket — avoid false recycles from a
# busy BiDi socket under stream load), tab save/restore via ~/.8-tabs.txt,
# collector-only revive (no Firefox churn), revive serialization lock.
#
# Rule: recycle only when the PROCESS is gone. Under captureScreenshot stream
# load the single BiDi socket saturates and getTree times out for many seconds —
# a BUSY socket, NOT a dead Firefox. Recycling on socket-unresponsiveness was a
# relentless false-recycle storm (and two concurrent up.sh fighting left Firefox
# down entirely).
#
# On start it brings the wire up; then every 15s it probes Firefox process
# liveness. If Firefox is gone (the EGL/VRAM crash, or any death), it revives
# the whole wire via up-omarchy.sh. This is the long-running service process.
set -uo pipefail
EIGHT=/home/rishi/Work/8
TABS_FILE="${TABS_FILE:-$HOME/.8-tabs.txt}"

echo "[peer-watchdog] started $(date +%H:%M:%S) — process-based liveness — tab store: $TABS_FILE"

# never run two up-omarchy.sh at once — concurrent revives kill each other's
# geckodriver session and leave Firefox down.
run_up() {
  # a healthy up-omarchy.sh finishes in well under 5 minutes; one older than
  # that is wedged (2026-07-05: one sat 31h in do_wait on vite's npm, and this
  # guard skipped every revive while Firefox stayed dead). Kill stale, proceed.
  for pid in $(pgrep -f 'bash.*up-omarchy.sh' 2>/dev/null); do
    age=$(ps -o etimes= -p "$pid" 2>/dev/null | tr -d ' ')
    if [ "${age:-0}" -gt 300 ]; then
      echo "[peer-watchdog $(date +%H:%M:%S)] up-omarchy.sh pid $pid stale (${age}s) — killing wedged revive"
      kill "$pid" 2>/dev/null; sleep 1; kill -9 "$pid" 2>/dev/null
    elif [ -n "$age" ]; then
      echo "[peer-watchdog $(date +%H:%M:%S)] up-omarchy.sh already running (${age}s) — skip (no concurrent revives)"
      return
    fi
  done
  bash "$EIGHT/up-omarchy.sh"
}

# probe Firefox by process, not BiDi socket — only a missing process is a true death.
# Also check BiDi :9222 port (if Firefox is up but BiDi socket died, that also needs revive).
probe_process() { pgrep -f 'firefox.*firefox-auto' >/dev/null 2>&1; }
probe_bidi()    { ss -tlnp 2>/dev/null | grep -q ':9222 '; }

# attempt initial bring-up if nothing is running
probe_process || bash "$EIGHT/up-omarchy.sh"

fails=0  # consecutive cycles with Firefox PROCESS gone
while true; do
  if probe_process; then
    fails=0

    # ALIVE (process exists). Opportunistically save tabs — a slow/failed getTree
    # here is just a busy socket, never a recycle trigger.
    resp=$(curl -s -m 12 http://127.0.0.1:4445/command -H 'Content-Type: application/json' \
      -d '{"method":"browsingContext.getTree","params":{}}' 2>/dev/null)
    if printf '%s' "$resp" | grep -q '"contexts"'; then
      printf '%s' "$resp" | python3 -c "
import json,sys
for c in json.load(sys.stdin).get('result',{}).get('contexts',[]):
    u=c.get('url','')
    if not u.startswith('about:') and not u.startswith('chrome:') and u:
        print(u)
" 2>/dev/null > "$TABS_FILE.tmp"
      if [ -s "$TABS_FILE.tmp" ]; then mv "$TABS_FILE.tmp" "$TABS_FILE"; else rm -f "$TABS_FILE.tmp"; fi
    fi

    # FLOW 10 — proactive mem recycle: 8 watches its OWN parent memory and recycles
    # BEFORE the OOM. Only fires when procinfo succeeds AND parent exceeds threshold.
    mem=$(curl -s -m4 "http://127.0.0.1:7070/procinfo?session=fox" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d.get('parent_mem_mb',0))
" 2>/dev/null)
    if [ "${mem:-0}" -gt "${RECYCLE_MB:-4500}" ] 2>/dev/null; then
      echo "[peer-watchdog $(date +%H:%M:%S)] Firefox parent ${mem}MB > ${RECYCLE_MB:-4500}MB -> proactive recycle (FLOW 10)"
      pkill -f "firefox.*firefox-auto" 2>/dev/null; sleep 2
      run_up; sleep 30
      continue
    fi

    # BiDi socket died but process still alive? That's a stale state and needs revive.
    if ! probe_bidi; then
      echo "[peer-watchdog $(date +%H:%M:%S)] Firefox process alive but BiDi :9222 gone -> reviving"
      pkill -f "firefox.*firefox-auto" 2>/dev/null; sleep 2
      run_up; sleep 30
      continue
    fi

    # COLLECTOR liveness — revive a dead collector WITHOUT Firefox churn.
    if ! ss -tlnp 2>/dev/null | grep -q ':7070 '; then
      echo "[peer-watchdog $(date +%H:%M:%S)] collector :7070 down -> reviving (collector-only)"
      SID=$(python3 -c "import json;print(json.load(open('$HOME/.8/gecko.json'))['session_id'])" 2>/dev/null || cat /tmp/claude-1000/sid 2>/dev/null || echo '')
      [ -n "$SID" ] && nohup "$EIGHT/collector/collector" -listen :7070 -brokers "fox=http://127.0.0.1:4445" >/tmp/claude-1000/collector.log 2>&1 &
      sleep 2
    fi

  else
    # Firefox PROCESS is GONE -> genuinely dead. Two consecutive to ride out a
    # momentary pkill/relaunch window, then revive (serialized by run_up's lock).
    fails=$((fails + 1))
    echo "[peer-watchdog $(date +%H:%M:%S)] Firefox process gone ($fails/2)"
    if [ "$fails" -ge 2 ]; then
      echo "[peer-watchdog $(date +%H:%M:%S)] Firefox dead -> reviving via up-omarchy.sh"
      run_up
      fails=0
      sleep 30
    fi
  fi
  sleep 15
done
