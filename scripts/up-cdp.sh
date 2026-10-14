#!/usr/bin/env bash
# Chrome/CDP boot: one engine, native-width capture, no Firefox companion.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="${EIGHT_REPO:-$(cd "$SCRIPT_DIR/../.." && pwd)}"
COLLECTOR="$REPO/8/collector/collector"
WEB="$REPO/8/web"
# Native discovery prefers Chrome and falls back to Firefox only if Chrome is
# absent. Preserve an explicit operator choice; do not suppress that fallback.
export EIGHT_ENGINE="${EIGHT_ENGINE:-}"

[ -x "$COLLECTOR" ] || bash "$REPO/8/build.sh"
# Native up discovers the browser, holds its CDP broker, then starts the
# collector. Persisted flags / an existing collector are deliberately retained.
"$COLLECTOR" up

if ! curl -fsS --max-time 2 http://127.0.0.1:8088/ >/dev/null 2>&1; then
  (cd "$WEB" && nohup npm run dev >/tmp/vite-8.log 2>&1 &)
fi

# Use the native supervisor on a fresh host. Do not stack it on an existing
# collector watch or on the legacy Firefox watchdog that owns a live host.
if ! pgrep -f 'collector watch|scripts/watchdog.sh' >/dev/null 2>&1; then
  nohup "$COLLECTOR" watch >/tmp/watch-8.log 2>&1 &
fi
echo 'CDP boot requested. Cockpit: http://localhost:8088/ (open in the Chrome seat).'
