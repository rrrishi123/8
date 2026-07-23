#!/usr/bin/env bash
# up-omarchy.sh — the LINUX full bring-up of the kosaten Firefox peer wire.
# The omarchy counterpart of scripts/up.sh (which is Mac-pathed and does not run here).
#
# Chain: geckodriver(:4444) -> Firefox BiDi(:9222) -> broker(:4445) -> collector(:7070) -> vite(:8088)
# plus: re-open the peer's living tabs (from saved ~/.8-tabs.txt, or hardcoded fallback).
#
# Idempotent-ish: start-auto-firefox.sh recreates a fresh BiDi session each run (kills firefox-auto),
# so this is safe to call on first boot AND as a watchdog revive after a crash.
set -uo pipefail
EIGHT=/home/rishi/Work/8
WEB=$EIGHT/web
LOG=/tmp/claude-1000/up-omarchy.log
export WAYLAND_DISPLAY=${WAYLAND_DISPLAY:-wayland-1} XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR:-/run/user/1000} MOZ_ENABLE_WAYLAND=0
# DISPLAY is required now that Firefox runs under XWayland (MOZ_ENABLE_WAYLAND=0) —
# without it Firefox can't reach the X server and fails to launch. Xwayland = :0.
export DISPLAY=${DISPLAY:-:0}
# node/npm live in mise, not /usr/bin — systemd units don't get the interactive PATH
export PATH="$HOME/.local/share/mise/shims:$PATH"
mkdir -p /tmp/claude-1000 2>/dev/null
say(){ echo "$(date +%H:%M:%S) $*" | tee -a "$LOG"; }
free_mib(){ nvidia-smi --query-gpu=memory.free --format=csv,noheader,nounits 2>/dev/null | tr -d ' '; }
listening(){ ss -ltn 2>/dev/null | grep -q ":$1"; }
cmd(){ curl -sf -m "${2:-20}" -X POST localhost:4445/command -H 'Content-Type: application/json' -d "$1"; }

# The peer's persistent tabs (its living organs) — loaded from saved file if available,
# otherwise the hardcoded fallback. The watchdog saves running URLs to ~/.8-tabs.txt
# every cycle, so after a crash-revive we reopen exactly what was live before.
TABS_FILE="$HOME/.8-tabs.txt"
if [ -s "$TABS_FILE" ]; then
  mapfile -t TABS < <(grep -vE '^about:|^chrome:|^$' "$TABS_FILE")
  say "loaded $((${#TABS[@]})) tabs from $TABS_FILE"
else
  TABS=(
    'http://localhost:8088/'
    'https://excalidraw.com/#room=ede89b517117beeecaa0,n6Knw1KGvicqM2rpSqxWhA'
    'https://chat.deepseek.com/a/chat/s/2948dfbf-9d9e-4fb2-8dd8-044d2a470478'
    'https://claude.ai/new'
    'http://localhost:9901/'
  )
  say "no tabs file — using hardcoded defaults (${#TABS[@]} tabs)"
fi

# 0. the castle world (:9901) — pilot and claude designed it together; pilot
#    watches his own world through this eye. Zero-LLM, one small binary.
listening 9901 || { nohup /home/rishi/Work/pilot/castle -serve :9901 "$HOME/.pilot-castle.jsonl" >/tmp/claude-1000/castle-serve.log 2>&1 & }

# 1. vite cockpit (:8088) — start-auto-firefox.sh does NOT start it.
if listening 8088; then say "vite: already up :8088"; else
  # setsid: vite must never be this script's child — a wait on it wedges the
  # revive serializer forever (the 31h do_wait deadlock of 2026-07-05)
  ( cd "$WEB" && setsid nohup npm run dev >/tmp/claude-1000/vite-8.log 2>&1 </dev/null & )
  for _ in $(seq 1 40); do listening 8088 && break; sleep 0.5; done
  listening 8088 && say "vite: up :8088" || say "vite: FAILED (see /tmp/claude-1000/vite-8.log)"
fi

# 2. VRAM guard — Firefox's EGL needs ~400MB. If the comic 12B (or any model) has eaten the card,
#    briefly stop ollama so firefox can grab its buffers; it then coexists once allocated.
if [ "$(free_mib)" -lt 500 ]; then
  say "vram guard: only $(free_mib)MiB free -> stopping kosaten-ollama briefly"
  docker stop kosaten-ollama >/dev/null 2>&1
  for _ in $(seq 1 16); do [ "$(free_mib)" -gt 5000 ] && break; sleep 0.5; done
  STOPPED_OLLAMA=1
fi

# 3. peer chain (proven launcher): geckodriver -> firefox(firefox-auto) -> broker -> collector + bot-wall.
say "launching peer chain..."
timeout 90 bash "$EIGHT/start-auto-firefox.sh" >>"$LOG" 2>&1
sleep 2
if ! listening 9222; then say "peer FAILED: firefox not up"; [ "${STOPPED_OLLAMA:-}" = 1 ] && docker start kosaten-ollama >/dev/null 2>&1; exit 1; fi
for _ in $(seq 1 20); do listening 4445 && break; sleep 0.5; done
listening 4445 || say "WARN: broker :4445 not up (node missing from PATH?) — tabs will fail"
say "peer up (BiDi :9222$(listening 4445 && echo ', broker :4445')$(listening 7070 && echo ', collector :7070'))"

# 4. reopen the living tabs — IDEMPOTENT. "fresh session => none present" is
#    false after an XWayland graceful recycle: session-restore revives tabs, and
#    ~/.8-tabs.txt (saved from the live tree) can carry duplicates — so blind
#    creates compounded one extra cockpit tab per recycle. Dedupe the wanted
#    list and only create tabs whose exact URL is not already open.
OPEN_URLS=$(cmd '{"method":"browsingContext.getTree","params":{}}' | python3 -c "
import json,sys
try:
    for c in json.load(sys.stdin).get('result',{}).get('contexts',[]):
        print(c.get('url',''))
except Exception:
    pass" 2>/dev/null)
declare -A _SEEN_TAB
for url in "${TABS[@]}"; do
  [ -n "${_SEEN_TAB[$url]:-}" ] && continue
  _SEEN_TAB[$url]=1
  if printf '%s\n' "$OPEN_URLS" | grep -qxF "$url"; then say "  tab exists: $url"; continue; fi
  ctx=$(cmd '{"method":"browsingContext.create","params":{"type":"tab"}}' | python3 -c "import json,sys;print(json.load(sys.stdin)['result']['context'])" 2>/dev/null)
  [ -z "$ctx" ] && { say "  tab create failed: $url"; continue; }
  cmd "{\"method\":\"browsingContext.navigate\",\"params\":{\"context\":\"$ctx\",\"url\":\"$url\",\"wait\":\"none\"}}" >/dev/null
  say "  tab: $url"
  sleep 3
done

# 5. restore ollama if we stopped it (kosaten reloads its models on demand).
[ "${STOPPED_OLLAMA:-}" = 1 ] && { docker start kosaten-ollama >/dev/null 2>&1; say "kosaten-ollama restarted"; }
say "WIRE UP. free=$(free_mib)MiB"
