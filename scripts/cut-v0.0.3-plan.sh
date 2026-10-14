#!/usr/bin/env bash
# cut-v0.0.3-plan — the OPERATOR's cut plan for v0.0.3, verified and printed.
#
# This script NEVER merges, tags, or pushes. It only VERIFIES the preconditions
# are green and PRINTS the exact commands the operator runs by hand when they
# greenlight (#1138). The recipe is cowork's merge review + philo's tag-safety
# note + the release manifest: http-mcp takes the release side on its two
# conflicted files, three fast-forwards, five version literals moved together,
# fresh v0.0.3 tags, then the four tagged commits smoked AS A SET so install.sh
# finally resolves its version. Read it, run the printed commands yourself.
set -uo pipefail
R="${EIGHT_REPOS:-$HOME/Desktop/repos}"
grn(){ printf '\033[32m%s\033[0m\n' "$*"; }; red(){ printf '\033[31m%s\033[0m\n' "$*"; }
ok=1

echo "== v0.0.3 cut — PRECONDITIONS (read-only) =="
# 1. all four arms on release/v0.0.2, clean
for r in http-mcp 8 pilot adapters; do
  br=$(git -C "$R/$r" rev-parse --abbrev-ref HEAD 2>/dev/null)
  dirty=$(git -C "$R/$r" status --porcelain 2>/dev/null | wc -l | tr -d ' ')
  [ "$br" = release/v0.0.2 ] && [ "$dirty" = 0 ] && grn "  $r: on release/v0.0.2, clean ($(git -C "$R/$r" rev-parse --short HEAD))" \
    || { red "  $r: branch=$br dirty=$dirty (want release/v0.0.2, clean)"; ok=0; }
done
# 2. v0.0.3 tags absent (so the cut mints fresh; v0.0.2 stays immutable)
for r in http-mcp 8 pilot adapters; do
  t=$(git -C "$R/$r" ls-remote --tags origin 'v0.0.3' 'collector/v0.0.3' 2>/dev/null | wc -l | tr -d ' ')
  [ "$t" = 0 ] && grn "  $r: v0.0.3 tags absent — cut mints fresh" || { red "  $r: v0.0.3 tag ALREADY EXISTS — do not re-tag"; ok=0; }
done
# 3. release smokes as one object
if ROOT="$R" "$R/8/scripts/system-smoke.sh" >/tmp/cut-smoke.log 2>&1; then grn "  system-smoke: GREEN (release coheres as one object)"; else red "  system-smoke: RED — see /tmp/cut-smoke.log"; ok=0; fi

[ "$ok" = 1 ] && grn "== ALL PRECONDITIONS GREEN — the cut is ready ==" || { red "== NOT READY — fix the reds above before cutting =="; exit 1; }

cat <<'PLAN'

== THE CUT — commands for the OPERATOR to run by hand (nothing below is executed) ==
# per-repo, from $EIGHT_REPOS. http-mcp is the only conflicted merge (2 files, release side).

# 1) http-mcp -> main, taking the release side on the two conflicted files:
#    cd http-mcp && git checkout main && git merge -X theirs release/v0.0.2   # (README.md, cmd/wire — release wins)
# 2) fast-forward the other three:
#    for r in 8 pilot adapters; do (cd $r && git checkout main && git merge --ff-only release/v0.0.2); done
# 3) bump the FIVE version literals v0.0.2 -> v0.0.3 together (install.sh is already v0.0.3):
#    http-mcp/contract/contract.go  Version="v0.0.3"
#    adapters/trace/trace.go        Version="v0.0.3"
#    pilot/main.go                  fallbackVersion="v0.0.3"
#    8/collector/main.go            seriesContractVersion="v0.0.3"
#    (commit the bump on each arm)
# 4) mint fresh tags on the cut commits:
#    each repo:  git tag v0.0.3
#    8 also:     git tag collector/v0.0.3
# 5) push (the publish step #1138 gates — operator only):
#    each repo:  git push origin main && git push origin --tags
# 6) THE NEVER-DONE ACT — smoke the FOUR TAGGED COMMITS AS A SET, then verify install.sh
#    resolves v0.0.3 (go install @v0.0.3 across the modules + the curl|sh path).

# v0.0.2 tags stay as-is (immutable; the go proxy caches them). Never re-tag v0.0.2.
PLAN
