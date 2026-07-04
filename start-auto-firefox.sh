#!/usr/bin/env bash
# Bring up the authenticated automated Firefox + the 8 channel.
# Re-applies the persistent creds (creds-store) into firefox-auto on EVERY launch,
# so the automated browser is always logged in to DeepSeek + Claude, with the
# 8 cockpit as the first tab. Idempotent: reuses geckodriver/collector if up.
set -uo pipefail
AC=/home/rishi/Work/kosaten/.auth-cache
AUTO=$AC/firefox-auto
STORE=$AC/creds-store
FF=/home/rishi/Work/ff/firefox/firefox
GECKO=/usr/bin/geckodriver
COCKPIT=http://localhost:8088/
BROKER=/home/rishi/Work/8/broker.mjs
COLLECTOR=/home/rishi/Work/8/collector/collector
ADAPT=/home/rishi/Work/adapters
TMP=/tmp/claude-1000
export WAYLAND_DISPLAY=wayland-1 XDG_RUNTIME_DIR=/run/user/1000 MOZ_ENABLE_WAYLAND=1
# node lives in mise, not /usr/bin — needed for broker.mjs when run from systemd
export PATH="$HOME/.local/share/mise/shims:$PATH"

# 1. stop any running automated firefox so its profile unlocks
pkill -9 -f "$AUTO" 2>/dev/null; sleep 1
# Clean the profile lock that Firefox leaves behind on crash/SIGKILL —
# without this, the next launch hangs because Firefox thinks another
# instance owns the profile.
rm -f "$AUTO/.parentlock"

# 1b. Clean crash history so Firefox never triggers safe mode on restart.
#     Every SIGKILL leaves a minidump; 7+ crash dumps + a stale
#     sessionCheckpoints.json (sessionstore-windows-restored:true) + an old
#     toolkit.startup.last_success = Firefox shows the safe-mode dialog instead
#     of loading normally, and geckodriver's session create times out.
#     We purge all crash artifacts AND reset the startup checkpoint so Firefox
#     believes the last session completed cleanly.
rm -rf "$AUTO/minidumps" "$AUTO/crashes"
mkdir -p "$AUTO/minidumps" "$AUTO/crashes/events"
echo '{}' > "$AUTO/sessionCheckpoints.json"
# Update last_success in prefs.js so Firefox doesn't think it died mid-startup.
# If prefs.js doesn't have the key yet, we still want the old value gone so
# Firefox writes a fresh one on successful launch.
if [ -f "$AUTO/prefs.js" ]; then
  sed -i '/toolkit\.startup\.last_success/d' "$AUTO/prefs.js"
  echo "user_pref(\"toolkit.startup.last_success\", $(date +%s));" >> "$AUTO/prefs.js"
fi

# 2. re-apply stored creds into the automation profile (the "always logged in" step)
mkdir -p "$AUTO"
cp -a "$STORE/cookies.sqlite" "$AUTO/" 2>/dev/null
rm -f "$AUTO"/cookies.sqlite-wal "$AUTO"/cookies.sqlite-shm 2>/dev/null
rm -rf "$AUTO/storage" 2>/dev/null; cp -a "$STORE/storage" "$AUTO/" 2>/dev/null
for f in webappsstore.sqlite storage.sqlite; do [ -f "$STORE/$f" ] && cp -a "$STORE/$f" "$AUTO/"; done
echo "creds applied from $STORE"

# 3. ensure a HEALTHY geckodriver on :4444.
#    Self-heal the zombie case: when Firefox crashes, geckodriver keeps :4444
#    listening but its firefox-bin child goes <defunct> and BiDi :9222 dies.
#    That stale geckodriver then rejects every new session with
#    "Session is already started" — so the watchdog's revive loops forever.
#    If :4444 is up but :9222 is gone, the geckodriver is stale: kill it (which
#    also reaps the defunct firefox-bin child) so we relaunch clean.
if ss -tlnp 2>/dev/null | grep -q ':4444 ' && ! ss -tlnp 2>/dev/null | grep -q ':9222 '; then
  echo "stale geckodriver (:4444 up, BiDi :9222 dead) -> killing for clean relaunch"
  pkill -f 'geckodriver --port 4444' 2>/dev/null; sleep 2
