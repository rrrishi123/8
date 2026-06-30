#!/usr/bin/env bash
# cockpit.sh — run 8's cockpit in its OWN browser, NOT as a tab inside the Firefox
# it observes. This is the Excalidraw's FLOW 7 ("8 out-of-browser"): the seer's
# render + stream + poll load lives in a separate Chrome, so it never inflates the
# SUBJECT Firefox. Measured: moving the cockpit out dropped the observed Firefox
# from ~8GB to ~350MB. CDP (--remote-debugging-port) lets 8 — and you — drive the
# cockpit itself through the wire (http-mcp bidi_command speaks CDP).
set -uo pipefail
CHROME="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
PORT="${COCKPIT_CDP_PORT:-9333}"
URL="${COCKPIT_URL:-http://localhost:8088}"

if curl -s -m2 "http://127.0.0.1:$PORT/json/version" >/dev/null 2>&1; then
  echo "cockpit: already up (Chrome CDP :$PORT)"; exit 0
fi
[ -x "$CHROME" ] || { echo "cockpit: Chrome not found at $CHROME"; exit 1; }

nohup "$CHROME" \
  --user-data-dir="$HOME/.8-cockpit-chrome" \
  --remote-debugging-port="$PORT" \
  --no-first-run --no-default-browser-check --disable-features=Translate \
  --app="$URL" >/tmp/cockpit-chrome.log 2>&1 &

for _ in $(seq 1 20); do curl -s -m2 "http://127.0.0.1:$PORT/json/version" >/dev/null 2>&1 && break; sleep 0.5; done
echo "cockpit: 8 in its OWN Chrome ($URL) — CDP :$PORT (subject Firefox stays clean)"
