#!/usr/bin/env bash
# Replay: gently scroll the DeepSeek chat tab in the already-running shared Firefox,
# FIRED THROUGH THE WITNESS (8 collector :7070) so the reafference rides back:
# the same response carries X-8-Witness / X-8-Ledger headers proving 8 saw it,
# and the action is appended to 8's replayable ledger (GET /requests).
#
# No new browser, no new session. Addresses the existing DeepSeek tab by context id.
# Usage: ./replay-deepseek-scroll-witnessed.sh
# Requires: curl, jq
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SPEC="$DIR/deepseek-scroll.command.json"

WITNESS="http://127.0.0.1:7070"
SESSION="fox"
BODY="$(jq -c '.http.body' "$SPEC")"   # {method:script.evaluate, params:{target:{context...}, expression:...}}

echo "Firing scroll THROUGH the witness -> $WITNESS/run?session=$SESSION"
HDRS="$(mktemp)"
RESP="$(curl -s -D "$HDRS" --max-time 30 -X POST "$WITNESS/run?session=$SESSION" \
          -H 'Content-Type: application/json' --data-binary "$BODY")"

echo
echo "--- PROOF-OF-WITNESS (reafference headers stamped by 8) ---"
grep -Ei '^X-8-' "$HDRS" || { echo "no witness headers — is the collector on :7070?"; exit 1; }
rm -f "$HDRS"

echo
echo "--- OP RESULT (scroll self-confirmation: delta>0 => it moved) ---"
INNER="$(printf '%s' "$RESP" | jq -r '.result.result.value // empty')"
[ -n "$INNER" ] && echo "$INNER" || { echo "raw: $RESP"; exit 1; }
DELTA="$(printf '%s' "$INNER" | jq -r '.delta // 0')"
[ "${DELTA:-0}" -gt 0 ] 2>/dev/null \
  && echo "CONFIRMED: scrolled ${DELTA}px" \
  || echo "NOTE: delta=${DELTA} (virtualized list may be pinned at bottom)"

echo
echo "--- INDEPENDENT RECEIPT (read the frame back from 8's ledger) ---"
LEDGER="$(printf '%s' "$RESP" | { grep -o '' >/dev/null 2>&1 || true; })"  # noop; ledger id came via header
curl -s --max-time 10 "$WITNESS/requests?n=3" \
  | jq -c '.requests[] | select(.method=="script.evaluate") | {frame:.id, ts:.ts, method:.method, status:.status, session:.session, replayable:.replayable}' \
  | head -1
