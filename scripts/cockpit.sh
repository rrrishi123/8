#!/usr/bin/env bash
# cockpit.sh — OPT-IN: run 8's cockpit in its OWN browser (Excalidraw FLOW 7,
# "8 out-of-browser"). NOT the default — 8's default is REFLEXIVE: a tab inside
# the Firefox it observes (FLOW 6), so it sees its own ★ memory through its own
# /procinfo. That self-witness is 8's biggest IP; don't trade it away lightly.
#
# Use this ONLY when you deliberately want the seer ISOLATED from the subject
# (e.g. measuring the subject Firefox without 8's render load on it). The cost:
# out-of-browser 8 LOSES self-sight (it must fall back to `ps` for its own mem).
# The ~8GB blowup that once motivated this was the RECURSION (8 witnessing its own
# traffic), now filtered at the collector pump — reflexive holds at ~0.7GB, so you
# rarely need this. CDP (--remote-debugging-port) keeps it driveable via http-mcp.
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
