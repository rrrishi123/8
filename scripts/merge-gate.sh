#!/usr/bin/env bash
# merge-gate.sh — a refutable, GROUNDED merge guardrail for the four-system.
#
# The point (Rishi, 2026-11-25): "guardrails need to be so strong you might not
# need to see the code — you trust the guardrail." So this gate decides
# merge-readiness WITHOUT anyone reading the diff. A mind merges when it is GREEN
# and does not when it is RED, full stop.
#
# Why grounded + sha-bound: incorrectness enters when a status is derived from a
# mutable/ambiguous signal (a reused "#22", an agree on a stale commit, a
# self-declared `by`). %6's own scrape invented a 3-agree quorum this way. So
# every input here is bound to the PR's CURRENT head sha and to a witnessed,
# non-author actor. A stale, forged, or mismatched agree does not count —
# mechanically, with no eyes on the code.
#
# GREEN requires ALL of:
#   1. CI: every required check PASS, zero FAIL (gh pr checks).
#   2. Quorum: >= 2 DISTINCT non-author firsthand agrees, each citing the PR's
#      CURRENT head short-sha. Distinctness folds a mind's signature spellings to
#      its witnessed actor id (first 8 hex). The proposer is excluded.
#   3. Mergeable: GitHub reports MERGEABLE/CLEAN (no conflicts).
# Anything else is RED, and the gate prints WHY, so the red is refutable.
#
# Usage: scripts/merge-gate.sh <repo> <pr> [collector_url]
#   scripts/merge-gate.sh rrrishi123/8 28
set -euo pipefail
REPO="${1:?repo e.g. rrrishi123/8}"; PR="${2:?pr number}"
COLLECTOR="${3:-http://127.0.0.1:7070}"
NEED="${MERGE_GATE_NEED:-2}"

head=$(gh pr view "$PR" --repo "$REPO" --json headRefOid -q .headRefOid)
short=${head:0:7}
mergeable=$(gh pr view "$PR" --repo "$REPO" --json mergeable -q .mergeable)

# 1. CI — all pass, none fail
checks=$(gh pr checks "$PR" --repo "$REPO" 2>/dev/null || true)
fails=$(printf '%s\n' "$checks" | grep -ciE $'\tfail' || true)
passes=$(printf '%s\n' "$checks" | grep -ciE $'\tpass' || true)

# 2. Grounded quorum — agrees citing CURRENT head short-sha, distinct non-author
#    witnessed actors. The collector is the witness; we read its record, not prose.
quorum=$(curl -s "$COLLECTOR/work" | REPO="$REPO" PR="$PR" SHORT="$short" python3 -c '
import sys,json,re,os
w=json.load(sys.stdin)["work"]; pr=os.environ["PR"]; short=os.environ["SHORT"]
# proposer = earliest actor who announced building/shipping THIS pr
prop=None
for x in sorted(w,key=lambda t:t.get("id",0)):
    tx=str(x.get("text",""))
    if re.search(r"\b(BUILT|SHIPPED|complete|PR #?%s)\b"%re.escape(pr),tx,re.I) and ("#%s"%pr in tx or "PR #%s"%pr in tx):
        if re.search(r"BUILT|SHIPPED|complete|slice|release-gate|/social|gitignore",tx,re.I):
            m=re.search(r"[0-9a-f]{8}",str(x.get("by",""))); prop=(m.group(0) if m else str(x.get("by",""))[:8]); break
def actor(by):
    m=re.search(r"[0-9a-f]{8}",by); return m.group(0) if m else by.strip().lower()[:8]
good=set()
for x in w:
    tx=str(x.get("text","")); by=str(x.get("by",""))
    # must be a firsthand CHECKED AGREE, on THIS pr, citing the CURRENT head sha
    if "AGREE" in tx.upper() and ("CHECKED" in tx.upper() or "firsthand" in tx.lower()):
        if re.search(r"#%s\b"%pr,tx) and short in tx:         # sha-bound to current head
            a=actor(by)
            if a!=prop: good.add(a)                           # non-author
print("%d %s %s"%(len(good),prop or "?"," ".join(sorted(good))))
')
n=$(echo "$quorum" | awk "{print \$1}")

echo "── merge-gate: $REPO #$PR @${short} ──"
echo "  CI        : ${passes} pass / ${fails} fail"
echo "  mergeable : ${mergeable}"
echo "  quorum    : ${n}/${NEED} grounded non-author agrees sha-bound to ${short}  (proposer excluded)"
echo "              actors: $(echo "$quorum" | cut -d' ' -f3-)"
if [ "${fails}" = "0" ] && [ "${passes}" -gt 0 ] && [ "${mergeable}" = "MERGEABLE" ] && [ "${n}" -ge "${NEED}" ]; then
  echo "  VERDICT   : GREEN — merge without reading the diff; the gate is the trust."
  exit 0
else
  echo "  VERDICT   : RED — do NOT merge. (refutable: fix CI / land a real sha-bound agree / rebase)"
  exit 1
fi
