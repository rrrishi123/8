#!/usr/bin/env bash
# poke — wake an outside agent with a webhook when this host's ledger has work.
# An agent that cannot hold a connection to the wire (a hosted routine, a paused
# VM) registers a webhook; 8 POSTs to it so the agent wakes and works /work.
# OFF unless EIGHT_POKE_URL is set. The secret never lives in the repo: put both
# values in $EIGHT_POKE_ENV (default ~/.8/poke.env, chmod 600):
#
#   EIGHT_POKE_URL=https://…              # the routine's webhook URL
#   EIGHT_POKE_AUTH='Bearer …'            # the FULL Authorization header value
#
#   ./scripts/poke.sh                     # poke if /work has open items (rate-limited)
#   ./scripts/poke.sh --force "reason"    # poke now, regardless of open work
#
# Bounded: at most one poke per EIGHT_POKE_MIN_S (default 600s) per URL. Exit 0
# when off, held or sent; 1 only when the webhook refused.
set -uo pipefail
ENVF="${EIGHT_POKE_ENV:-$HOME/.8/poke.env}"
# shellcheck disable=SC1090
[ -r "$ENVF" ] && . "$ENVF"
URL="${EIGHT_POKE_URL:-}"; AUTH="${EIGHT_POKE_AUTH:-}"
C="${EIGHT_COLLECTOR:-http://127.0.0.1:7070}"
MIN_S="${EIGHT_POKE_MIN_S:-600}"
FORCE=0; [ "${1:-}" = --force ] && { FORCE=1; shift; }
REASON="${1:-open work on the ledger}"

[ -n "$URL" ] || { echo "[poke] off (EIGHT_POKE_URL unset; see $ENVF)"; exit 0; }
if [ -r "$ENVF" ] && [ "$(stat -c %a "$ENVF" 2>/dev/null || stat -f %Lp "$ENVF")" != 600 ]; then
  echo "[poke] warning: $ENVF should be chmod 600" >&2
fi

open=$(curl -s -m 5 "$C/work" | python3 -c 'import sys,json
try: d=json.load(sys.stdin); print(sum(1 for w in (d.get("work") or []) if w.get("status") not in ("done","closed","expired")))
except Exception: print(-1)')
if [ "$FORCE" = 0 ] && [ "${open:--1}" -le 0 ]; then echo "[poke] held (open work: $open)"; exit 0; fi

STAMP="${XDG_STATE_HOME:-$HOME/.local/state}/8/poke.$(printf %s "$URL" | cksum | cut -d' ' -f1)"
mkdir -p "$(dirname "$STAMP")"
if [ "$FORCE" = 0 ] && [ -f "$STAMP" ] && [ $(( $(date +%s) - $(cat "$STAMP") )) -lt "$MIN_S" ]; then
  echo "[poke] held (last poke < ${MIN_S}s ago)"; exit 0
fi

host="${PEER_HOST:-$(hostname -s 2>/dev/null || hostname)}"
ledger=$(curl -s -m 3 -D - -o /dev/null "$C/health" | tr -d '\r' | awk -F': ' 'tolower($1)=="x-8-ledger-id"{print $2}')
body=$(python3 -c 'import json,sys; print(json.dumps({"source":"8","host":sys.argv[1],"reason":sys.argv[2],"open_work":int(sys.argv[3]),"ledger_id":sys.argv[4] or None,"work":sys.argv[5]+"/work"}))' \
  "$host" "$REASON" "${open:--1}" "${ledger:-}" "$C")
# the Authorization value goes to curl on stdin (-H @-), never on argv or in a log
code=$(printf 'Authorization: %s\n' "$AUTH" | curl -s -m 15 -o /dev/null -w '%{http_code}' \
  -X POST -H 'Content-Type: application/json' ${AUTH:+-H @-} --data "$body" "$URL")
case "$code" in
  2*) date +%s > "$STAMP"; echo "[poke] sent ($code, open work: $open)";;
  *)  echo "[poke] webhook refused: HTTP $code" >&2; exit 1;;
esac
