#!/usr/bin/env bash
# watchdog.sh — keep the channel Firefox alive. The hard lesson: judge Firefox by
# its PROCESS, not by whether the BiDi socket answers. Under captureScreenshot
# stream load the single BiDi socket saturates and getTree times out for many
# seconds — a BUSY socket, NOT a dead Firefox. Recycling on socket-unresponsiveness
# was a relentless false-recycle storm (and two concurrent up.sh fighting left
# Firefox down entirely). So: recycle only when the process is GONE, serialize
# revives with a lock, and keep getTree only for opportunistic tab-saving.
#
# Run detached:  nohup bash scripts/watchdog.sh >/tmp/watchdog.log 2>&1 &
cd "$(dirname "$0")/.." || exit 1
TABS_FILE="${TABS_FILE:-$HOME/.8-tabs.txt}"

# SINGLETON (pid-owned mkdir-lock, dead-owner stolen) — exactly one watchdog.
# up.sh spawns a watchdog per revive and a detached one can outlive for days;
# without this guard they pile up (2 watchdogs racing revives, 2026-07-27).
# mkdir is atomic (no TOCTOU); flock is unusable (children inherit the fd).
_WD_LOCK=/tmp/8-watchdog.lockdir
if ! mkdir "$_WD_LOCK" 2>/dev/null; then
  _o=$(cat "$_WD_LOCK/pid" 2>/dev/null)
  if [ -n "$_o" ] && kill -0 "$_o" 2>/dev/null; then
    echo "[watchdog] another watchdog (pid $_o) already holds the lock — exiting (singleton)"; exit 0
  fi
  rm -rf "$_WD_LOCK"; mkdir "$_WD_LOCK" 2>/dev/null || { echo "[watchdog] lock race lost — exiting"; exit 0; }
fi
echo $$ > "$_WD_LOCK/pid"
trap 'rm -rf "$_WD_LOCK"' EXIT

echo "[watchdog] started $(date +%H:%M:%S) — process-based liveness — tab store: $TABS_FILE"

