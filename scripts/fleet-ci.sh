#!/usr/bin/env bash
# Test one immutable four-arm release. In Actions the caller's repository is
# checked out at its event SHA; the other arms are pinned by their checkouts.
# Only a green gate writes the manifest consumed by each host's deploy watcher.
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${ROOT:-$(cd "$script_dir/../.." && pwd)}"
SOURCE_REPOSITORY="${SOURCE_REPOSITORY:-${GITHUB_REPOSITORY:-}}"
SOURCE_SHA="${SOURCE_SHA:-${GITHUB_SHA:-}}"
RELEASE_MANIFEST="${RELEASE_MANIFEST:-$ROOT/fleet-release.json}"
arms=(8 http-mcp pilot adapters)
revisions=()

# Never retain an older successful manifest after any validation failure.
rm -f "$RELEASE_MANIFEST"
case "$SOURCE_REPOSITORY" in
  rrrishi123/8|rrrishi123/http-mcp|rrrishi123/pilot|rrrishi123/adapters) ;;
  *) echo "SOURCE_REPOSITORY must name one of the four release arms" >&2; exit 1 ;;
esac
[[ "$SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]] || { echo "SOURCE_SHA must be a full commit SHA" >&2; exit 1; }
for arm in "${arms[@]}"; do
  revision="$(git -C "$ROOT/$arm" rev-parse HEAD)"
  [[ "$revision" =~ ^[0-9a-f]{40}$ ]] || exit 1
  if [[ "rrrishi123/$arm" == "$SOURCE_REPOSITORY" && "$revision" != "$SOURCE_SHA" ]]; then
    echo "$arm: checkout does not match the push SHA" >&2
    exit 1
  fi
  if [[ -n "$(git -C "$ROOT/$arm" status --porcelain --untracked-files=normal)" ]]; then
    echo "$arm: gate requires a clean checkout" >&2
    exit 1
  fi
  revisions+=("$revision")
  printf '%s %s\n' "$arm" "$revision"
done

for arm in "${arms[@]}"; do
  (cd "$ROOT/$arm" && ./build.sh)
done
for module in 8/collector http-mcp pilot adapters adapters/webrtc; do
  (
    cd "$ROOT/$module"
    echo "== build, vet and test $module =="
    go build ./...
    go vet ./...
    go test ./...
    unformatted="$(gofmt -l .)"
    if [[ -n "$unformatted" ]]; then
      printf 'gofmt required in %s:\n%s\n' "$module" "$unformatted" >&2
      exit 1
    fi
  )
done

# Boot-to-first-seat smoke: the witness must not merely compile but actually
# start and reach a seat. Build the collector to a temp path (outside every
# checkout, so the clean-tree assertion below still holds), boot it browser-less
# on an isolated port with auth off, and require /health to report alive within
# a bounded window. This degraded boot is exactly what `collector up` promises
# when substrate is absent, so a green here means a host can boot the release.
echo "== boot-to-first-seat smoke (collector) =="
smoke_dir="$(mktemp -d)"
smoke_bin="$smoke_dir/collector-smoke"
smoke_log="$smoke_dir/collector-smoke.log"
smoke_port="${FLEET_SMOKE_PORT:-7199}"
(cd "$ROOT/8/collector" && go build -o "$smoke_bin" .)
EIGHT_TOKEN="" "$smoke_bin" -listen ":$smoke_port" >"$smoke_log" 2>&1 &
smoke_pid=$!
smoke_ok=""
for _ in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:$smoke_port/health" 2>/dev/null | grep -q '"alive":true'; then
    smoke_ok=1
    break
  fi
  sleep 0.5
done
kill "$smoke_pid" 2>/dev/null || true
wait "$smoke_pid" 2>/dev/null || true
if [[ -z "$smoke_ok" ]]; then
  echo "collector did not reach first seat (/health alive) — boot smoke failed" >&2
  tail -20 "$smoke_log" >&2 || true
  rm -rf "$smoke_dir"
  exit 1
fi
rm -rf "$smoke_dir"
echo "boot smoke: collector reached first seat on :$smoke_port"

node --test "$ROOT/8/scripts/fleet-agent.test.mjs"
(
  cd "$ROOT/8/web"
  npm ci
  npm run build
  npm test
)

# Catch a concurrently modified checkout before blessing it. Build outputs are
# gitignored; every manifest revision refers only to clean, committed source.
for i in "${!arms[@]}"; do
  arm="${arms[$i]}"
  [[ "$(git -C "$ROOT/$arm" rev-parse HEAD)" == "${revisions[$i]}" ]]
  [[ -z "$(git -C "$ROOT/$arm" status --porcelain --untracked-files=normal)" ]] || {
    echo "$arm: checkout changed while testing" >&2
    exit 1
  }
done

jq -n \
  --arg repository "$SOURCE_REPOSITORY" --arg sha "$SOURCE_SHA" \
  --arg eight "${revisions[0]}" --arg wire "${revisions[1]}" \
  --arg pilot "${revisions[2]}" --arg adapters "${revisions[3]}" \
  '{schema: 1, branch: "release/v0.0.2", source: {repository: $repository, sha: $sha},
    revisions: {"8": $eight, "http-mcp": $wire, pilot: $pilot, adapters: $adapters}}' \
  > "$RELEASE_MANIFEST"
echo "FLEET CI GREEN: $RELEASE_MANIFEST"
