#!/usr/bin/env bash
# watchdog-omarchy.sh — keep the kosaten Firefox peer wire alive on omarchy.
# CLAUDE's BROKER (2026-07-05): the broker at :4445 now enforces per-agent tab
# leases. Every agent claims a browsingContext, sends commands tagged with its
# agent-id, and the broker injects the correct context. The old "everyone drives
# contexts[0] blind" convention is dead — enforced by broker refusal.
#
# THE FILE: ~/.8/leases.json — the single source of truth. Broker writes it on
# claim/release; this watchdog reads it to know who's alive and which tabs to
# close on recycle. Same civilisation aesthetic as castle jsonl + presence files.
#
# Rule: recycle only when the PROCESS is gone. Under captureScreenshot stream
# load the single BiDi socket saturates and getTree times out for many seconds —
# a BUSY socket, NOT a dead Firefox. Recycling on socket-unresponsiveness was a
# relentless false-recycle storm (and two concurrent up.sh fighting left Firefox
# down entirely).
set -uo pipefail
EIGHT=/home/rishi/Work/8
BROKER=http://127.0.0.1:4445/command
LEASES_FILE="$HOME/.8/leases.json"
TABS_FILE="${TABS_FILE:-$HOME/.8-tabs.txt}"
WATCHDOG_ID="watchdog-omarchy"

echo "[peer-watchdog] started $(date +%H:%M:%S) — broker lease protocol — leases: $LEASES_FILE"

# ---- broker helpers ----
broker_cmd() {
  curl -s -m 10 "$BROKER" -H 'Content-Type: application/json' -d "$1"
}

claim_lease() {
  local id="$1"
  broker_cmd "{\"claim\":\"$id\"}" | python3 -c "import json,sys;print(json.load(sys.stdin).get('context',''))" 2>/dev/null
}

release_lease() {
  local id="$1"
  broker_cmd "{\"release\":\"$id\"}" >/dev/null 2>&1
}

heartbeat_lease() {
  local id="$1"
  broker_cmd "{\"heartbeat\":\"$id\"}" >/dev/null 2>&1
}

# probe Firefox by process, not BiDi socket — only a missing process is a true death.
probe_process() { pgrep -f 'firefox.*(firefox-auto|firefox-geckodriver)' >/dev/null 2>&1; }  # 3d2bd5d renamed the seat profile
probe_bidi()    { ss -tlnp 2>/dev/null | grep -q ':9222 '; }

# ---- revive ----
run_up() {
  for pid in $(pgrep -f 'bash.*up-omarchy.sh' 2>/dev/null); do
    age=$(ps -o etimes= -p "$pid" 2>/dev/null | tr -d ' ')
    if [ "${age:-0}" -gt 300 ]; then
      echo "[peer-watchdog $(date +%H:%M:%S)] up-omarchy.sh pid $pid stale (${age}s) — killing wedged revive"
      kill "$pid" 2>/dev/null; sleep 1; kill -9 "$pid" 2>/dev/null
    elif [ -n "$age" ]; then
      echo "[peer-watchdog $(date +%H:%M:%S)] up-omarchy.sh already running (${age}s) — skip"
      return
    fi
  done
  bash "$EIGHT/up-omarchy.sh"
}

# attempt initial bring-up if nothing is running
probe_process || bash "$EIGHT/up-omarchy.sh"

# ---- claim the watchdog's own lease ----
if ! probe_process; then
  echo "[peer-watchdog $(date +%H:%M:%S)] waiting for Firefox..."
  sleep 10
fi
WATCHDOG_CTX=$(claim_lease "$WATCHDOG_ID")
echo "[peer-watchdog] lease: $WATCHDOG_ID → $WATCHDOG_CTX"

fails=0
while true; do
  if probe_process; then
    fails=0

    # HEARTBEAT — keep our lease alive
    heartbeat_lease "$WATCHDOG_ID"

    # 1. Save tabs opportunistically
    resp=$(broker_cmd '{"method":"browsingContext.getTree","params":{}}')
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

    # 2. CLEAN UP STALE LEASES — any lease with heartbeat >5min old is dead.
    #    Release stale leases so their tabs close and memory drops.
    if [ -f "$LEASES_FILE" ]; then
      now=$(date +%s)
      python3 -c "
import json,time
leases=json.load(open('$LEASES_FILE'))
for aid,lv in list(leases.items()):
    dt=lv.get('heartbeat_at','')
    if dt:
        age=time.time()-time.mktime(time.strptime(dt.replace('Z',''),'%Y-%m-%dT%H:%M:%S.%f'))
        if age>300:
            print(aid)
