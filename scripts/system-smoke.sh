#!/usr/bin/env bash
# system-smoke — verify the four-system release as ONE object, not four repos in
# isolation (the #1149 system-CI, made runnable). Builds + tests every arm at its
# current release/v0.0.2 checkout, asserts the version literals are in lockstep,
# and reports the composed tips. Exit non-zero on any failure.
#
#   ROOT=~/Desktop/repos ./system-smoke.sh
set -uo pipefail
ROOT="${ROOT:-$HOME/Desktop/repos}"
fail=0; note() { printf '  %s\n' "$*"; }
red()  { printf '\033[31m%s\033[0m\n' "$*"; fail=1; }
grn()  { printf '\033[32m%s\033[0m\n' "$*"; }

echo "== four-system smoke @ release/v0.0.2 =="

# --- per-arm build + test (the arm's own CI, run together) ---
arm() { # repo  test-dir
  local r="$1" td="$2" d="$ROOT/$1"
  [ -d "$d/.git" ] || { red "$r: missing"; return; }
  local sha; sha=$(git -C "$d" rev-parse --short HEAD)
  ( cd "$d/$td" && go build ./... >/dev/null 2>&1 ) || { red "$r@$sha: build FAIL"; return; }
  local out; out=$( cd "$d/$td" && go test ./... 2>&1 )
  if printf '%s' "$out" | grep -qE '^(FAIL|--- FAIL|panic)'; then red "$r@$sha: test FAIL"; printf '%s\n' "$out" | grep -E 'FAIL|panic' | head -3
  else grn "$r@$sha: build+test ok ($(printf '%s' "$out" | grep -c '^ok') pkgs)"; fi
}
arm http-mcp .
arm adapters .
arm pilot    .
arm 8        collector

# --- web arm (8/web) ---
if ! command -v npx >/dev/null 2>&1; then note "8/web: skipped (no npx on PATH — Go arms are the release gate)"
elif ( cd "$ROOT/8/web" && npx tsc --noEmit >/dev/null 2>&1 ); then grn "8/web: tsc ok"
else red "8/web: tsc FAIL"; fi

# --- version-literal lockstep (the drift guard, as a gate) ---
echo "-- version lockstep --"
lit() { grep -cE "$3" "$ROOT/$1/$2" 2>/dev/null; }   # read the CHECKED-OUT file (HEAD) — works on release/v0.0.2 or a private kosaten branch that merged it
c=$(lit http-mcp contract/contract.go 'Version = "v0.0.2"')
a=$(lit adapters trace/trace.go 'Version = "v0.0.2"')
p=$(lit pilot main.go 'fallbackVersion = "v0.0.2"')
s=$(lit 8 collector/main.go 'seriesContractVersion = "v0.0.2"')
v=$(lit 8 install.sh 'VER="v0.0.3"')
if [ "$c$a$p$s$v" = "11111" ]; then grn "lockstep ok (4x v0.0.2 + install.sh v0.0.3-ahead)"; else red "lockstep DRIFT: contract=$c adapters=$a pilot=$p series=$s install=$v"; fi

# --- composed tips (the release object) ---
echo "-- composed release --"
for r in http-mcp 8 pilot adapters; do note "$r $(git -C "$ROOT/$r" rev-parse --short HEAD) [$(git -C "$ROOT/$r" rev-parse --abbrev-ref HEAD)]"; done

[ "$fail" = 0 ] && { grn "== SMOKE GREEN: release coheres as one object =="; exit 0; } || { red "== SMOKE RED =="; exit 1; }
