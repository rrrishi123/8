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
BROWSERPACK="$REPO/adapters/browser/browser"
CHANNEL="$REPO/http-mcp/.bin/channel"
COLLECTOR="$REPO/8/collector/collector"
WEB="$REPO/8/web"
BROKER=http://127.0.0.1:4445/command

up()   { lsof -ti :"$1" >/dev/null 2>&1; }
wait_up() { for _ in $(seq 1 40); do up "$1" && return 0; sleep 0.3; done; return 1; }
cmd()  { curl -s -m "${2:-15}" "$BROKER" -H 'Content-Type: application/json' -d "$1"; }

# 0. Force non-headless so the Firefox window is visible on the desktop.
# NOTE: Firefox goes headless when MOZ_HEADLESS is set to ANY non-empty value —
# even "0". The only way to force a visible window is to unset it.
unset MOZ_HEADLESS

# 1+2. the firefox SEAT — owned by adapters' browser pack, not this script
#      (8 never launches browsers; it only watches). The pack launches
#      geckodriver + Firefox DETACHED (their own process session), so no shell
#      — including the one running this script — holds the seat's lifetime.
#      It also publishes ~/.8/gecko.json and passes -remote-allow-system-access
#      (the WITNESS's chrome-context privilege for per-tab requestProcInfo).
#      Reuse a live seat; only summon a fresh one when the current one is gone.
SEAT="$HOME/.8/gecko.json"
WS=""; SID=""
if up 4444 && [ -s "$SEAT" ] && pgrep -f "firefox.*ltqa-firefox-deepseek" >/dev/null; then
  WS=$(jq -r '.ws // empty' "$SEAT"); SID=$(jq -r '.session_id // empty' "$SEAT")
fi
if [ -n "$WS" ]; then
  echo "firefox:    seat reused — session $SID (browser pack owns it)"
else
  RR=$("$BROWSERPACK" up --engine firefox --port 4444 --profile "$PROFILE") \
    || { echo "FAILED firefox seat:"; echo "$RR" | head -c 600; exit 1; }
  WS=$(echo "$RR" | jq -r '.stream // empty'); SID=$(echo "$RR" | jq -r '.session_id // empty')
  [ -z "$WS" ] && { echo "FAILED firefox seat:"; echo "$RR" | head -c 600; exit 1; }
  echo "firefox:    seat up via browser pack — session $SID"
fi
echo "            ws=$WS"

# Bring Firefox window to front so it's visible on the desktop.
# Delayed to let Firefox finish initializing the saved tabs.
(sleep 5 && osascript -e 'tell application "Firefox" to activate' \
  -e 'tell application "Firefox" to set bounds of front window to {0, 50, 1440, 980}') &
echo "            window summoned to front (5s delay)"

# 3. broker -> the fresh ws (replace any stale broker).
lsof -ti :4445 | xargs kill 2>/dev/null || true; sleep 1
nohup "$CHANNEL" -ws "$WS" -listen :4445 >/tmp/broker.log 2>&1 &
wait_up 4445 && echo "broker:     up :4445" \
  || { echo "FAILED broker :4445 — everything after this would silently no-op. $(head -1 /tmp/broker.log)"; exit 1; }

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
  # -session-file: re-read the live seat on every probe, so /procinfo (and the
  # watchdog's FLOW-10 recycle that depends on it) survives Firefox recycles —
  # a fixed -gecko session URL goes stale on the first recycle and blinds both.
  nohup "$COLLECTOR" -listen :7070 -brokers "$BROKERS" \
    -gecko "http://127.0.0.1:4444/session/$SID" \
    -session-file "$HOME/.8/gecko.json" >/tmp/collector-8.log 2>&1 &
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

first=1; DS=""; COCKPIT=""
# IDEMPOTENT restore: the seat may be REUSED with its tabs already open (it
# outlives this script now). Only create what's missing; never navigate an
# existing tab away, and never double-open a URL.
OPEN=$(cmd '{"method":"browsingContext.getTree","params":{}}')
FIRST_URL=$(echo "$OPEN" | jq -r '.result.contexts[0].url // empty')
for u in "${URLS[@]}"; do
  case "$u" in about:*|chrome:*|"") continue;; esac
  target=$(echo "$OPEN" | jq -r --arg u "$u" '.result.contexts[] | select(.url==$u) | .context' | head -1)
  if [ -n "$target" ]; then
    echo "tab:        $target == $u (already open)"
  else
    if [ "$first" = 1 ] && [ "$FIRST_URL" = "about:blank" ]; then target="$CTX"
    else target=$(cmd '{"method":"browsingContext.create","params":{"type":"tab"}}' | jq -r '.result.context'); fi
    cmd "{\"method\":\"browsingContext.navigate\",\"params\":{\"context\":\"$target\",\"url\":\"$u\",\"wait\":\"complete\"}}" 60 >/dev/null
    echo "tab:        $target -> $u"
  fi
  first=0
  case "$u" in *deepseek.com*) DS="$target";; esac
  # track the COCKPIT's actual context — it is NOT necessarily the first tab, so
  # activating $CTX (the first context) would land on excalidraw, not 8. (This is
  # exactly why "Firefox doesn't come back to 8 after a recycle".)
  case "$u" in *localhost:8088*|*127.0.0.1:8088*) COCKPIT="$target";; esac
done
echo "restored ${#URLS[@]} tab(s) from $TABS_FILE"

# 8 IS the control surface AND the reflexive self-witness: foreground ITS tab (the
# real cockpit context, not just the first tab) so you ALWAYS land back on 8 after
# a recycle, and its streams run (they pause when its tab is hidden).
FG="${COCKPIT:-$CTX}"
cmd "{\"method\":\"browsingContext.activate\",\"params\":{\"context\":\"$FG\"}}" >/dev/null
echo "activated:  8's cockpit tab is foreground ($FG) — reflexive, sees itself"

# 8. login check: is DeepSeek authenticated? (textarea present = yes)
[ -z "$DS" ] && DS="$CTX"
LOGGEDIN=$(cmd "{\"method\":\"script.evaluate\",\"params\":{\"expression\":\"!!document.querySelector('textarea')\",\"target\":{\"context\":\"$DS\"},\"awaitPromise\":true}}" 15 | jq -r '.result.result.value // "?"')
echo
echo "WIRE UP.  cockpit=$CTX  deepseek=$DS"
echo "deepseek logged in (textarea present): $LOGGEDIN"
[ "$LOGGEDIN" != "true" ] && echo "  !! not authenticated — profile creds may have expired; re-login needed."
echo "ws=$WS"

# 9. watchdog — the memory bound. Without it Firefox has NO recycle ceiling on
# mac: 12 tabs + media autoplay grew the parent to a 49GB footprint (2026-07-24,
# entire swap consumed; macOS pages instead of OOM-killing so it balloons
# silently). watchdog.sh recycles at RECYCLE_MB (default 4500) via FLOW 10.
if ! pgrep -f "scripts/watchdog.sh" >/dev/null 2>&1; then
  ( cd "$REPO/8" && setsid nohup bash scripts/watchdog.sh >/tmp/watchdog-8.log 2>&1 & )
  echo "watchdog:   started (recycle bound ${RECYCLE_MB:-4500}MB, log /tmp/watchdog-8.log)"
else
  echo "watchdog:   already running"
fi
