#!/usr/bin/env bash
# Build all four local working trees without overwriting any live host binary.
# Run from anywhere; EIGHT_REPO is the parent directory holding the four repos.
set -euo pipefail
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_dir="${EIGHT_REPO:-$(cd "$script_dir/../.." && pwd)}"
task_dir="$(mktemp -d "${TMPDIR:-/tmp}/eight-runtime.XXXXXX")"
image_name="${FLEET_IMAGE:-eight-fleet:local}"
arch="${FLEET_ARCH:-$(docker info --format '{{.Architecture}}')}"
case "$arch" in aarch64|arm64) arch=arm64;; x86_64|amd64) arch=amd64;; *) echo "unsupported arch: $arch" >&2; exit 1;; esac
trap 'rm -rf "$task_dir"' EXIT

# Deliberate source allowlist: no auth files, profiles, logs, .git or models.
# Include working-tree additions so held/uncommitted changes are tested too.
for arm in 8 http-mcp pilot adapters; do
  source_dir="$repo_dir/$arm"
  dest_dir="$task_dir/source/$arm"
  mkdir -p "$dest_dir"
  while IFS= read -r -d '' path; do
    [ -f "$source_dir/$path" ] || continue
    mkdir -p "$dest_dir/$(dirname "$path")"
    cp "$source_dir/$path" "$dest_dir/$path"
  done < <(git -C "$source_dir" ls-files -z --cached --others --exclude-standard -- \
    '*.go' 'go.mod' 'go.sum' '**/go.mod' '**/go.sum' 'build.sh' 'contract/transports/transports.json')
  (cd "$dest_dir" && BUILD_SHA="$(git -C "$source_dir" rev-parse HEAD)" \
    GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOMAXPROCS=2 bash build.sh)
  printf '%s %s dirty=%s\n' "$arm" "$(git -C "$source_dir" rev-parse HEAD)" \
    "$(test -n "$(git -C "$source_dir" status --porcelain)" && echo yes || echo no)" >>"$task_dir/source-manifest.txt"
done

mkdir -p "$task_dir/runtime/8/collector" "$task_dir/runtime/8/web"
cp "$task_dir/source/8/collector/collector" "$task_dir/runtime/8/collector/"
for arm in http-mcp pilot adapters; do
  mkdir -p "$task_dir/runtime/$arm"
  cp -R "$task_dir/source/$arm/.bin" "$task_dir/runtime/$arm/"
done
# The dispatcher's historic mcp atom expects .bin/http; keep the bundle coherent.
ln -s http-mcp "$task_dir/runtime/http-mcp/.bin/http"
# VITE_COLLECTOR_URL=. resolves API routes on this cockpit's own origin.
(cd "$repo_dir/8/web" && VITE_COLLECTOR_URL=. npm run build -- --outDir "$task_dir/runtime/8/web/dist")
cp "$task_dir/source-manifest.txt" "$task_dir/runtime/source-manifest.txt"
cp "$script_dir/Dockerfile" "$task_dir/Dockerfile"
cp "$script_dir/entrypoint.sh" "$task_dir/entrypoint.sh"
docker build --platform "linux/$arch" -t "$image_name" "$task_dir"
echo "Built $image_name ($arch); source revisions:"
cat "$task_dir/source-manifest.txt"