fi
if ! ss -tlnp 2>/dev/null | grep -q ':4444 '; then
  nohup "$GECKO" --port 4444 --host 127.0.0.1 --binary "$FF" --log info >"$TMP/gecko.log" 2>&1 &
  for _ in $(seq 1 20); do ss -tlnp 2>/dev/null | grep -q ':4444 ' && break; sleep 0.5; done
fi

# 4. create a headful session on firefox-auto with BiDi.
#    -remote-allow-system-access unlocks the chrome context, which is how the
#    drawshot and procinfo work (gBrowser, ChromeUtils.requestProcInfo) — the
#    8 cockpit's canvas needs this to render live tab previews.
RESP=$(curl -s -m 60 -X POST http://127.0.0.1:4444/session -H 'Content-Type: application/json' \
  -d "{\"capabilities\":{\"alwaysMatch\":{\"moz:firefoxOptions\":{\"args\":[\"-profile\",\"$AUTO\",\"-remote-allow-system-access\"]},\"webSocketUrl\":true}}}")
SID=$(echo "$RESP" | python3 -c "import json,sys;print(json.load(sys.stdin)['value']['sessionId'])" 2>/dev/null)
WS=$(echo "$RESP"  | python3 -c "import json,sys;print(json.load(sys.stdin)['value']['capabilities']['webSocketUrl'])" 2>/dev/null)
[ -z "${SID:-}" ] && { echo "session create FAILED: $RESP"; exit 1; }
echo "$SID" >"$TMP/sid"; echo "session $SID"

# 4b. publish SID so collector/watchdog can auto-recover without manual restart.
#     Atomic write (temp + rename) so a concurrent reader never sees a half-written file.
mkdir -p "$HOME/.8"
printf '{"session_id":"%s","ws":"%s","ts":"%s"}\n' "$SID" "$WS" "$(date -u +%FT%TZ)" > "$HOME/.8/gecko.json.tmp" \
  && mv -f "$HOME/.8/gecko.json.tmp" "$HOME/.8/gecko.json"
echo "            published SID -> ~/.8/gecko.json"

# 5. (re)start broker :4445 on the new session ws
pkill -f 'broker.mjs' 2>/dev/null; sleep 1
BIDI_WS="$WS" PORT=4445 nohup node "$BROKER" >"$TMP/broker.log" 2>&1 &
sleep 2

# 5b. BOT-WALL — hide navigator.webdriver via a BiDi preload (registered once on
#     the session, applies to every navigation). This is the step Mac scripts/up.sh
#     has (its step 4) that this launcher was missing: without it, geckodriver leaves
#     navigator.webdriver=true, Cloudflare on claude.ai loops a managed challenge,
#     and the saved login never loads. DeepSeek's CF is lax so it worked regardless.
curl -s -m 10 http://127.0.0.1:4445/command -H 'Content-Type: application/json' \
  -d '{"method":"script.addPreloadScript","params":{"functionDeclaration":"() => { Object.defineProperty(Navigator.prototype, \"webdriver\", { get: () => false }); }"}}' >/dev/null \
  && echo "preload: navigator.webdriver hidden (bot-wall)" \
  || echo "WARN: preload step failed — claude.ai may hit the CF challenge"

# 6. minimal collector fallback if not yet running (up-omarchy.sh starts it with
#    full flags -fxshot -fxdriver -gecko -session-file; this is only for the case
#    where up-omarchy.sh's collector start failed to bind before we got here).
ss -tlnp 2>/dev/null | grep -q ':7070 ' || {
  GECKO_URL="http://127.0.0.1:4444/session/$SID"
  nohup "$COLLECTOR" -listen :7070 -brokers "fox=http://127.0.0.1:4445" \
    -fxshot "$ADAPT/browser/firefox-drawshot.js" \
    -fxdriver "$ADAPT/browser/firefox-stream.js" \
    -gecko "$GECKO_URL" -session-file "$HOME/.8/gecko.json" \
    >"$TMP/collector.log" 2>&1 & sleep 1; }

# 7. 8 cockpit as first tab
curl -s -m 30 -X POST "http://127.0.0.1:4444/session/$SID/url" -H 'Content-Type: application/json' -d "{\"url\":\"$COCKPIT\"}" >/dev/null
echo "UP: automated firefox (authenticated) — 8 cockpit is the first tab; session in $TMP/sid"
