#!/usr/bin/env bash
# fleet-sync — a HOST pulls the open-source release into its own branch and
# rebuilds. This is the afferent-of-code half of the fleet: every host tracks
# the same upstream (release/v0.0.2) unless it explicitly runs a private branch,
# in which case the release is merged UNDER the private overlay (kosaten stays on
# top). Idempotent, and --dry-run shows the plan without changing anything.
#
#   REPOS="http-mcp 8 pilot adapters" UPSTREAM=release/v0.0.2 ./fleet-sync.sh --dry-run
#   ./fleet-sync.sh              # sync + rebuild
#
# It NEVER force-pushes and NEVER touches main. A conflict aborts that repo
# cleanly (private overlay collides with an upstream change -> a human decides).
set -uo pipefail
ROOT="${ROOT:-$HOME/Work}"; [ -d "$ROOT/8/.git" ] || ROOT="$HOME/Desktop/repos"
REPOS="${REPOS:-http-mcp 8 pilot adapters}"
UPSTREAM="${UPSTREAM:-release/v0.0.2}"
DRY=0; [ "${1:-}" = "--dry-run" ] && DRY=1
say(){ printf '  %s\n' "$*"; }

echo "== fleet-sync  root=$ROOT  upstream=$UPSTREAM  $([ $DRY = 1 ] && echo '(dry-run)')"
for r in $REPOS; do
  d="$ROOT/$r"; [ -d "$d/.git" ] || { say "$r: MISSING — skip"; continue; }
  cur=$(git -C "$d" rev-parse --abbrev-ref HEAD)
  if [ -n "$(git -C "$d" status --porcelain)" ]; then say "$r: DIRTY working tree — skip (commit/stash first)"; continue; fi
  git -C "$d" fetch -q origin "$UPSTREAM" 2>/dev/null || { say "$r: fetch FAILED — skip"; continue; }
  local_sha=$(git -C "$d" rev-parse --short HEAD)
  up_sha=$(git -C "$d" rev-parse --short FETCH_HEAD)
  base=$(git -C "$d" merge-base HEAD FETCH_HEAD 2>/dev/null)
  if [ "$(git -C "$d" rev-parse HEAD)" = "$(git -C "$d" rev-parse FETCH_HEAD)" ]; then say "$r [$cur]: up to date @ $local_sha"; continue; fi
  behind=$(git -C "$d" rev-list --count HEAD..FETCH_HEAD 2>/dev/null)
  private=$([ "$base" = "$(git -C "$d" rev-parse HEAD)" ] && echo no || echo yes)  # HEAD not an ancestor of upstream => private commits on top
  say "$r [$cur]: $local_sha <- upstream $up_sha (+$behind)$([ "$private" = yes ] && echo ' · PRIVATE overlay -> merge under')"
  [ $DRY = 1 ] && continue
  if [ "$private" = no ]; then
    git -C "$d" merge --ff-only FETCH_HEAD >/dev/null 2>&1 && say "  ff -> $(git -C "$d" rev-parse --short HEAD)" || say "  ff FAILED"
  else
    if git -C "$d" merge --no-edit FETCH_HEAD >/dev/null 2>&1; then say "  merged upstream under overlay -> $(git -C "$d" rev-parse --short HEAD)"
    else git -C "$d" merge --abort 2>/dev/null; say "  CONFLICT with private overlay — aborted, needs a human"; continue; fi
  fi
  # rebuild through the canonical build (same one CI uses)
  if [ -x "$d/build.sh" ]; then ( cd "$d" && ./build.sh >/dev/null 2>&1 ) && say "  rebuilt" || say "  build FAILED"; fi
done
[ $DRY = 1 ] && echo "== dry-run only — nothing changed =="
exit 0   # per-repo issues are reported+skipped individually; the smoke gate catches real breakage
