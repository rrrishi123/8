#!/usr/bin/env bash
# peer-beat — this host heartbeats the federation RENDEZVOUS so it shows up on
# the 8 portal (#886, PUSH-based: a peer that stops beating ages out after 90s,
# so the roster is self-verifying). Every INTERVAL it POSTs {host, actor,
# hostres} to RENDEZVOUS/peers, attaching its own /hostres so the portal can
# draw load/mem per host. This is the missing organ: the rendezvous existed but
# nobody was beating, so /peers was empty and no host — outer or inner — showed.
#
#   RENDEZVOUS=http://<mac-tailscale-ip>:7070 PEER_HOST=omarchy ./peer-beat.sh &
#   ./peer-beat.sh &        # mac: defaults to the local collector as rendezvous
#
# Detached:  nohup ./scripts/peer-beat.sh >/tmp/peer-beat.log 2>&1 &
set -uo pipefail
RENDEZVOUS="${RENDEZVOUS:-http://127.0.0.1:7070}"    # the collector the Firefox UI reads /peers from
SELF="${SELF:-http://127.0.0.1:7070}"                # this host's own collector, for /hostres
HOST="${PEER_HOST:-$(hostname -s 2>/dev/null || hostname)}"
INTERVAL="${INTERVAL:-30}"                            # < peerStaleAfter (90s), with margin

# singleton per host+rendezvous (mkdir lock) — don't stack beats
LOCK="/tmp/8-peerbeat.${HOST}.lockdir"
if ! mkdir "$LOCK" 2>/dev/null; then
  o=$(cat "$LOCK/pid" 2>/dev/null)
  if [ -n "$o" ] && kill -0 "$o" 2>/dev/null; then echo "[peer-beat] already running (pid $o) for $HOST — exiting"; exit 0; fi
  rm -rf "$LOCK"; mkdir "$LOCK" 2>/dev/null || exit 0
fi
echo $$ >"$LOCK/pid"; trap 'rm -rf "$LOCK"' EXIT

echo "[peer-beat] $HOST -> $RENDEZVOUS/peers every ${INTERVAL}s"
while :; do
  hr=$(curl -s -m 4 "$SELF/hostres" 2>/dev/null); [ -n "$hr" ] || hr='{}'
  body=$(printf '{"host":"%s","actor":"peer-beat","hostres":%s}' "$HOST" "$hr")
  curl -s -m 6 "$RENDEZVOUS/peers" -H 'Content-Type: application/json' -H "X-8-Actor: peer-beat/$HOST" -d "$body" >/dev/null 2>&1 \
    || echo "[peer-beat $(date +%H:%M:%S)] beat to $RENDEZVOUS FAILED (unreachable?)"
  sleep "$INTERVAL"
done