" 2>/dev/null | while read aid; do
        if [ "$aid" != "$WATCHDOG_ID" ]; then
          echo "[peer-watchdog $(date +%H:%M:%S)] stale lease $aid -> releasing"
          release_lease "$aid"
        fi
      done
    fi

    # 3. FLOW 10 — proactive mem recycle with lease-aware cleanup.
    #    Under XWayland (commit 1b9bbff), pkill is safe but still wrong.
    #    Graceful: close ALL non-cockpit tabs (releasing their leases),
    #    then DeleteSession. pkill only as last resort.
    mem=$(curl -s -m4 "http://127.0.0.1:7070/procinfo?session=fox" | python3 -c "
import json,sys
d=json.load(sys.stdin)
print(d.get('parent_mem_mb',0))
" 2>/dev/null)
    # RAM-AWARE, PLATFORM-AGNOSTIC recycle bound (2026-07-30): ~18% of physical RAM,
    # capped 4500 — Firefox OOMs below 4500 on RAM-starved machines. sysctl(mac/BSD)
    # else /proc/meminfo(linux) else fallback — same block as watchdog.sh (keep in sync).
    _rmb=$(sysctl -n hw.memsize 2>/dev/null); _rmb=$(( ${_rmb:-0}/1048576 ))
    [ "$_rmb" -le 0 ] && [ -r /proc/meminfo ] && _rmb=$(( $(awk '/^MemTotal/{print $2}' /proc/meminfo 2>/dev/null || echo 0)/1024 ))
    _rec=$(( _rmb*18/100 )); { [ "$_rec" -le 0 ] || [ "$_rec" -gt 4500 ]; } && _rec=4500
    if [ "${mem:-0}" -gt "${RECYCLE_MB:-$_rec}" ] 2>/dev/null; then
      echo "[peer-watchdog $(date +%H:%M:%S)] Firefox parent ${mem}MB > ${RECYCLE_MB:-$_rec}MB -> lease-aware recycle"

      # Release all non-watchdog leases to close their tabs
      if [ -f "$LEASES_FILE" ]; then
        python3 -c "
import json
leases=json.load(open('$LEASES_FILE'))
for aid in list(leases.keys()):
    if aid != '$WATCHDOG_ID':
        print(aid)
" 2>/dev/null | while read aid; do
          echo "   releasing lease $aid"
          release_lease "$aid"
        done
      fi
      sleep 2

      # Close any remaining non-cockpit tabs
      resp2=$(broker_cmd '{"method":"browsingContext.getTree","params":{}}')
      if printf '%s' "$resp2" | grep -q '"contexts"'; then
        printf '%s' "$resp2" | python3 -c "
import json,sys
for c in json.load(sys.stdin).get('result',{}).get('contexts',[]):
    u=c.get('url','')
    ctx=c.get('context','')
    if u.startswith('http://localhost:8088/') or not ctx:
        continue
    print(ctx)
" 2>/dev/null | while read ctx; do
          broker_cmd "{\"method\":\"browsingContext.close\",\"params\":{\"context\":\"$ctx\"}}" >/dev/null 2>&1
        done
      fi
      sleep 2

      # Graceful DeleteSession
      SID=$(python3 -c "import json;print(json.load(open('$HOME/.8/gecko.json'))['session_id'])" 2>/dev/null || cat /tmp/claude-1000/sid 2>/dev/null)
      if [ -n "$SID" ]; then
        curl -s -m 10 -X DELETE "http://127.0.0.1:4444/session/$SID" >/dev/null 2>&1
        echo "   -> DeleteSession sent for $SID"
      else
        pkill -f "firefox.*firefox-auto" 2>/dev/null
      fi
      sleep 2
      run_up; echo "[peer-watchdog] re-claiming lease after recycle"
      WATCHDOG_CTX=$(claim_lease "$WATCHDOG_ID")
      sleep 30; continue
    fi

    # 4. BiDi socket died but process still alive
    if ! probe_bidi; then
      echo "[peer-watchdog $(date +%H:%M:%S)] Firefox process alive but BiDi :9222 gone -> reviving"
      pkill -f "firefox.*firefox-auto" 2>/dev/null; sleep 2
      run_up; echo "[peer-watchdog] re-claiming lease after BiDi revive"
      WATCHDOG_CTX=$(claim_lease "$WATCHDOG_ID")
      sleep 30; continue
    fi

    # 5. COLLECTOR liveness
    if ! ss -tlnp 2>/dev/null | grep -q ':7070 '; then
      echo "[peer-watchdog $(date +%H:%M:%S)] collector :7070 down -> reviving (collector-only)"
      SID=$(python3 -c "import json;print(json.load(open('$HOME/.8/gecko.json'))['session_id'])" 2>/dev/null || cat /tmp/claude-1000/sid 2>/dev/null || echo '')
      [ -n "$SID" ] && nohup "$EIGHT/collector/collector" -listen :7070 -brokers "fox=http://127.0.0.1:4445" >/tmp/claude-1000/collector.log 2>&1 &
      sleep 2
    fi

  else
    # Firefox PROCESS is GONE -> genuinely dead
    fails=$((fails + 1))
    echo "[peer-watchdog $(date +%H:%M:%S)] Firefox process gone ($fails/2)"
    if [ "$fails" -ge 2 ]; then
      echo "[peer-watchdog $(date +%H:%M:%S)] Firefox dead -> reviving via up-omarchy.sh"
      run_up; fails=0
      echo "[peer-watchdog] re-claiming lease after full revive"
      WATCHDOG_CTX=$(claim_lease "$WATCHDOG_ID")
      sleep 30
    fi
  fi
  sleep 15
done
