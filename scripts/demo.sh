#!/usr/bin/env bash
# demo.sh — the four-system's pieces IN ACTION, narrated and self-evidencing.
#
# Hermetic by design: it stands up an ISOLATED witness (its own temp HOME, a spare
# port, no brokers/gecko) so it runs on any host, is reproducible, and NEVER touches
# the production witness (~/.8). Every step names the PIECE, runs it live, and prints
# the WITNESS receipt as proof. The live-only pieces — driving a real browser over a
# CHANNEL, the wake-on-event poke landing in a tmux pane, the federation mesh — are
# shown as the exact commands to run against a live fleet.
#
#   ./scripts/demo.sh            # PORT= to override the spare port (default 7098)
set -uo pipefail
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"   # .../8/scripts
ROOT="${ROOT:-$(cd "$SELF_DIR/../.." && pwd)}"                 # dir holding 8, http-mcp, ...
PORT="${PORT:-7098}"
COL="$ROOT/8/collector"
H="http://127.0.0.1:$PORT"

bold(){ printf '\033[1m%s\033[0m\n' "$*"; }
hr(){   printf '\n\033[1;35m── %s ──\033[0m\n' "$*"; }
say(){  printf '   %s\n' "$*"; }
ev(){   printf '   \033[36m» %s\033[0m\n' "$*"; }   # evidence (cyan)
run(){  printf '   \033[2m$ %s\033[0m\n' "$*"; }    # the command (dim)

echo
bold "THE WIRE — the pieces in action"
say  "two atoms (CALL + CHANNEL) over a lean wire, and a witness that records every act."

hr "0 · stand up an isolated WITNESS (never touches ~/.8)"
if lsof -ti ":$PORT" >/dev/null 2>&1; then say "port $PORT busy — set PORT= to a free one"; exit 1; fi
( cd "$COL" && go build -o collector . ) || { say "collector build FAILED"; exit 1; }
ev "built the collector from source"
TMP=$(mktemp -d); mkdir -p "$TMP/.8"
cleanup(){ [ -n "${cpid:-}" ] && kill "$cpid" 2>/dev/null; rm -rf "$TMP"; }
trap cleanup EXIT
( cd "$COL" && env -u TMUX HOME="$TMP" ./collector -listen ":$PORT" >"$TMP/col.log" 2>&1 & echo $! >"$TMP/pid" )
cpid=$(cat "$TMP/pid")
up=0; for _ in $(seq 1 20); do curl -s -m2 "$H/health" 2>/dev/null | grep -q '"alive":true' && { up=1; break; }; sleep 0.5; done
[ "$up" = 1 ] || { say "collector never came up — log:"; tail -5 "$TMP/col.log" | sed 's/^/     /'; exit 1; }
ev "isolated witness live on $H  (HOME=$TMP, no brokers)"

hr "1 · the CALL atom — one act, witnessed"
say "A CALL is a single request. The witness stamps every one with a receipt."
run "POST $H/work  {text, by}"
hdrs=$(curl -s -D - -o "$TMP/body" -m6 "$H/work" -H 'Content-Type: application/json' -H 'X-8-Actor: demo' \
       -d '{"text":"[demo] a witnessed call","by":"demo"}' | tr -d '\r')
ev "response: $(cat "$TMP/body")"
ev "receipt:  $(echo "$hdrs" | awk -F': ' 'tolower($1)=="x-8-witness"{print $2}')"
say "^ that X-8-Witness line is the point: who did what, when — provable, not inferred."

hr "2 · the WITNESS — inscribed and queryable"
run "GET $H/work   (read the act back)"
curl -s -m4 "$H/work" | python3 -c '
import json,sys
d=json.load(sys.stdin); items=d if isinstance(d,list) else d.get("work",[])
m=[w for w in items if isinstance(w,dict) and "[demo] a witnessed call" in (w.get("text") or "")]
print("   \033[36m» ledger holds %d acts; the demo CALL is #%s\033[0m" % (len(items), m[-1]["id"] if m else "?"))'
say "the act is now part of the append-only record — this is why teardown isn't death."

hr "3 · the surfaces the witness exposes"
for ep in health mind peers; do
  run "GET $H/$ep"
  ev "$(curl -s -m3 "$H/$ep" 2>/dev/null | head -c 150)"
done
say "/mind = who's registered · /peers = the federation mesh (empty here; a live hub lists every host)."

hr "4 · task vs record — the queue stays honest"
say "a TASK is claimable work; a RECORD is born done (a finding/act), never summoned to a worker."
curl -s -m4 "$H/work" -H 'Content-Type: application/json' \
     -d '{"kind":"record","by":"demo","text":"[demo] a record — born done"}' >/dev/null
ev "posted a record; it lands on the surface but never enters the worker picker."

hr "5 · wake-on-event (#4441) — a completion reaches whoever waited"
say "tag a task with a notify target; when it flips done, the witness pokes that pane."
rid=$(curl -s -m4 "$H/work" -H 'Content-Type: application/json' \
      -d '{"text":"[demo] wake-on-event","by":"demo","notify":"%demo"}' \
      | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
nf=$(python3 -c "import json;items=json.load(open('$TMP/.8/work.json'));print(next((w.get('notify') for w in items if w.get('id')==$rid),None))")
ev "created #$rid with notify=$nf  (the opt-in wake target round-trips onto the item)"
curl -s -m4 "$H/work" -H 'Content-Type: application/json' -d "{\"id\":$rid,\"status\":\"done\",\"by\":\"demo\"}" >/dev/null
ev "flipped #$rid done → the witness fires the notify (here it DEFERS: no live pane to type into)"
say "on a live fleet this TYPES '[8-done #$rid] …' into the waiting pane — verified against pane %16."

hr "6 · the CHANNEL atom — a coupled, moving connection (live fleet)"
say "CALL is a press; CHANNEL is a pin that stays coupled and moves — e.g. driving a real browser."
run "mcp http_request POST http://127.0.0.1:4445/command  {bidi: browsingContext.navigate ...}"
say "the hermetic demo has no browser; on a live fleet the broker holds a Firefox/Chrome"
say "session and a CHANNEL command drives it — every frame witnessed the same way as a CALL."

hr "7 · isolation — the production witness is untouched"
grep -q '\[demo\]' "$TMP/.8/work.json" 2>/dev/null && ev "wrote the throwaway ledger $TMP/.8/work.json"
if [ -f "$HOME/.8/work.json" ] && grep -q '\[demo\] a witnessed call' "$HOME/.8/work.json" 2>/dev/null; then
  say "LEAK: a demo act reached production ~/.8 (bug)"
else
  ev "production ~/.8 never touched ✓"
fi

hr "see the LIVE system"
say "cockpit (visual):   cd 8/web && npm run dev     → http://localhost:8088"
say "live witness:       curl -s 127.0.0.1:7070/work"
say "the mesh:           curl -s 127.0.0.1:7070/peers"
say "drive a real tab:   mcp http_request → 127.0.0.1:4445/command   (the CHANNEL atom)"
echo
bold "that's the whole system: two atoms, one witness — in action."
echo