# elapsed seconds for a pid — PLATFORM-AGNOSTIC. `ps -o etimes=` (whole seconds)
# is a GNU/procps extension ABSENT on macOS/BSD: there it errored every cycle
# ("integer expression expected") so the wedge-killer below silently never fired
# and wedged up.sh piled up (2 up.sh + 2 watchdogs, 2026-07-27). `etime`
# ([[DD-]HH:]MM:SS) is POSIX and works on Linux AND macOS; parse it ourselves.
age_secs() {
  local e; e=$(ps -o etime= -p "$1" 2>/dev/null | tr -d ' ')
  [ -z "$e" ] && { echo 0; return; }
  local days=0 hms="$e"
  case "$e" in *-*) days=${e%%-*}; hms=${e#*-};; esac
  local h=0 m=0 s=0 IFS=:; set -- $hms
  case $# in 3) h=$1 m=$2 s=$3;; 2) m=$1 s=$2;; 1) s=$1;; esac
  echo $(( 10#${days:-0}*86400 + 10#${h:-0}*3600 + 10#${m:-0}*60 + 10#${s:-0} ))
}

# never run two up.sh at once — concurrent revives kill each other's geckodriver
# session and leave Firefox down (the exact mess that desynced the wire for hours).
run_up() {
  # kill a WEDGED revive first (2026-07-25: a stuck up.sh held the field for
  # hours while the watchdog skipped 'already running' — deadlocked revives)
  for pid in $(pgrep -f 'bash scripts/up.sh' 2>/dev/null); do
    age=$(age_secs "$pid")
    if [ "${age:-0}" -gt 300 ]; then
      echo "[watchdog $(date +%H:%M:%S)] up.sh pid $pid wedged (${age}s) — killing stale revive"
      kill "$pid" 2>/dev/null; sleep 1; kill -9 "$pid" 2>/dev/null
      rm -rf /tmp/8-up.lockdir
    fi
  done
  if pgrep -f 'bash scripts/up.sh' >/dev/null 2>&1; then
    echo "[watchdog $(date +%H:%M:%S)] up.sh already running — skip (no concurrent revives)"
    return
  fi
  bash scripts/up.sh >/tmp/up-watchdog.log 2>&1
}

fails=0  # consecutive cycles with the Firefox PROCESS gone (process death, not socket silence)
while true; do
  if pgrep -f 'firefox.*ltqa-firefox-deepseek' >/dev/null 2>&1; then
    fails=0
    # ALIVE (process exists). Opportunistically save tabs — a slow/failed getTree
    # here is just a busy socket, never a recycle trigger.
    resp=$(curl -s -m 12 http://127.0.0.1:4445/command -H 'Content-Type: application/json' \
      -d '{"method":"browsingContext.getTree","params":{}}' 2>/dev/null)
    if printf '%s' "$resp" | grep -q '"contexts"'; then
      # INVARIANT (2026-07-24): tabs are conserved — observation may only ADD to
      # the ledger, never remove. A snapshot-overwrite forgot tabs two ways:
      # (a) mid-revive/degraded trees clobbered the ledger down to 2 entries;
      # (b) PARKED tabs (fair aperture, discardBrowser) drop out of the BiDi
      #     tree entirely, so every healthy save silently dropped them.
      # Closing a tab is a deliberate act (human edits the ledger / explicit
      # close), never an inference from absence.
      printf '%s' "$resp" | jq -r '.result.contexts[].url // empty' 2>/dev/null \
        | grep -vE '^about:|^chrome:|^$' > "$TABS_FILE.obs"
      if [ -s "$TABS_FILE.obs" ]; then
        cat "$TABS_FILE" "$TABS_FILE.obs" 2>/dev/null | awk 'NF && !seen[$0]++' > "$TABS_FILE.tmp"
        mv "$TABS_FILE.tmp" "$TABS_FILE"
      fi
      rm -f "$TABS_FILE.obs"
    fi

    # FLOW 10 — proactive mem recycle: 8 watches its OWN parent memory and recycles
    # BEFORE the OOM. Only fires when procinfo succeeds AND parent exceeds threshold.
    mem=$(curl -s -m4 "http://127.0.0.1:7070/procinfo?session=fox" | jq -r '.parent_mem_mb // 0' 2>/dev/null)
    if [ "${mem:-0}" -gt "${RECYCLE_MB:-4500}" ] 2>/dev/null; then
      echo "[watchdog $(date +%H:%M:%S)] Firefox parent ${mem}MB > ${RECYCLE_MB:-4500}MB -> proactive recycle (FLOW 10)"
      pkill -f "firefox.*ltqa-firefox-deepseek" 2>/dev/null; sleep 2
      run_up; sleep 30
    fi

    # COLLECTOR liveness — revive a dead collector WITHOUT Firefox churn.
    if ! lsof -ti :7070 >/dev/null 2>&1; then
      echo "[watchdog $(date +%H:%M:%S)] collector :7070 down -> reviving (collector-only)"
      [ -x collector/collector ] || ( cd collector && go build -o collector . )
      SID=$(ps aux | grep '[c]hannel -ws' | grep 4445 | grep -o 'session/[0-9a-f-]*' | head -1 | cut -d/ -f2)
      BRK="fox=http://127.0.0.1:4445"
      lsof -ti :4446 >/dev/null 2>&1 && BRK="$BRK,chrome=http://127.0.0.1:4446"
      nohup collector/collector -listen :7070 -brokers "$BRK" -gecko "http://127.0.0.1:4444/session/$SID" >/tmp/collector-8.log 2>&1 &
    fi
  else
    # Firefox PROCESS is GONE -> genuinely dead. Two consecutive to ride out a
    # momentary pkill/relaunch window, then revive (serialized by run_up's lock).
    fails=$((fails + 1))
    echo "[watchdog $(date +%H:%M:%S)] Firefox process gone ($fails/2)"
    if [ "$fails" -ge 2 ]; then
      echo "[watchdog $(date +%H:%M:%S)] Firefox dead -> reviving via up.sh"
      run_up
      fails=0
      sleep 30
    fi
  fi
  sleep 15
done
