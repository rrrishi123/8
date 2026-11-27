#!/usr/bin/env bash
# cut-release.sh — the v0.0.5 cut, GATED on grounded consensus, executed mechanically.
#
# Rishi delegated the cut to consensus ("take consensus instead of asking me").
# So this script does NOT cut on anyone's say-so. It cuts iff the consensus organ
# has self-closed the proposal on its Need distinct grounded agrees with zero
# standing disagree — the witness is the authority, not a person and not %6.
# Trust the gate, not the diff; here, trust the consensus, not the proposer.
#
# Gate (GREEN) requires the proposal workitem to be:
#   status == done  AND  flipped_by starts with "consensus:"
# which the ledger sets only when Need grounded agrees land and none disagree.
#
# On GREEN, for each arm in lockstep: FF main<-next, tag, push tag, GitHub Release.
# Safety: aborts if a tag already exists or main is not a fast-forward of next.
# Default is DRY-RUN (prints the plan + gate). Pass --execute to actually cut.
#
# Usage: scripts/cut-release.sh <version> <proposal_id> [--execute] [collector_url]
#   scripts/cut-release.sh v0.0.5 9812            # dry-run, shows gate
#   scripts/cut-release.sh v0.0.5 9812 --execute  # cuts iff consensus GREEN
set -euo pipefail
VER="${1:?version e.g. v0.0.5}"; PROP="${2:?consensus proposal id}"
EXECUTE=0; COLLECTOR="http://127.0.0.1:7070"
for a in "${@:3}"; do case "$a" in --execute) EXECUTE=1;; http*) COLLECTOR="$a";; esac; done
REPO_ROOT="${EIGHT_REPOS:-$HOME/Desktop/repos}"
ARMS=(8 http-mcp pilot adapters)
# repo == rrrishi123/<arm> for all four; no assoc array (macOS /bin/bash is 3.2, no `declare -A`).
gh_repo() { echo "rrrishi123/$1"; }

# --- consensus gate (grounded; the organ self-closed the proposal or it did not) ---
gate=$(curl -s "$COLLECTOR/work" | PROP="$PROP" python3 -c '
import sys,json,os
w=json.load(sys.stdin)["work"]; pid=int(os.environ["PROP"])
it=next((x for x in w if x.get("id")==pid),None)
if not it: print("MISSING - proposal not found"); sys.exit()
st=it.get("status"); fb=str(it.get("flipped_by") or it.get("flippedBy") or "")
acks=it.get("acks") or it.get("Acks") or []
ag=sum(1 for a in acks if str(a.get("verdict","")).startswith("agree"))
dis=sum(1 for a in acks if str(a.get("verdict","")).startswith("disagree"))
green = (st=="done" and fb.startswith("consensus:"))
print("%s status=%s flipped_by=%s agrees=%d disagrees=%d need=%s"%(
    "GREEN" if green else "RED", st, fb or "-", ag, dis, it.get("need") or it.get("Need")))
')
echo "── cut-gate: proposal #$PROP for $VER ──"
echo "  consensus: $gate"
if [[ "$gate" != GREEN* ]]; then
  echo "  VERDICT: RED — not cutting. Consensus has not self-closed the proposal."
  echo "           (refutable: the ring lands its grounded agrees, the organ flips it, rerun.)"
  exit 1
fi

# --- grounded pre-cut safety across all arms (before touching anything) ---
echo "── pre-cut safety (all arms must pass before any push) ──"
for arm in "${ARMS[@]}"; do
  cd "$REPO_ROOT/$arm"; git fetch origin --tags -q 2>/dev/null
  git merge-base --is-ancestor origin/main origin/next 2>/dev/null || { echo "  ABORT $arm: main is NOT a fast-forward of next"; exit 2; }
  git ls-remote --tags origin "$VER" 2>/dev/null | grep -q "refs/tags/$VER$" && { echo "  ABORT $arm: tag $VER already exists"; exit 2; }
  # the 8 arm ships a NESTED go module (collector/) — go install .../collector@$VER
  # needs the module-path tag collector/$VER or it 404s (this is exactly how v0.0.4 shipped broken).
  if [ "$arm" = "8" ]; then
    git ls-remote --tags origin "collector/$VER" 2>/dev/null | grep -q "refs/tags/collector/$VER$" && { echo "  ABORT $arm: nested tag collector/$VER already exists"; exit 2; }
    echo "  $arm: FF-able, no $VER and no collector/$VER tag — OK ($(git rev-parse --short origin/next))"
  else
    echo "  $arm: FF-able, no $VER tag — OK ($(git rev-parse --short origin/next))"
  fi
done

if [ "$EXECUTE" != "1" ]; then
  echo "── DRY-RUN (consensus GREEN, safety OK). Pass --execute to cut. Would run per arm: ──"
  for arm in "${ARMS[@]}"; do
    if [ "$arm" = "8" ]; then echo "  $arm: push next:main · tag $VER + nested collector/$VER · push both · gh release create $VER";
    else echo "  $arm: push next:main · tag $VER · push tag · gh release create $VER"; fi
  done
  exit 0
fi

# --- execute the lockstep cut ---
echo "── EXECUTING cut $VER (consensus #$PROP GREEN) ──"
for arm in "${ARMS[@]}"; do
  cd "$REPO_ROOT/$arm"; sha=$(git rev-parse origin/next)
  echo "  [$arm] FF main -> $sha"; git push origin "origin/next:refs/heads/main" 2>&1 | tail -1
  echo "  [$arm] tag $VER";        git tag "$VER" "$sha" && git push origin "$VER" 2>&1 | tail -1
  if [ "$arm" = "8" ]; then
    # nested go-module tag so `go install .../collector@$VER` resolves (the v0.0.4 break)
    echo "  [$arm] nested tag collector/$VER"; git tag "collector/$VER" "$sha" && git push origin "collector/$VER" 2>&1 | tail -1
  fi
  echo "  [$arm] release";         gh release create "$VER" --repo "$(gh_repo "$arm")" --target "$sha" --title "$VER" --notes "four-system $VER — lockstep cut, consensus #$PROP" 2>&1 | tail -1
done
echo "── cut done. release-gate self-verifies on the landed tags. ──"
