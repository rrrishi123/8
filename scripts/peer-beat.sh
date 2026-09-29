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

# HOSTRES source: a peer WITH its own collector uses $SELF/hostres; a peer that
# has none (e.g. the colima VM / a container host) sets HOSTRES_CMD to a command
# whose stdout is the hostres JSON, so it still shows load/mem on the portal.
echo "[peer-beat] $HOST -> $RENDEZVOUS/peers every ${INTERVAL}s${HOSTRES_CMD:+ (hostres via cmd)}"
while :; do
  if [ -n "${HOSTRES_CMD:-}" ]; then hr=$(eval "$HOSTRES_CMD" 2>/dev/null)
  else hr=$(curl -s -m 4 "$SELF/hostres" 2>/dev/null); fi
  [ -n "$hr" ] || hr='{}'
  # BUDGET: carry this host's collective usage-left in the heartbeat, so /peers is
  # the ONE place that shows every host+provider's 5h/7d limits — the number that
  # decides who can be given work. Each host reports its OWN account (omarchy's is
  # a different Claude account than mac's; codex is a separate provider).
  if [ "${SKIP_BUDGET:-0}" = 1 ]; then bud='{}'   # host with no Claude account of its OWN that we sense (e.g. colima = browser seats + a cloud Claude read elsewhere)
  else
  # jq preserves null (unknown) providers instead of discarding the OTHER
  # provider's reading when one has no sensor. It also removes the Python dep.
  bud=$(curl -s -m 4 "$SELF/budget" 2>/dev/null | jq -c '{
    claude_5h: .budget.windows["5h"].utilization,
    claude_7d: .budget.windows["7d"].utilization,
    claude_gated: .budget.gated, claude_observed_at: .budget.observed_at,
    claude_5h_reset: .budget.windows["5h"].reset_at, claude_7d_reset: .budget.windows["7d"].reset_at,
    codex_5h: .providers.codex.windows["5h"].utilization,
    codex_7d: .providers.codex.windows["7d"].utilization,
    codex_gated: .providers.codex.gated, codex_observed_at: .providers.codex.observed_at,
    codex_5h_reset: .providers.codex.windows["5h"].reset_at, codex_7d_reset: .providers.codex.windows["7d"].reset_at
  }' 2>/dev/null)
  [ -n "$bud" ] || bud='{}'
  fi
  # BUILD SHA (T3 parity): the running collector's provenance, so /peers self-attests
  # each host's version and the hub flags drift with no human poking.
  build=$(curl -s -m 4 "$SELF/health" 2>/dev/null | jq -r '.build // ""' 2>/dev/null)
  # MANIFEST (federate this host's panes/tabs so /resolve host-qualifies them ACROSS
  # hosts): carry the reconciled LIVE tab list, so a host's tmux panes + browser tabs
  # show on the hub as <host>/<seat>/<tab>, not only the local ones. Trimmed to live +
  # essential fields to bound the 30s beat.
  man=$(curl -s -m 4 "$SELF/manifest" 2>/dev/null | jq -c '{tabs:[.tabs[]|select(.status=="live")|{uid,ctx,url,session,opened_by,why,status}]}' 2>/dev/null)
  [ -n "$man" ] || man='{}'
  body=$(printf '{"host":"%s","actor":"peer-beat","hostres":%s,"manifest":%s,"extra":{"budget":%s,"build":"%s"}}' "$HOST" "$hr" "$man" "$bud" "$build")
  curl -s -m 6 "$RENDEZVOUS/peers" -H 'Content-Type: application/json' -H "X-8-Actor: peer-beat/$HOST" -d "$body" >/dev/null 2>&1 \
    || echo "[peer-beat $(date +%H:%M:%S)] beat to $RENDEZVOUS FAILED (unreachable?)"
  sleep "$INTERVAL"
done
