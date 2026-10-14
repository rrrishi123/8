#!/usr/bin/env bash
# codex-resume — the self-resume supervisor for a limit-blocked mind.
#
# The failure this fixes: Codex hits its 5h usage limit, its TUI freezes at the
# "try again at HH:MM" screen, and NOTHING wakes it when the window resets — so
# it sat idle for 2.5 days until the operator noticed. The four-system holds the
# state across the quiet part, but nobody was turning the loop over. This does
# for a sleeping mind what the operator's return did: watch the pane, and when it
# is limit-blocked AND has budget again, restart it and hand it back the ledger.
#
#   nohup ./scripts/codex-resume.sh >/tmp/codex-resume.log 2>&1 &
set -uo pipefail
PANE="${CODEX_PANE:-%19}"
POLL="${POLL:-300}"                              # check every 5 min
COLLECTOR="${COLLECTOR:-http://127.0.0.1:7070}"
TASK="${CODEX_TASK:-You are Codex Astra resuming after a usage-limit reset. Continue your queue THROUGH THE WIRE (http-mcp http_request): GET ${COLLECTOR}/work?status=todo; do the coding/infra items assigned to codex (finish #1196 Lever 3 the in-Colima runtime image if still partial); record each via POST ${COLLECTOR}/work by=codex; announce each in this pane. Commit to release/v0.0.2 is fine; #1138 forbids only main-merge/tags/publish.}"

tb=$(command -v tmux) || { echo "no tmux"; exit 1; }

blocked(){ "$tb" capture-pane -p -t "$PANE" 2>/dev/null | grep -qiE 'usage limit|try again at|hit your .*limit'; }

# reset_passed — the codex TUI advertises its OWN reset ("try again at 9:21 PM").
# Once that moment is in the past the 5h window HAS reset, even when /budget's
# codex sensor is dark (5h utilization null) — which is precisely when the
# util-based check below cannot fire. This is the gap that left codex frozen for
# 2.5 days: a passed reset time is itself the budget signal. Guarded to a reset
# in the last 6h so a "tomorrow 2 AM" parse (which date -j reads as today, hours
# in the past) can't false-fire and relaunch into a still-empty window.
reset_passed(){
  local scr t now rst
  scr=$("$tb" capture-pane -p -t "$PANE" 2>/dev/null)
  t=$(printf '%s\n' "$scr" | grep -oiE 'try again at [0-9]{1,2}:[0-9]{2} ?[AP]M' | tail -1 | sed -E 's/.*[Aa][Tt] //')
  [ -n "$t" ] || return 1
  now=$(date +%s)
  rst=$(date -j -f '%I:%M %p' "$t" +%s 2>/dev/null) || return 1
  # date -j fills in TODAY, but the advertised time may be from before midnight:
  # a bare clock time that lands in the future must have been YESTERDAY's reset,
  # so roll it back a day and read it as the most-recent past occurrence.
  [ "$rst" -gt "$now" ] && rst=$((rst - 86400))
  [ "$rst" -le "$now" ] && [ $((now - rst)) -le 21600 ]
}

has_budget(){
  # a reset the codex TUI itself advertises, its advertised reset time having
  # passed, or a low 5h utilization from /budget
  "$tb" capture-pane -p -t "$PANE" 2>/dev/null | grep -qi 'usage limit reset available' && return 0
  reset_passed && return 0
  local u; u=$(curl -s -m4 "$COLLECTOR/budget" 2>/dev/null | jq -r '.providers.codex.windows["5h"].utilization // empty' 2>/dev/null)
  [ -n "$u" ] && awk -v u="$u" 'BEGIN{exit !(u<0.5)}'
}

restart_codex(){
  "$tb" send-keys -t "$PANE" C-c; sleep 1; "$tb" send-keys -t "$PANE" C-c; sleep 1   # exit the frozen TUI
  "$tb" send-keys -t "$PANE" -l "codex --dangerously-bypass-approvals-and-sandbox -m gpt-6-astra \"$TASK\""
  "$tb" send-keys -t "$PANE" Enter; sleep 9
  "$tb" send-keys -t "$PANE" "3"; sleep 0.5; "$tb" send-keys -t "$PANE" Enter        # clear hook-trust (safe: no hooks)
}

echo "[codex-resume] watching $PANE every ${POLL}s — wakes codex when limit-blocked + budget returns"
while :; do
  if blocked; then
    if has_budget; then
      echo "[codex-resume $(date +%H:%M)] codex limit-blocked AND budget available -> restarting"
      restart_codex
      sleep 120
    else
      echo "[codex-resume $(date +%H:%M)] codex limit-blocked, still no budget -> waiting"
    fi
  fi
  sleep "$POLL"
done
