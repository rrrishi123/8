#!/bin/sh
# release-gate — is vX a coherent, cold-installable four-system release? REFUTABLE, read-only.
#
#   ROOT=~/Desktop/repos ./scripts/release-gate.sh v0.0.5          # gate the tag that exists
#   ROOT=~/Desktop/repos ./scripts/release-gate.sh v0.0.5 --pre    # before the cut: assert the tag does NOT exist yet
#   INSTALL=1 ... release-gate.sh v0.0.5                            # also cold `go install` each arm and run --version
#
# Every assertion is something a reviewer used to check by eye, or did not:
#   T1 tag vX exists on origin for every arm            T2 tag == origin/main on every arm (#9559 hygiene)
#   T3 8 also carries collector/vX at the SAME commit    (nested module; v0.0.4 shipped without it at first)
#   T4 cold resolve: `go list -m <module>@vX` from an EMPTY module cache, every arm (what `go install` sees)
#   T5 version literals AT THE TAG agree with vX (5 code sites + README baseline x4) — the lockstep, read from
#      `git show vX:path`, not from a working tree
#   T6 (INSTALL=1) cold `go install` into a scratch GOBIN and `--version` prints vX
# Exit 0 = GREEN (release coheres), 1 = RED (one or more assertions refuted). No network writes, ever.
set -u
V="${1:-}"; MODE="${2:-}"
[ -n "$V" ] || { echo "usage: release-gate.sh vX.Y.Z [--pre]"; exit 2; }
SELF_DIR=$(cd "$(dirname "$0")" && pwd)
ROOT="${ROOT:-$(cd "$SELF_DIR/../.." && pwd)}"
fail=0
red()  { printf '\033[31mRED  %s\033[0m\n' "$*"; fail=1; }
grn()  { printf '\033[32mok   %s\033[0m\n' "$*"; }
note() { printf '     %s\n' "$*"; }
ARMS="http-mcp pilot adapters 8"
mod() { case "$1" in 8) echo github.com/rrrishi123/8/collector;; *) echo "github.com/rrrishi123/$1";; esac; }
remote_sha() { git -C "$ROOT/$1" ls-remote --tags origin "refs/tags/$2^{}" 2>/dev/null | cut -f1 | head -1; }
remote_sha_light() { git -C "$ROOT/$1" ls-remote --tags origin "refs/tags/$2" 2>/dev/null | cut -f1 | head -1; }
main_sha() { git -C "$ROOT/$1" ls-remote origin refs/heads/main 2>/dev/null | cut -f1; }

echo "== release-gate $V (ROOT=$ROOT) =="
for r in $ARMS; do [ -e "$ROOT/$r/.git" ] || { red "$r: not checked out under ROOT"; }; done
[ "$fail" = 0 ] || exit 1

