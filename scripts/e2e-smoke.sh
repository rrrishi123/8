#!/usr/bin/env bash
# e2e-smoke — the DYNAMIC release test: prove the four-system WORKS as a running
# whole, not just that each arm builds. system-smoke is static (build+test+lockstep
# per arm); this stands up the FRESHLY-BUILT collector and drives the CALL atom
# through it, asserting the WITNESS actually inscribes it (ledger-id advances +
# the record reads back) — the round-trip that gives real confidence in the
# release object.
#
# ISOLATION (non-negotiable): every ~/.8 path is $HOME-derived (workFile() =
# $HOME/.8/work.json), so we run the collector under a TEMP HOME on a spare port
# with no brokers/gecko. It writes a throwaway ledger and NEVER touches the
# production witness or ~/.8. Verified at the end.
#
#   ./e2e-smoke.sh            # build, run, assert, tear down; green/red exit
set -uo pipefail
# ROOT is the dir that HOLDS the four repos. Derive it from THIS script's own
# location (8/scripts/e2e-smoke.sh) so the test runs on any host / in CI, not
# only the operator's ~/Desktop/repos or ~/Work. Env ROOT still overrides.
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"   # .../8/scripts
ROOT="${ROOT:-$(cd "$SELF_DIR/../.." && pwd)}"                 # dir holding 8, http-mcp, ...
PORT="${PORT:-7099}"
COL="$ROOT/8/collector"
fail=0; note(){ printf '  %s\n' "$*"; }
grn(){ printf '\033[32m%s\033[0m\n' "$*"; }
red(){ printf '\033[31m%s\033[0m\n' "$*"; fail=1; }
H="http://127.0.0.1:$PORT"

echo "== e2e-smoke: CALL atom + witness round-trip, isolated =="

# spare port, or bail (never collide with a live collector)
if lsof -ti ":$PORT" >/dev/null 2>&1; then red "port $PORT busy — set PORT= to a free one"; exit 1; fi

# fresh build of the arm under test
( cd "$COL" && go build -o collector . ) || { red "collector build FAIL"; exit 1; }
note "built collector"

# isolated HOME — relocates work.json, gecko.json, inbox, stream all at once
TMP=$(mktemp -d)
mkdir -p "$TMP/.8"
cleanup(){ [ -n "${cpid:-}" ] && kill "$cpid" 2>/dev/null; rm -rf "$TMP"; }
trap cleanup EXIT

# start the isolated collector: no brokers, no gecko, temp HOME, spare port.
# unset TMUX so it doesn't try to witness this session's real tmux server.
( cd "$COL" && env -u TMUX HOME="$TMP" ./collector -listen ":$PORT" >"$TMP/col.log" 2>&1 & echo $! >"$TMP/pid" )
cpid=$(cat "$TMP/pid")
note "isolated collector pid=$cpid HOME=$TMP port=$PORT"

# wait for /health alive
up=0; for _ in $(seq 1 20); do curl -s -m 2 "$H/health" | grep -q '"alive":true' && { up=1; break; }; sleep 0.5; done
[ "$up" = 1 ] && grn "/health alive" || { red "collector never became healthy — log:"; tail -5 "$TMP/col.log" | sed 's/^/    /'; exit 1; }

# --- CALL atom: POST /work, assert 200 + ok:true + witness headers advance ---
marker="e2e-$(date +%s)-$RANDOM"
before=$(curl -s -D - -o /dev/null -m 4 "$H/health" | tr -d '\r' | awk -F': ' 'tolower($1)=="x-8-ledger-id"{print $2}')
resp=$(curl -s -D "$TMP/h" -m 6 "$H/work" -H 'Content-Type: application/json' \
  -d "{\"kind\":\"record\",\"status\":\"done\",\"by\":\"e2e\",\"text\":\"$marker\"}")
hdrs=$(tr -d '\r' <"$TMP/h")
echo "$resp" | grep -q '"ok":true' && grn "CALL /work accepted ($resp)" || red "CALL /work not ok: $resp"
echo "$hdrs" | grep -qi '^X-8-Witness:' && grn "witness header present ($(echo "$hdrs" | awk -F': ' 'tolower($1)=="x-8-witness"{print $2}'))" || red "no X-8-Witness header"
after=$(echo "$hdrs" | awk -F': ' 'tolower($1)=="x-8-ledger-id"{print $2}')
if [ -n "$before" ] && [ -n "$after" ] && [ "$after" -gt "$before" ] 2>/dev/null; then grn "ledger-id advanced $before -> $after"; else red "ledger-id did not advance ($before -> $after)"; fi

# --- witness inscription: the record reads back from the ledger ---
curl -s -m 4 "$H/work" | grep -q "$marker" && grn "record reads back from /work (witnessed + queryable)" || red "record NOT found on read-back"

# --- isolation proof: wrote the TEMP ledger, NOT the production one ---
grep -q "$marker" "$TMP/.8/work.json" 2>/dev/null && grn "wrote isolated $TMP/.8/work.json" || red "isolated ledger missing the record"
if [ -f "$HOME/.8/work.json" ] && grep -q "$marker" "$HOME/.8/work.json" 2>/dev/null; then red "LEAK: marker landed in production ~/.8/work.json"; else grn "production ~/.8 untouched"; fi

[ "$fail" = 0 ] && { grn "== E2E GREEN: CALL atom round-trips and the witness inscribes it =="; exit 0; } || { red "== E2E RED =="; exit 1; }
