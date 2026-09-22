#!/usr/bin/env bash
# self-prompt.sh — ledger autonomy loop. NO model.
#
# The intelligence is the agent's own forward planning: while doing step n-1,
# a Claude Code agent plans step n and appends it as ONE line to its own
# ledger at ~/.8/ledger/<pane>.txt. This loop, when the pane is IDLE, pops the
# oldest queued line and sends it back into the pane.
#
# If the ledger is EMPTY, the agent is not left silent — it gets a default
# self-check nudge (did you plan ahead / what were you doing / how have things
# changed / glance at your siblings) so the fleet never stops flowing. The
# agent then does the next useful thing and re-queues. Idle-gated — never
# interrupts a live turn.
#
# One pass per invocation. Loop:  while true; do self-prompt.sh; sleep 240; done
set -uo pipefail
LEDGER="${LEDGER_DIR:-$HOME/.8/ledger}"
PANES="${SELF_PROMPT_PANES:-%8 %9 %10 %11}"
SPEC="$HOME/Desktop/repos/flutter-kaleidoscope/specimen"
idle() { ! tmux capture-pane -t "$1" -p 2>/dev/null | grep -q 'esc to interrupt'; }
# plan/usage-limit gate: while a pane shows a limit/reset state, back off (don't
# spam). The 240s poll keeps checking; once the ~4h limit window clears, the next
# pass delivers its next line automatically — the fleet self-resumes on reset.
limited() { tmux capture-pane -t "$1" -p 2>/dev/null | grep -qiE 'usage limit|resets? (at|in)|rate limit|out of (credit|tokens)|upgrade to increase'; }

mkdir -p "$LEDGER"

# load backpressure: don't add fleet work while the machine is thrashing.
# Several panes each firing a heavy render/build at once spikes load into the
# 100s on a 10-core box and STARVES everything (wedged Blender renders, etc.).
# When 1-min load exceeds the cap, back off the whole pass; the 240s cadence
# re-checks and resumes automatically once the machine recovers.
LOAD1=$(uptime | awk '{print int($(NF-2))}')
LOAD_CAP="${SELF_PROMPT_LOAD_CAP:-30}"
if [ "${LOAD1:-0}" -gt "$LOAD_CAP" ]; then
  echo "$(date +%H:%M) load ${LOAD1} > ${LOAD_CAP} — backing off ALL panes (thrash guard; auto-resumes when load drops)"
  exit 0
fi

# BUDGET gate (2026-09-16): the fleet self-drives ONLY while the 5h window is in
# RESEARCH phase (<50%, /budget's own verdict). Above the ceiling it CONSERVES —
# the loop backs off every pane and auto-resumes when the window resets. This is
# the operator's rule: "keep doing work while 5h<50%; decide it yourself."
PHASE=$(curl -s -m3 http://127.0.0.1:7070/budget 2>/dev/null | grep -o '"phase":"[a-z]*"' | head -1 | cut -d\" -f4)
if [ "$PHASE" = "conserve" ]; then
  echo "$(date +%H:%M) budget CONSERVE (5h >= ceiling) — backing off ALL panes; auto-resumes at reset"
  exit 0
fi

for p in $PANES; do
  # hold gate: a pane that has drained its queue and is legitimately waiting on
  # a HUMAN decision drops a $p.hold marker. Skip it — don't self-nag every
  # cycle (that just burns tokens re-verifying a settled state). Real human
  # messages still reach the pane directly; only the loop's auto-nudge is
  # suppressed. The pane (or the human) removes the marker to resume flowing.
  if [ -f "$LEDGER/$p.hold" ]; then
    echo "$(date +%H:%M) $p on HOLD ($(head -c 80 "$LEDGER/$p.hold" 2>/dev/null)) — skip; rm the marker to resume"
    continue
  fi
  idle "$p" || { echo "$(date +%H:%M) $p busy — skip"; continue; }
  limited "$p" && { echo "$(date +%H:%M) $p plan-limited — backing off (auto-resumes on reset)"; continue; }
  f="$LEDGER/$p.txt"
  if [ -s "$f" ]; then
    next=$(head -1 "$f")
    tail -n +2 "$f" > "$f.tmp" 2>/dev/null && mv "$f.tmp" "$f"   # pop delivered line
    src="ledger"
  else
    # empty ledger -> default self-check so the agent never goes silent
    next="Self-check (plan ahead): what were you doing, and how have things changed since? Use http-mcp (never curl) to read 127.0.0.1:7070/work/by-pane and 127.0.0.1:7070/budget, skim your own recent work and your siblings' panes, then do OR dispatch the single next useful step toward v0.0.2 release-readiness (the audit: committed-binary boot fix, tag/branch split, CI to all arms, wire-contract module). Append your next planned step as one line to $f. You have autonomy while 5h<50%."
    src="nudge"
  fi
  [ -z "${next// /}" ] && continue
  epoch=$(date +%s)
  tagged="[8-loop #$epoch · $src] $next"
  echo "$(date +%H:%M) $p [$src] <- ${next:0:110}"
  tmux send-keys -t "$p" "$tagged"; sleep 0.3; tmux send-keys -t "$p" Enter
done
