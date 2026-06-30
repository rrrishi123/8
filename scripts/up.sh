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
COLLECTOR="$REPO/8/collector/collector"
WEB="$REPO/8/web"
BROKER=http://127.0.0.1:4445/command

up()   { lsof -ti :"$1" >/dev/null 2>&1; }
wait_up() { for _ in $(seq 1 40); do up "$1" && return 0; sleep 0.3; done; return 1; }
cmd()  { curl -s -m "${2:-15}" "$BROKER" -H 'Content-Type: application/json' -d "$1"; }

# 1. geckodriver — restart fresh so no dead/stale session lingers.
lsof -ti :4444 | xargs kill 2>/dev/null || true; sleep 1
nohup "$GECKO" --port 4444 --host 127.0.0.1 --allow-hosts localhost 127.0.0.1 --log info >/tmp/geckodriver.log 2>&1 &
wait_up 4444 && echo "geckodriver: up :4444"

# 2. Firefox BiDi session on the persistent profile (= the saved login).
#    -remote-allow-system-access unlocks the chrome context, which is how the
#    WITNESS reads per-tab memory/CPU (ChromeUtils.requestProcInfo) — Firefox
#    refuses requestProcInfo from a content sandbox. This is the observe-all-tabs
#    Observation channel's privilege, read-only; it never drives a target.
RESP=$(curl -s -m 90 http://127.0.0.1:4444/session -H 'Content-Type: application/json' -d @- <<JSON
{"capabilities":{"alwaysMatch":{"browserName":"firefox","webSocketUrl":true,
  "moz:firefoxOptions":{"args":["-profile","$PROFILE","-remote-allow-system-access"]}}}}
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

# 5. collector. -gecko enables /procinfo (per-tab mem/CPU via the chrome context).
#    BROWSER PACK — the 2nd engine: if a Chrome CDP is reachable (started by
#    `adapters/browser up`, on :9333), hold its page socket as a SECOND broker so
#    8 shows Firefox AND Chrome side by side (the channel-physics twin: fox is
#    BiDi, chrome is CDP — the collector probes which and captures accordingly).
#    Auto-detected: no Chrome -> no seat -> default behaviour unchanged.
BROKERS="fox=http://127.0.0.1:4445"
if curl -s -m2 http://127.0.0.1:9333/json/version >/dev/null 2>&1; then
  CHROME_WS=$(curl -s -m3 http://127.0.0.1:9333/json | jq -r '[.[]|select(.type=="page" and .webSocketDebuggerUrl)][0].webSocketDebuggerUrl // empty')
  if [ -n "$CHROME_WS" ]; then
    lsof -ti :4446 | xargs kill 2>/dev/null || true; sleep 0.3
    nohup "$CHANNEL" -ws "$CHROME_WS" -listen :4446 >/tmp/broker-chrome.log 2>&1 &
    wait_up 4446 && BROKERS="$BROKERS,chrome=http://127.0.0.1:4446" && echo "browser:    chrome seat on :4446"
  fi
fi
# brokers are fixed at collector start, so a NEW chrome seat needs a fresh
# collector. If one is already up WITHOUT chrome, restart it to pick the seat up.
if up 7070 && ! curl -s -m2 http://127.0.0.1:7070/health | grep -q '"chrome"' && [ "$BROKERS" != "fox=http://127.0.0.1:4445" ]; then
  lsof -ti :7070 | xargs kill 2>/dev/null || true; sleep 0.5
fi
if up 7070; then echo "collector:  already up :7070"; else
  # a missing binary must NOT kill the wire (8 would poll a dead :7070 forever) —
  # build it on demand. This is the gap that left 8 "moving while doing nothing".
  [ -x "$COLLECTOR" ] || { echo "collector:  binary missing -> building"; ( cd "$REPO/8/collector" && go build -o collector . ); }
  nohup "$COLLECTOR" -listen :7070 -brokers "$BROKERS" \
    -gecko "http://127.0.0.1:4444/session/$SID" >/tmp/collector-8.log 2>&1 &
  wait_up 7070 && echo "collector:  up :7070 (procinfo enabled; brokers: $BROKERS)"
fi

# 6. vite cockpit (the web app served on :8088).
if up 8088; then echo "vite:       already up :8088"; else
  ( cd "$WEB" && nohup npm run dev >/tmp/vite-8.log 2>&1 & )
  wait_up 8088 && echo "vite:       up :8088"
fi

# 6b. 8 runs REFLEXIVELY — as a tab INSIDE the Firefox it observes (FLOW 6). That
#     self-witness (8 sees its own ★ tab memory through its own /procinfo) is 8's
#     biggest IP; the cockpit tab is restored below with the other tabs. The ~8GB
#     blowup that once tempted moving it out was the RECURSION (witnessing its own
#     traffic), now filtered at the pump — reflexive holds at ~0.7GB.
#     (FLOW 7 "out-of-browser" lives in scripts/cockpit.sh as an OPT-IN, for when
#      you want the seer isolated from the subject — it loses self-sight.)

# 7. RESTORE the working tabs from the persistent store (the watchdog auto-saves
#    them every cycle). First run / empty store -> defaults: cockpit + peer thread.
#    This is the session-store the channel Firefox lacks across crashes.
TABS_FILE="${TABS_FILE:-$HOME/.8-tabs.txt}"
DS_THREAD="https://chat.deepseek.com/a/chat/s/82a7eafd-2ba5-4226-836d-344368e7723b"
CTX=$(cmd '{"method":"browsingContext.getTree","params":{}}' | jq -r '.result.contexts[0].context')

URLS=()
if [ -s "$TABS_FILE" ]; then
  while IFS= read -r u; do [ -n "$u" ] && URLS+=("$u"); done < "$TABS_FILE"
fi
[ ${#URLS[@]} -eq 0 ] && URLS=("http://localhost:8088/" "$DS_THREAD")
# 8's own cockpit always comes back, even if it wasn't in the last snapshot
case " ${URLS[*]} " in *":8088"*) ;; *) URLS=("http://localhost:8088/" "${URLS[@]}");; esac

first=1; DS=""
for u in "${URLS[@]}"; do
  case "$u" in about:*|chrome:*|"") continue;; esac
  if [ "$first" = 1 ]; then target="$CTX"; first=0
  else target=$(cmd '{"method":"browsingContext.create","params":{"type":"tab"}}' | jq -r '.result.context'); fi
  cmd "{\"method\":\"browsingContext.navigate\",\"params\":{\"context\":\"$target\",\"url\":\"$u\",\"wait\":\"complete\"}}" 60 >/dev/null
  echo "tab:        $target -> $u"
  case "$u" in *deepseek.com*) DS="$target";; esac
done
echo "restored ${#URLS[@]} tab(s) from $TABS_FILE"

# 8 IS the control surface AND the reflexive self-witness: foreground its tab so
# you land on 8 and its streams run (they pause when its tab is hidden).
cmd "{\"method\":\"browsingContext.activate\",\"params\":{\"context\":\"$CTX\"}}" >/dev/null
echo "activated:  8's cockpit tab is foreground ($CTX) — reflexive, sees itself"

# 8. login check: is DeepSeek authenticated? (textarea present = yes)
[ -z "$DS" ] && DS="$CTX"
LOGGEDIN=$(cmd "{\"method\":\"script.evaluate\",\"params\":{\"expression\":\"!!document.querySelector('textarea')\",\"target\":{\"context\":\"$DS\"},\"awaitPromise\":true}}" 15 | jq -r '.result.result.value // "?"')
echo
echo "WIRE UP.  cockpit=$CTX  deepseek=$DS"
echo "deepseek logged in (textarea present): $LOGGEDIN"
[ "$LOGGEDIN" != "true" ] && echo "  !! not authenticated — profile creds may have expired; re-login needed."
echo "ws=$WS"
