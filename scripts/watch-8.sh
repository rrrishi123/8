#!/usr/bin/env bash
# watch-8.sh — OBSERVER-only health watcher for the 8 wire (never acts; the
# watchdog acts). Emits one line per STATE TRANSITION to /tmp/8-watch.log:
# firefox-down, dup up.sh / watchdog count, collector-down, and parent memory
# SUSTAINED >4400MB (two consecutive ticks — the aperture normally oscillates
# 3.5–4.6GB, so a single spike is healthy self-regulation, not danger).
#
# Why this exists as a detached script: session-harness monitors kept getting
# SIGTERM'd (2026-07-27, exit 144 twice); the watching must outlive the session.
# An agent tails /tmp/8-watch.log to relay events; if the tail dies, this keeps
# recording and the tail is re-armed with zero lost events.
#
# Run detached:  nohup bash scripts/watch-8.sh >/dev/null 2>&1 &
# Platform-agnostic: pgrep/curl/jq only — no procps extensions (the etimes lesson).

LOG=/tmp/8-watch.log

# singleton (same pid-owned mkdir-lock pattern as watchdog.sh)
_LOCK=/tmp/8-watch.lockdir
if ! mkdir "$_LOCK" 2>/dev/null; then
  _o=$(cat "$_LOCK/pid" 2>/dev/null)
  if [ -n "$_o" ] && kill -0 "$_o" 2>/dev/null; then exit 0; fi
  rm -rf "$_LOCK"; mkdir "$_LOCK" 2>/dev/null || exit 0
fi
echo $$ > "$_LOCK/pid"
trap 'rm -rf "$_LOCK"' EXIT

echo "$(date +%H:%M:%S) 8-WATCH watcher started (pid $$)" >> "$LOG"

prev="INIT"; hi=0; wdBad=0; upBad=0
while true; do
  ff=$(pgrep -f 'firefox.*ltqa-firefox-deepseek' 2>/dev/null | head -1)
  wc=$(pgrep -f 'bash scripts/watchdog.sh' 2>/dev/null | grep -c .)
  uc=$(pgrep -f 'bash scripts/up.sh' 2>/dev/null | grep -c .)
  mem=$(curl -s -m4 "http://127.0.0.1:7070/procinfo?session=fox" 2>/dev/null | jq -r '.parent_mem_mb // -1' 2>/dev/null); mem=${mem%.*}; [ -z "$mem" ] && mem=-1
  curl -s -m3 http://127.0.0.1:7070/health >/dev/null 2>&1 && col=up || col=DOWN
  if [ "$mem" -gt 4400 ] 2>/dev/null; then hi=$((hi+1)); else hi=0; fi
  # watchdog-count debounce: a spawn attempt is REJECTED by the singleton lock
  # within seconds (by design, e.g. up.sh racing a revive) — the 25s sample can
  # catch it mid-exit. Only a count!=1 that PERSISTS two ticks is a real problem.
  if [ "$wc" != "1" ]; then wdBad=$((wdBad+1)); else wdBad=0; fi
  key=""; probs=""
  [ -z "$ff" ] && { key="${key}F"; probs="$probs FIREFOX-DOWN"; }
  { [ "$wdBad" -ge 2 ]; } 2>/dev/null && { key="${key}W$wc"; probs="$probs watchdogs=$wc(want1, persisted)"; }
  # up.sh debounce: a recycle briefly races two revives before they finish /
  # the wedge-killer trims a stuck one — only a DUP that PERSISTS two ticks is real.
  if { [ "$uc" -gt 1 ]; } 2>/dev/null; then upBad=$((upBad+1)); else upBad=0; fi
  { [ "$upBad" -ge 2 ]; } 2>/dev/null && { key="${key}U"; probs="$probs DUP-up.sh=$uc(persisted)"; }
  [ "$col" = "DOWN" ] && { key="${key}C"; probs="$probs COLLECTOR-DOWN"; }
  { [ "$hi" -ge 2 ]; } 2>/dev/null && { key="${key}M"; probs="$probs parent_mem=${mem}MB SUSTAINED>4400 (recycle imminent)"; }
  if [ -z "$key" ]; then key="ok"; line="healthy — firefox up, 1 watchdog, collector up, parent_mem=${mem}MB"; else line="ALERT:$probs"; fi
  if [ "$key" != "$prev" ]; then echo "$(date +%H:%M:%S) 8-WATCH $line" >> "$LOG"; prev="$key"; fi
  sleep 25
done
