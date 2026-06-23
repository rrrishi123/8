#!/usr/bin/env bash
# up.sh — bring the whole 8 wire up in one shot. Idempotent: reuses anything
# already listening; only (re)starts what's down.
#
# Chain: geckodriver(:4444) -> Firefox(BiDi) -> broker(:4445) -> collector(:7070) -> vite(:8088)
#
# Two things that always bite on a fresh session, both handled here:
#   1. LOGIN  — Firefox launches on the persistent profile, which already holds
#               the DeepSeek/Claude logins + the cf_clearance cookie. No re-login.
#   2. BOT-WALL — we hide navigator.webdriver via a BiDi preload script, so
#               Cloudflare et al. don't block the automated session (which would
#               also block the saved login from loading).
set -uo pipefail

REPO=/Users/rishirajs/Desktop/repos
PROFILE=/Users/rishirajs/.ltqa-firefox-deepseek
GECKO="$REPO/ltqa-platform/.bin/drivers/firefox/0.37.0/geckodriver"
CHANNEL="$REPO/http-mcp/.bin/channel"
COLLECTOR="$REPO/command-explorer/collector/collector"
WEB="$REPO/command-explorer/web"
BROKER=http://127.0.0.1:4445/command

up()   { lsof -ti :"$1" >/dev/null 2>&1; }
wait_up() { for _ in $(seq 1 40); do up "$1" && return 0; sleep 0.3; done; return 1; }
cmd()  { curl -s -m "${2:-15}" "$BROKER" -H 'Content-Type: application/json' -d "$1"; }

# 1. geckodriver — restart fresh so no dead/stale session lingers.
lsof -ti :4444 | xargs kill 2>/dev/null || true; sleep 1
nohup "$GECKO" --port 4444 --host 127.0.0.1 --allow-hosts localhost 127.0.0.1 --log info >/tmp/geckodriver.log 2>&1 &
wait_up 4444 && echo "geckodriver: up :4444"

# 2. Firefox BiDi session on the persistent profile (= the saved login).
RESP=$(curl -s -m 90 http://127.0.0.1:4444/session -H 'Content-Type: application/json' -d @- <<JSON
{"capabilities":{"alwaysMatch":{"browserName":"firefox","webSocketUrl":true,
  "moz:firefoxOptions":{"args":["-profile","$PROFILE"]}}}}
JSON
)
WS=$(echo "$RESP"  | jq -r '.value.capabilities.webSocketUrl // empty')
SID=$(echo "$RESP" | jq -r '.value.sessionId // empty')
[ -z "$WS" ] && { echo "FAILED firefox session:"; echo "$RESP" | head -c 600; exit 1; }
echo "firefox:    session $SID"
echo "            ws=$WS"

# 3. broker -> the fresh ws (replace any stale broker).
lsof -ti :4445 | xargs kill 2>/dev/null || true; sleep 1
nohup "$CHANNEL" -ws "$WS" -listen :4445 >/tmp/broker.log 2>&1 &
wait_up 4445 && echo "broker:     up :4445"

# 4. hide navigator.webdriver (bot-wall) — BiDi preload, applies to every nav.
cmd '{"method":"script.addPreloadScript","params":{"functionDeclaration":"() => { Object.defineProperty(Navigator.prototype, \"webdriver\", { get: () => false }); }"}}' >/dev/null
echo "preload:    navigator.webdriver hidden"

# 4b. subscribe to BiDi events — WITHOUT this the channel emits nothing and 8's
# feed shows zero channel rows (the bug: one socket, but silent until subscribed).
cmd '{"method":"session.subscribe","params":{"events":["network.beforeRequestSent","network.responseCompleted","log.entryAdded","browsingContext.domContentLoaded"]}}' >/dev/null
echo "subscribe:  channel events flowing (network, log, domContentLoaded)"

# 5. collector.
if up 7070; then echo "collector:  already up :7070"; else
  nohup "$COLLECTOR" -listen :7070 -brokers fox=http://127.0.0.1:4445 >/tmp/collector-8.log 2>&1 &
  wait_up 7070 && echo "collector:  up :7070"
fi

# 6. vite cockpit.
if up 8088; then echo "vite:       already up :8088"; else
  ( cd "$WEB" && nohup npm run dev >/tmp/vite-8.log 2>&1 & )
  wait_up 8088 && echo "vite:       up :8088"
fi

# 7. open the working tabs: cockpit (default ctx) + DeepSeek peer (new tab).
CTX=$(cmd '{"method":"browsingContext.getTree","params":{}}' | jq -r '.result.contexts[0].context')
cmd "{\"method\":\"browsingContext.navigate\",\"params\":{\"context\":\"$CTX\",\"url\":\"http://localhost:8088/\",\"wait\":\"complete\"}}" 30 >/dev/null
echo "cockpit:    $CTX -> :8088"
DS=$(cmd '{"method":"browsingContext.create","params":{"type":"tab"}}' | jq -r '.result.context')
# resume the SAME peer thread (the one with the full discussion), not a fresh chat
DS_THREAD="https://chat.deepseek.com/a/chat/s/82a7eafd-2ba5-4226-836d-344368e7723b"
cmd "{\"method\":\"browsingContext.navigate\",\"params\":{\"context\":\"$DS\",\"url\":\"$DS_THREAD\",\"wait\":\"complete\"}}" 60 >/dev/null
echo "deepseek:   $DS -> peer thread (context preserved)"

# 8. login check: is DeepSeek authenticated? (textarea present = yes)
LOGGEDIN=$(cmd "{\"method\":\"script.evaluate\",\"params\":{\"expression\":\"!!document.querySelector('textarea')\",\"target\":{\"context\":\"$DS\"},\"awaitPromise\":true}}" 15 | jq -r '.result.result.value // "?"')
echo
echo "WIRE UP.  cockpit=$CTX  deepseek=$DS"
echo "deepseek logged in (textarea present): $LOGGEDIN"
[ "$LOGGEDIN" != "true" ] && echo "  !! not authenticated — profile creds may have expired; re-login needed."
echo "ws=$WS"
