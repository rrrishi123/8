#!/usr/bin/env bash
# Docker --init reaps children. No /init, Selkies, compositor, audio, nginx or VNC.
set -euo pipefail
profile="${CHROME_PROFILE:-/config/.config/chromium}"
mkdir -p "$profile"
# The migration owns an offline COPY; no other browser may mount it. Chromium's
# singleton symlinks still name the old container hostname after that copy.
rm -f "$profile/SingletonLock" "$profile/SingletonCookie" "$profile/SingletonSocket"
unset DISPLAY WAYLAND_DISPLAY
browser_pid="" proxy_pid=""
stop() {
  trap - TERM INT
  [ -z "$browser_pid" ] || kill -TERM "$browser_pid" 2>/dev/null || true
  [ -z "$proxy_pid" ] || kill -TERM "$proxy_pid" 2>/dev/null || true
  wait || true
}
trap 'stop; exit 0' TERM INT
trap stop EXIT
/usr/lib/chromium/chromium --headless=new --no-sandbox \
  --user-data-dir="$profile" --password-store=basic \
  --remote-debugging-address=127.0.0.1 --remote-debugging-port=9223 \
  --remote-allow-origins='*' --no-first-run --no-default-browser-check \
  --window-size=1440,900 --restore-last-session "$@" &
browser_pid=$!
/usr/local/bin/cdp-proxy &
proxy_pid=$!
wait -n "$browser_pid" "$proxy_pid"
