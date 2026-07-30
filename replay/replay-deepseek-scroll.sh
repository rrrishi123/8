#!/usr/bin/env bash
# Replay: gently scroll the DeepSeek chat tab down in the already-running shared Firefox.
# Uses the http-mcp BiDi broker (CALL atom) — no new browser, no new session.
# Reads the exact wire payload from deepseek-scroll.command.json and re-sends it.
#
# Usage: ./replay-deepseek-scroll.sh
# Requires: curl, jq

set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SPEC="$DIR/deepseek-scroll.command.json"

BROKER="$(jq -r '.http.url' "$SPEC")"
BODY="$(jq -c '.http.body' "$SPEC")"

echo "Replaying DeepSeek scroll -> $BROKER"
RESP="$(curl -s -X POST "$BROKER" -H 'Content-Type: application/json' -d "$BODY")"

# The broker wraps the JS return (a JSON string) in result.result.value.
INNER="$(printf '%s' "$RESP" | jq -r '.result.result.value // .body // empty')"
if [ -z "$INNER" ]; then
  echo "Raw broker response:"; printf '%s\n' "$RESP"; exit 1
fi

echo "Scroll result: $INNER"
DELTA="$(printf '%s' "$INNER" | jq -r '.delta // 0')"
if [ "${DELTA:-0}" -gt 0 ] 2>/dev/null; then
  echo "CONFIRMED: scrolled down by ${DELTA}px (before -> after)."
else
  echo "NOTE: delta=${DELTA} — container may already be pinned at bottom (virtualized list clamps)."
fi
