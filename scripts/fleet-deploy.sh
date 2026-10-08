#!/usr/bin/env bash
# fleet-deploy — propagate the release to EVERY host in parallel, test-gated.
# For each host it runs fleet-sync (track upstream + rebuild) and then
# system-smoke (verify the release coheres as one object); a host that fails the
# smoke is reported RED and its services are NOT bounced. This is the efferent
# half of the fleet loop — "get all changes to all hosts" — with the test gate
# you asked for baked in, so a bad release can't silently propagate.
#
#   ./fleet-deploy.sh --dry-run          # show the plan per host, change nothing
#   ./fleet-deploy.sh                    # sync+smoke every host, in parallel
#
# Hosts: "local" runs here; "name@host:ROOT" runs over ssh. Tailscale ssh must be
# authed for remote hosts (a broken door is reported, not fatal to the others).
set -uo pipefail
DRY=0; [ "${1:-}" = "--dry-run" ] && DRY=1
FLEET="${FLEET:-local rishi@omarchy:/home/rishi/Work}"
LR="${LR:-$HOME/Desktop/repos}"                       # local repos root
sc(){ printf '\033[32m%s\033[0m\n' "$*"; }; rd(){ printf '\033[31m%s\033[0m\n' "$*"; }

one() { # host-spec
  local spec="$1" tag out rc
  if [ "$spec" = "local" ]; then
    tag="local"
    [ $DRY = 1 ] && { echo "[$tag] would: fleet-sync + system-smoke @ $LR"; return; }
    out=$(cd "$LR/8" && ROOT="$LR" ./scripts/fleet-sync.sh 2>&1 && ROOT="$LR" ./scripts/system-smoke.sh 2>&1); rc=$?
  else
    local userhost="${spec%%:*}" rroot="${spec#*:}"; tag="$userhost"
    if [ $DRY = 1 ]; then echo "[$tag] would: ssh -> fleet-sync + system-smoke @ $rroot"; return; fi
    # probe the ssh door first so a re-auth wall is a clean report, not a hang
    if ! ssh -o BatchMode=yes -o ConnectTimeout=8 "$userhost" true 2>/dev/null; then rd "[$tag] ssh unreachable (Tailscale re-auth?) — skipped"; return; fi
    # BOOTSTRAP: the host may not have the scripts yet. Fetch upstream and extract
    # them from FETCH_HEAD into /tmp (no working-tree change) so a first deploy
    # works, then run the sync (which brings in everything) + the smoke gate.
    out=$(ssh -o BatchMode=yes -o ConnectTimeout=12 "$userhost" bash -s <<BOOT 2>&1
set -e
cd "$rroot/8"
git fetch -q origin ${UPSTREAM:-release/v0.0.2}
git show FETCH_HEAD:scripts/fleet-sync.sh   > /tmp/fleet-sync.sh
git show FETCH_HEAD:scripts/system-smoke.sh > /tmp/system-smoke.sh
ROOT="$rroot" bash /tmp/fleet-sync.sh
ROOT="$rroot" bash /tmp/system-smoke.sh
BOOT
); rc=$?
  fi
  if [ $rc = 0 ] && printf '%s' "$out" | grep -q 'SMOKE GREEN'; then sc "[$tag] deployed + smoke GREEN"
  else rd "[$tag] FAILED (rc=$rc) — services NOT bounced"; printf '%s\n' "$out" | grep -iE 'FAIL|RED|conflict|error' | head -3 | sed "s/^/    /"; fi
}

echo "== fleet-deploy  $([ $DRY = 1 ] && echo '(dry-run)')  hosts: $FLEET"
pids=""
for h in $FLEET; do one "$h" & pids="$pids $!"; done   # parallel
for p in $pids; do wait "$p"; done
echo "== fleet-deploy done =="