if [ "$MODE" = "--pre" ]; then
  echo "-- T0 pre-cut: $V must not exist yet (tags are immutable on the Go proxy) --"
  for r in $ARMS; do
    s=$(remote_sha_light "$r" "$V"); [ -z "$s" ] && grn "$r: no $V yet" || red "$r: $V ALREADY EXISTS at ${s%"${s#???????}"} — do not re-tag"
  done
fi

echo "-- T1/T2 tag on origin + tag==main --"
for r in $ARMS; do
  t=$(remote_sha "$r" "$V"); [ -n "$t" ] || t=$(remote_sha_light "$r" "$V")
  m=$(main_sha "$r")
  if [ -z "$t" ]; then [ "$MODE" = "--pre" ] && note "$r: (pre-cut) tag absent, skipping T2" || red "$r: tag $V not on origin"; continue; fi
  grn "$r: tag $V = ${t%"${t#???????}"}"
  [ "$t" = "$m" ] && grn "$r: main == $V" || red "$r: main (${m%"${m#???????}"}) != $V (${t%"${t#???????}"}) — #9559: ff the tagged commit into main, or main carries untagged work"
done

echo "-- T3 nested module tag collector/$V in 8 --"
c=$(remote_sha 8 "collector/$V"); [ -n "$c" ] || c=$(remote_sha_light 8 "collector/$V"); t8=$(remote_sha 8 "$V"); [ -n "$t8" ] || t8=$(remote_sha_light 8 "$V")
if [ -z "$c" ]; then [ "$MODE" = "--pre" ] && note "8: (pre-cut) collector/$V absent" || red "8: collector/$V MISSING — 8 has no root go.mod; go install …/8/collector@$V cannot resolve"
elif [ "$c" != "$t8" ]; then red "8: collector/$V (${c%"${c#???????}"}) != $V (${t8%"${t8#???????}"}) — the two tags must name one commit"
else grn "8: collector/$V present and == $V"; fi

echo "-- T4 cold resolve from an empty module cache --"
if [ "$MODE" = "--pre" ]; then note "(pre-cut) skipped — nothing to resolve yet"; else
  TMP=$(mktemp -d 2>/dev/null || mktemp -d -t relgate); mkdir -p "$TMP/cache" "$TMP/gopath" "$TMP/bin"
  for r in $ARMS; do
    out=$(cd "$TMP" && GOMODCACHE="$TMP/cache" GOPATH="$TMP/gopath" GOFLAGS= GOPROXY=https://proxy.golang.org,direct GONOSUMDB=github.com/rrrishi123 go list -m "$(mod "$r")@$V" 2>&1 | tail -1)
    case "$out" in *" $V"|*" $V "*) grn "$r: $(mod "$r")@$V resolves";; *) red "$r: cold resolve FAILED: $out";; esac
  done
  if [ "${INSTALL:-0}" = 1 ]; then
    echo "-- T6 cold go install + --version --"
    for spec in "http-mcp:github.com/rrrishi123/http-mcp/cmd/mcp:http-mcp" "pilot:github.com/rrrishi123/pilot:pilot" "adapters:github.com/rrrishi123/adapters/cmd/adapters:adapters" "8:github.com/rrrishi123/8/collector:collector"; do
      r=${spec%%:*}; rest=${spec#*:}; pkg=${rest%%:*}; bin=${rest#*:}
      if (cd "$TMP" && GOMODCACHE="$TMP/cache" GOPATH="$TMP/gopath" GOBIN="$TMP/bin" GOFLAGS= GOPROXY=https://proxy.golang.org,direct GONOSUMDB=github.com/rrrishi123 go install "$pkg@$V" >"$TMP/$r.log" 2>&1); then
        v=$("$TMP/bin/$bin" --version 2>&1 | head -1); case "$v" in *"$V"*) grn "$r: installed, --version → $v";; *) red "$r: installed but --version says '$v' (expected $V — literal not bumped before the tag?)";; esac
      else red "$r: go install $pkg@$V FAILED: $(tail -1 "$TMP/$r.log")"; fi
    done
  fi
  rm -rf "$TMP"
fi

echo "-- T5 version literals AT THE TAG (lockstep) --"
at() { git -C "$ROOT/$1" fetch -q origin "refs/tags/$3:refs/tags/$3" 2>/dev/null; git -C "$ROOT/$1" show "$3:$2" 2>/dev/null | grep -cE "$4"; }
ref="$V"; [ "$MODE" = "--pre" ] && ref="origin/next" && note "(pre-cut) reading literals from origin/next instead of the tag"
atref() { if [ "$MODE" = "--pre" ]; then git -C "$ROOT/$1" fetch -q origin next 2>/dev/null; git -C "$ROOT/$1" show "origin/next:$2" 2>/dev/null | grep -cE "$3"; else at "$1" "$2" "$V" "$3"; fi; }
chk() { n=$(atref "$1" "$2" "$3"); [ "${n:-0}" = 1 ] && grn "$1/$2: $4 = $V" || red "$1/$2: $4 != $V (matches=${n:-0})"; }
chk http-mcp contract/contract.go "Version = \"$V\"" contract.Version
chk adapters trace/trace.go "Version = \"$V\"" trace.Version
chk pilot main.go "fallbackVersion = \"$V\"" fallbackVersion
chk 8 collector/main.go "seriesContractVersion = \"$V\"" seriesContractVersion
chk 8 install.sh "VER=\"$V\"" install.sh.VER
for r in $ARMS; do chk "$r" README.md "Baseline: \\*\\*\`$V\`\\*\\* across all four arms" README.baseline; done

echo
[ "$fail" = 0 ] && { printf '\033[32m== RELEASE GATE GREEN: %s coheres as one object ==\033[0m\n' "$V"; exit 0; } || { printf '\033[31m== RELEASE GATE RED: %s refuted above ==\033[0m\n' "$V"; exit 1; }
