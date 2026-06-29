#!/usr/bin/env bash
# watchdog.sh — the channel Firefox crashes periodically (memory). Instead of
# hand-cranking up.sh each time, this keeps it alive: every 15s it asks the
# broker to reach Firefox (browsingContext.getTree); if it cannot (Firefox is
# gone -> broken pipe / no contexts), it brings the whole wire back via up.sh.
#
# Run detached:  nohup bash scripts/watchdog.sh >/tmp/watchdog.log 2>&1 &
cd "$(dirname "$0")/.." || exit 1
TABS_FILE="${TABS_FILE:-$HOME/.8-tabs.txt}"
echo "[watchdog] started $(date +%H:%M:%S) — tab store: $TABS_FILE"
while true; do
  resp=$(curl -s -m 5 http://127.0.0.1:4445/command -H 'Content-Type: application/json' \
    -d '{"method":"browsingContext.getTree","params":{}}' 2>/dev/null)
  if printf '%s' "$resp" | grep -q '"contexts"'; then
    # ALIVE: auto-save the open tabs — the session-store the channel Firefox
    # lacks across its memory crashes. up.sh restores from this on the next revive.
    printf '%s' "$resp" | jq -r '.result.contexts[].url // empty' 2>/dev/null \
      | grep -vE '^about:|^chrome:|^$' > "$TABS_FILE.tmp"
    if [ -s "$TABS_FILE.tmp" ]; then mv "$TABS_FILE.tmp" "$TABS_FILE"; else rm -f "$TABS_FILE.tmp"; fi
  else
    echo "[watchdog $(date +%H:%M:%S)] Firefox unreachable -> reviving via up.sh"
    bash scripts/up.sh >/tmp/up-watchdog.log 2>&1
    sleep 30   # cooldown: let the fresh session settle before probing again
  fi
  sleep 15
done
