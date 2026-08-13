#!/usr/bin/env bash
# OUR restore — rebuilds the tmux fleet from eight.db, with the flag, no claude-deck.
#
# Identity comes from our own store: windows(name,layout) + panes(cwd,claude_uuid).
# The %N handle is deliberately NOT restored — it's re-minted by the new server; we
# rebind by (session,win,pane) instead. Every claude pane relaunches WITH
# --dangerously-skip-permissions (the flag that got dropped last reboot), and each
# resume-uuid is used at most once (fixes the %0/%me 12444bc8 duplicate).
#
# Usage:
#   restore-8.sh                      # rebuild kosaten1 (REFUSES if it already exists)
#   SRC=kosaten1 DST=kosaten1-test MODE=dry restore-8.sh   # structural dry-run into a scratch name
set -euo pipefail

DB="${DB:-$HOME/.8/eight.db}"
SRC="${SRC:-kosaten1}"        # session rows to read
DST="${DST:-$SRC}"           # session name to build
MODE="${MODE:-live}"         # live | dry
FLAG="--dangerously-skip-permissions"

q(){ sqlite3 -noheader -separator $'\t' "$DB" "$1"; }

if tmux has-session -t "=$DST" 2>/dev/null; then
  echo "restore-8: session '$DST' already exists — refusing (restore is additive)." >&2
  exit 1
fi

declare -A USED_UUID   # dedup resume-uuids

wins=$(q "select win from windows where session='$SRC' order by win")
first=1
for w in $wins; do
  wname=$(q "select name from windows where session='$SRC' and win=$w")
  layout=$(q "select layout from windows where session='$SRC' and win=$w")
  # panes for this window, ordered
  mapfile -t prows < <(q "select pane,cwd,claude_uuid,cmd from panes where session='$SRC' and win=$w order by pane")
  # first pane cwd
  IFS=$'\t' read -r p0 cwd0 uuid0 cmd0 <<<"${prows[0]}"
  cwd0="${cwd0:-$HOME}"
  if [ $first -eq 1 ]; then
    tmux new-session -d -s "$DST" -n "$wname" -c "$cwd0"
    first=0
  else
    tmux new-window -d -t "$DST:$w" -n "$wname" -c "$cwd0"
  fi
  target="$DST:$w"
  # additional panes
  for ((i=1;i<${#prows[@]};i++)); do
    IFS=$'\t' read -r pi cwdi uuidi cmdi <<<"${prows[$i]}"
    tmux split-window -t "$target" -c "${cwdi:-$HOME}"
  done
  [ ${#prows[@]} -gt 1 ] && [ -n "$layout" ] && tmux select-layout -t "$target" "$layout" || true
  # launch each pane's session
  idx=0
  for row in "${prows[@]}"; do
    IFS=$'\t' read -r pi cwdi uuidi cmdi <<<"$row"
    pt="$target.$idx"; idx=$((idx+1))
    [ -z "$uuidi" ] && continue                      # ssh/nvim/shell: leave a plain shell
    [ -n "${USED_UUID[$uuidi]:-}" ] && continue      # dedup: this session already placed
    USED_UUID[$uuidi]=1
    if [ "$MODE" = dry ]; then
      tmux send-keys -t "$pt" "printf 'RESTORE claude --resume %s $FLAG\\n' $uuidi" Enter
    else
      tmux send-keys -t "$pt" "claude --resume $uuidi $FLAG" Enter
    fi
  done
done
echo "restore-8: built '$DST' from '$SRC' (mode=$MODE, flag on, uuids deduped)"
